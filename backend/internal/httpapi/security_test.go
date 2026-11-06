package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strings"
	"testing"
	"time"
)

func newSecureAPI(t *testing.T, baseURL string, trusted ...string) *API {
	t.Helper()
	u, _ := url.Parse(baseURL)
	var prefixes []netip.Prefix
	for _, p := range trusted {
		prefixes = append(prefixes, netip.MustParsePrefix(p))
	}
	h := newAuthHarness(t, u.Scheme == "https")
	api, err := New(Deps{
		Log: slog.New(slog.DiscardHandler), DB: fakeDB{}, Version: "test",
		Auth: h.api.deps.Auth, BaseURL: u, TrustedProxies: prefixes,
	})
	if err != nil {
		t.Fatal(err)
	}
	return api
}

func serve(h http.Handler, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body errorBody
	_ = json.NewDecoder(rec.Body).Decode(&body)
	return body.Error.Code
}

func TestSecurityHeaders(t *testing.T) {
	for _, tc := range []struct {
		base     string
		wantHSTS bool
	}{
		{"https://calendar.example.com", true},
		{"http://localhost:5173", false},
	} {
		api := newSecureAPI(t, tc.base)
		// Headers are on every response, including errors.
		for _, path := range []string{"/healthz", "/api/me", "/api/unknown"} {
			rec := serve(api, httptest.NewRequest(http.MethodGet, path, nil))
			h := rec.Header()
			for _, name := range []string{"Content-Security-Policy", "X-Content-Type-Options", "X-Frame-Options",
				"Referrer-Policy", "Cross-Origin-Opener-Policy", "Permissions-Policy"} {
				if h.Get(name) == "" {
					t.Errorf("%s %s: missing %s", tc.base, path, name)
				}
			}
			if csp := h.Get("Content-Security-Policy"); !strings.Contains(csp, "frame-ancestors 'none'") || strings.Contains(csp, "unsafe-inline") {
				t.Errorf("weak CSP: %s", csp)
			}
			if got := h.Get("Strict-Transport-Security") != ""; got != tc.wantHSTS {
				t.Errorf("%s: HSTS present=%v, want %v", tc.base, got, tc.wantHSTS)
			}
		}
	}
}

func TestCSRFProtection(t *testing.T) {
	api := newSecureAPI(t, "https://calendar.example.com")
	logout := func(hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "https://calendar.example.com/auth/logout", nil)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		return serve(api, req)
	}

	blocked := map[string]map[string]string{
		"cross-site fetch":            {"Sec-Fetch-Site": "cross-site", "Origin": "https://evil.example"},
		"same-site (sibling host)":    {"Sec-Fetch-Site": "same-site", "Origin": "https://evil.calendar.example.com"},
		"old browser, foreign Origin": {"Origin": "https://evil.example"},
	}
	for name, hdr := range blocked {
		rec := logout(hdr)
		if rec.Code != http.StatusForbidden || errorCode(t, rec) != "cross_origin" {
			t.Errorf("%s: status=%d, want 403 cross_origin", name, rec.Code)
		}
	}

	allowed := map[string]map[string]string{
		"same-origin fetch":            {"Sec-Fetch-Site": "same-origin", "Origin": "https://calendar.example.com"},
		"old browser, own Origin":      {"Origin": "https://calendar.example.com"},
		"non-browser client (curl)":    {},
		"trusted origin, Host differs": {"Origin": "https://calendar.example.com", "Sec-Fetch-Site": "cross-site"},
	}
	for name, hdr := range allowed {
		if rec := logout(hdr); rec.Code != http.StatusNoContent {
			t.Errorf("%s: status=%d, want 204", name, rec.Code)
		}
	}

	// Safe methods are never blocked: the provider's redirect to /auth/callback
	// is a cross-site GET by design.
	req := httptest.NewRequest(http.MethodGet, "/auth/callback?code=x&state=y", nil)
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	if rec := serve(api, req); rec.Code == http.StatusForbidden {
		t.Error("cross-site GET to callback must not be blocked")
	}
}

