package oidcdev_test

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/AndreyZubov/family-calendar/backend/internal/auth"
	"github.com/AndreyZubov/family-calendar/backend/internal/oidcdev"
)

// TestRealClientAgainstDevProvider runs the app's actual OIDC client code
// (discovery, PKCE, token exchange, ID token verification) against the dev
// provider, exactly as a local sign-in does.
func TestRealClientAgainstDevProvider(t *testing.T) {
	// The issuer URL is only known once the server listens, so the handler
	// is attached after creation.
	var dev *oidcdev.Server
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dev.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	var err error
	dev, err = oidcdev.New(oidcdev.Config{Issuer: srv.URL, ClientID: "dev", ClientSecret: "dev", Log: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	p, err := auth.NewOIDCProvider(ctx, auth.OIDCConfig{
		IssuerURL: srv.URL, ClientID: "dev", ClientSecret: "dev", RedirectURL: "http://localhost:5173/auth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	signIn := func(st auth.FlowState) string {
		authURL, _ := url.Parse(p.AuthCodeURL(st.State, st.Nonce, st.Verifier))
		form := authURL.Query()
		form.Set("email", "alice@example.com")
		form.Set("name", "Alice")
		resp, err := noRedirect.PostForm(srv.URL+"/authorize", form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		loc, _ := url.Parse(resp.Header.Get("Location"))
		if resp.StatusCode != http.StatusFound || loc.Query().Get("state") != st.State {
			t.Fatalf("authorize: %d %s", resp.StatusCode, loc)
		}
		return loc.Query().Get("code")
	}

	st := auth.NewFlowState("/", time.Now())
	id, err := p.Exchange(ctx, signIn(st), st.Verifier, st.Nonce)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if id.Email != "alice@example.com" || id.Name != "Alice" || id.Issuer != srv.URL || id.Subject == "" {
		t.Errorf("identity = %+v", id)
	}

	// The same email always maps to the same subject (stable user).
	st2 := auth.NewFlowState("/", time.Now())
	id2, err := p.Exchange(ctx, signIn(st2), st2.Verifier, st2.Nonce)
	if err != nil || id2.Subject != id.Subject {
		t.Errorf("second sign-in: %+v, %v", id2, err)
	}

	// Wrong PKCE verifier and replayed codes are rejected.
	st3 := auth.NewFlowState("/", time.Now())
	code := signIn(st3)
	if _, err := p.Exchange(ctx, code, auth.NewFlowState("/", time.Now()).Verifier, st3.Nonce); err == nil {
		t.Error("wrong PKCE verifier accepted")
	}
	if _, err := p.Exchange(ctx, code, st3.Verifier, st3.Nonce); err == nil {
		t.Error("code reused after a failed exchange")
	}
}

func TestAuthorizeRejectsBadRequests(t *testing.T) {
	d, err := oidcdev.New(oidcdev.Config{Issuer: "http://localhost:9090", ClientID: "dev", ClientSecret: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	good := url.Values{
		"client_id": {"dev"}, "response_type": {"code"}, "redirect_uri": {"http://localhost:5173/auth/callback"},
		"code_challenge": {"x"}, "code_challenge_method": {"S256"}, "state": {"s"}, "nonce": {"n"},
	}
	cases := map[string]func(url.Values){
		"unknown client":   func(v url.Values) { v.Set("client_id", "other") },
		"no PKCE":          func(v url.Values) { v.Del("code_challenge") },
		"plain PKCE":       func(v url.Values) { v.Set("code_challenge_method", "plain") },
		"foreign redirect": func(v url.Values) { v.Set("redirect_uri", "https://evil.example/cb") },
		"implicit flow":    func(v url.Values) { v.Set("response_type", "token") },
	}
	for name, mutate := range cases {
		v := url.Values{}
		for k, vs := range good {
			v[k] = append([]string(nil), vs...)
		}
		mutate(v)
		rec := httptest.NewRecorder()
		d.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/authorize?"+v.Encode(), nil))
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", name, rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	d.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/authorize?"+good.Encode(), nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Sign in as Alice Smith") {
		t.Errorf("valid request: %d", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "<script") {
		t.Error("form must not contain scripts")
	}
}
