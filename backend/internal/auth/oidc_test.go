package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// fakeIssuer is a minimal OIDC provider: discovery, JWKS and a token
// endpoint that enforces PKCE. Tests control the ID token claims.
type fakeIssuer struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	signer jose.Signer

	mu        sync.Mutex
	challenge string         // code_challenge from the authorization request
	claims    map[string]any // claims for the next ID token
}

const testClientID = "test-client"

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeIssuer{key: key, signer: signer}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                f.srv.URL,
			"authorization_endpoint":                f.srv.URL + "/authorize",
			"token_endpoint":                        f.srv.URL + "/token",
			"jwks_uri":                              f.srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("GET /jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"},
		}})
	})
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.mu.Lock()
		challenge, claims, signer := f.challenge, f.claims, f.signer
		f.mu.Unlock()

		// PKCE: SHA-256(code_verifier) must equal the challenge sent earlier.
		sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
		if base64.RawURLEncoding.EncodeToString(sum[:]) != challenge {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"PKCE verification failed"}`))
			return
		}
		if r.PostForm.Get("code") != "good-code" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		idToken, err := jwt.Signed(signer).Claims(claims).Serialize()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": idToken,
		})
	})
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	return f
}

// standardClaims returns valid claims for the given nonce.
func (f *fakeIssuer) standardClaims(nonce string) map[string]any {
	now := time.Now()
	return map[string]any{
		"iss": f.srv.URL, "sub": "user-123", "aud": testClientID,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(), "nonce": nonce,
		"email": "alice@example.com", "email_verified": true,
		"name": "Alice", "picture": "https://example.com/a.png",
	}
}

// begin builds the authorization URL and records its PKCE challenge, as a
// real provider would when the browser arrives at /authorize.
func (f *fakeIssuer) begin(t *testing.T, p *OIDCProvider, st FlowState, claims map[string]any) {
	t.Helper()
	u, err := url.Parse(p.AuthCodeURL(st.State, st.Nonce, st.Verifier))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("state") != st.State || q.Get("nonce") != st.Nonce {
		t.Fatalf("authorization URL missing PKCE/state/nonce: %s", u)
	}
	if strings.Contains(u.String(), st.Verifier) {
		t.Fatal("authorization URL must contain the challenge, never the verifier")
	}
	f.mu.Lock()
	f.challenge, f.claims = q.Get("code_challenge"), claims
	f.mu.Unlock()
}

func newTestProvider(t *testing.T, f *fakeIssuer) *OIDCProvider {
	t.Helper()
	p, err := NewOIDCProvider(context.Background(), OIDCConfig{
		IssuerURL: f.srv.URL, ClientID: testClientID, ClientSecret: "secret",
		RedirectURL: "http://localhost:5173/auth/callback",
	})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOIDCExchangeSuccess(t *testing.T) {
	f := newFakeIssuer(t)
	p := newTestProvider(t, f)
	st := NewFlowState("/", time.Now())
	f.begin(t, p, st, f.standardClaims(st.Nonce))

	id, err := p.Exchange(context.Background(), "good-code", st.Verifier, st.Nonce)
	if err != nil {
		t.Fatal(err)
	}
	want := Identity{Issuer: f.srv.URL, Subject: "user-123", Email: "alice@example.com", Name: "Alice", Picture: "https://example.com/a.png"}
	if id != want {
		t.Errorf("identity = %+v, want %+v", id, want)
	}
}

func TestOIDCExchangeRejects(t *testing.T) {
	otherKey, _ := rsa.GenerateKey(rand.Reader, 2048)
	otherSigner, _ := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: otherKey},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", "k1"))

	tests := []struct {
		name     string
		mutate   func(claims map[string]any)
		signer   func(f *fakeIssuer) jose.Signer
		verifier func(st FlowState) string
		code     string
		wantErr  error // nil: any error
	}{
		{name: "nonce mismatch", mutate: func(c map[string]any) { c["nonce"] = "other" }, wantErr: ErrNonceMismatch},
		{name: "email not verified", mutate: func(c map[string]any) { c["email_verified"] = false }, wantErr: ErrEmailNotVerified},
		{name: "wrong audience", mutate: func(c map[string]any) { c["aud"] = "someone-else" }},
		{name: "wrong issuer", mutate: func(c map[string]any) { c["iss"] = "https://evil.example" }},
		{name: "expired token", mutate: func(c map[string]any) { c["exp"] = time.Now().Add(-time.Hour).Unix() }},
		{name: "forged signature", signer: func(*fakeIssuer) jose.Signer { return otherSigner }},
		{name: "wrong PKCE verifier", verifier: func(FlowState) string { return randomToken() }},
		{name: "invalid code", code: "stolen-code"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeIssuer(t)
			p := newTestProvider(t, f)
			st := NewFlowState("/", time.Now())
			claims := f.standardClaims(st.Nonce)
			if tt.mutate != nil {
				tt.mutate(claims)
			}
			f.begin(t, p, st, claims)
			if tt.signer != nil {
				f.mu.Lock()
				f.signer = tt.signer(f)
				f.mu.Unlock()
			}
			verifier, code := st.Verifier, "good-code"
			if tt.verifier != nil {
				verifier = tt.verifier(st)
			}
			if tt.code != "" {
				code = tt.code
			}

			_, err := p.Exchange(context.Background(), code, verifier, st.Nonce)
			if err == nil {
				t.Fatal("want error, got nil")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("err = %v, want %v", err, tt.wantErr)
			}
		})
	}
}