func TestBodyLimit(t *testing.T) {
	var readErr error
	h := chain(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, readErr = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}), limitBody)

	// Declared too large: rejected before the handler runs.
	req := httptest.NewRequest(http.MethodPost, "/api/x", bytes.NewReader(make([]byte, MaxBodyBytes+1)))
	if rec := serve(h, req); rec.Code != http.StatusRequestEntityTooLarge || errorCode(t, rec) != "body_too_large" {
		t.Errorf("declared large body: status=%d", rec.Code)
	}

	// Unknown length (chunked) and too large: reading fails in the handler.
	req = httptest.NewRequest(http.MethodPost, "/api/x", io.MultiReader(bytes.NewReader(make([]byte, MaxBodyBytes)), strings.NewReader("x")))
	req.ContentLength = -1
	serve(h, req)
	var maxErr *http.MaxBytesError
	if !errors.As(readErr, &maxErr) {
		t.Errorf("chunked large body: read err = %v, want MaxBytesError", readErr)
	}

	// The limit is part of the real middleware chain.
	api := newSecureAPI(t, "https://calendar.example.com")
	req = httptest.NewRequest(http.MethodPost, "/auth/logout", bytes.NewReader(make([]byte, MaxBodyBytes+1)))
	if rec := serve(api, req); rec.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("API chain without body limit: status=%d", rec.Code)
	}

	// At the limit: fine.
	req = httptest.NewRequest(http.MethodPost, "/api/x", bytes.NewReader(make([]byte, MaxBodyBytes)))
	if rec := serve(h, req); rec.Code != http.StatusOK || readErr != nil {
		t.Errorf("body at limit: status=%d err=%v", rec.Code, readErr)
	}
}

func TestAuthRateLimit(t *testing.T) {
	api := newSecureAPI(t, "https://calendar.example.com")
	login := func(ip string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
		req.RemoteAddr = ip + ":40000"
		return serve(api, req)
	}

	for i := range AuthRateLimit.Burst {
		if rec := login("198.51.100.1"); rec.Code != http.StatusFound {
			t.Fatalf("request %d within burst: status %d", i+1, rec.Code)
		}
	}
	rec := login("198.51.100.1")
	if rec.Code != http.StatusTooManyRequests || errorCode(t, rec) != "rate_limited" {
		t.Fatalf("over burst: status=%d", rec.Code)
	}
	if ra := rec.Header().Get("Retry-After"); ra == "" || ra == "0" {
		t.Errorf("Retry-After = %q", ra)
	}
	// Other clients are unaffected.
	if rec := login("198.51.100.2"); rec.Code != http.StatusFound {
		t.Errorf("other IP: status=%d", rec.Code)
	}
	// Health checks are never limited.
	for range 50 {
		req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
		req.RemoteAddr = "198.51.100.1:40000"
		if rec := serve(api, req); rec.Code != http.StatusOK {
			t.Fatalf("healthz limited: %d", rec.Code)
		}
	}
}

func TestIPLimiterRefillsAndEvicts(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	l := newIPLimiter(RateLimit{Every: time.Second, Burst: 2})
	l.now = func() time.Time { return now }
	ip := netip.MustParseAddr("203.0.113.7")

	l.allow(ip)
	l.allow(ip)
	ok, wait := l.allow(ip)
	if ok || wait <= 0 || wait > time.Second {
		t.Fatalf("third request: ok=%v wait=%v", ok, wait)
	}
	// Rejected requests do not dig the hole deeper.
	for range 10 {
		l.allow(ip)
	}
	now = now.Add(time.Second)
	if ok, _ := l.allow(ip); !ok {
		t.Error("token not refilled after 1s")
	}

	now = now.Add(idleAfter + time.Second)
	l.mu.Lock()
	l.evictIdleLocked(now)
	n := len(l.clients)
	l.mu.Unlock()
	if n != 0 {
		t.Errorf("idle client not evicted, %d left", n)
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("172.16.0.0/12")}
	var got netip.Addr
	h := clientIP(trusted)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = ClientIPFrom(r.Context())
	}))

	cases := []struct {
		name, peer, xff, want string
	}{
		{"direct client, no header", "198.51.100.9:5000", "", "198.51.100.9"},
		{"untrusted peer cannot spoof", "198.51.100.9:5000", "1.2.3.4", "198.51.100.9"},
		{"behind trusted proxy", "172.18.0.3:5000", "203.0.113.5", "203.0.113.5"},
		{"client-supplied prefix ignored", "172.18.0.3:5000", "1.2.3.4, 203.0.113.5", "203.0.113.5"},
		{"chain of trusted proxies", "172.18.0.3:5000", "203.0.113.5, 172.18.0.9", "203.0.113.5"},
		{"malformed hop stops the walk", "172.18.0.3:5000", "203.0.113.5, garbage", "172.18.0.3"},
		{"IPv4-mapped IPv6 peer", "[::ffff:198.51.100.9]:5000", "", "198.51.100.9"},
	}
	for _, c := range cases {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = c.peer
		if c.xff != "" {
			req.Header.Set("X-Forwarded-For", c.xff)
		}
		h.ServeHTTP(httptest.NewRecorder(), req)
		if got.String() != c.want {
			t.Errorf("%s: got %s, want %s", c.name, got, c.want)
		}
	}
}
