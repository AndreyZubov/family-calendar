package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/AndreyZubov/family-calendar/backend/internal/auth"
	"github.com/AndreyZubov/family-calendar/backend/internal/store/db"
)

// fakeProvider stands in for Google. AuthCodeURL encodes state and nonce so
// the test can play the provider's role; Exchange checks the code and
// verifier the way a real provider would.
type fakeProvider struct {
	mu       sync.Mutex
	verifier string // expected PKCE verifier (captured at AuthCodeURL)
	nonce    string
	identity auth.Identity
	err      error
}

func (p *fakeProvider) AuthCodeURL(state, nonce, verifier string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.verifier, p.nonce = verifier, nonce
	return "https://idp.example/authorize?" + url.Values{"state": {state}, "nonce": {nonce}}.Encode()
}

func (p *fakeProvider) Exchange(_ context.Context, code, verifier, nonce string) (auth.Identity, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.err != nil {
		return auth.Identity{}, p.err
	}
	if code != "good-code" || verifier != p.verifier || nonce != p.nonce {
		return auth.Identity{}, errors.New("invalid_grant")
	}
	return p.identity, nil
}

// memDB is an in-memory implementation of the queries used by auth.
type memDB struct {
	mu       sync.Mutex
	users    map[string]db.User // key: issuer|subject
	sessions map[string]db.Session
	members  map[uuid.UUID]db.GetMembershipByUserRow
}

func newMemDB() *memDB {
	return &memDB{users: map[string]db.User{}, sessions: map[string]db.Session{}, members: map[uuid.UUID]db.GetMembershipByUserRow{}}
}

func (m *memDB) UpsertUserByOIDC(_ context.Context, a db.UpsertUserByOIDCParams) (db.User, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := a.OidcIssuer + "|" + a.OidcSubject
	u, ok := m.users[k]
	if !ok {
		u = db.User{ID: uuid.New(), OidcIssuer: a.OidcIssuer, OidcSubject: a.OidcSubject}
	}
	u.Email, u.Name, u.AvatarUrl = a.Email, a.Name, a.AvatarUrl
	m.users[k] = u
	return u, nil
}

func (m *memDB) GetMembershipByUser(_ context.Context, id uuid.UUID) (db.GetMembershipByUserRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.members[id]; ok {
		return r, nil
	}
	return db.GetMembershipByUserRow{}, pgx.ErrNoRows
}

func (m *memDB) userByID(id uuid.UUID) (db.User, bool) {
	for _, u := range m.users {
		if u.ID == id {
			return u, true
		}
	}
	return db.User{}, false
}

func (m *memDB) CreateSession(_ context.Context, a db.CreateSessionParams) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[string(a.TokenHash)] = db.Session{TokenHash: a.TokenHash, UserID: a.UserID, LastSeenAt: time.Now(), ExpiresAt: a.ExpiresAt}
	return nil
}

func (m *memDB) GetSessionUser(_ context.Context, h []byte) (db.GetSessionUserRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[string(h)]
	if !ok || !s.ExpiresAt.After(time.Now()) {
		return db.GetSessionUserRow{}, pgx.ErrNoRows
	}
	u, _ := m.userByID(s.UserID)
	return db.GetSessionUserRow{User: u, SessionExpiresAt: s.ExpiresAt, SessionLastSeenAt: s.LastSeenAt}, nil
}

func (m *memDB) TouchSession(context.Context, db.TouchSessionParams) error { return nil }

func (m *memDB) DeleteSession(_ context.Context, h []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, string(h))
	return nil
}

func (m *memDB) DeleteExpiredSessions(context.Context) (int64, error) { return 0, nil }

func (m *memDB) sessionCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

type authHarness struct {
	api      *API
	provider *fakeProvider
	db       *memDB
	cookies  auth.Cookies
	logs     *bytes.Buffer
}

func newAuthHarness(t *testing.T, secure bool) *authHarness {
	t.Helper()
	flow, err := auth.NewFlowSealer(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	mdb := newMemDB()
	p := &fakeProvider{identity: auth.Identity{
		Issuer: "https://idp.example", Subject: "alice-sub", Email: "alice@example.com",
		Name: "Alice", Picture: "https://idp.example/alice.png",
	}}
	cookies := auth.Cookies{Secure: secure}
	var logs bytes.Buffer
	api, err := New(Deps{
		Log: slog.New(slog.NewJSONHandler(&logs, nil)), DB: fakeDB{}, Version: "test",
		Auth: AuthDeps{
			Provider: p, Sessions: auth.NewSessions(mdb, 24*time.Hour), Flow: flow,
			Cookies: cookies, Users: mdb,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &authHarness{api: api, provider: p, db: mdb, cookies: cookies, logs: &logs}
}

// request sends a request with the given cookies and returns the response.
func (h *authHarness) request(method, target string, cookies ...*http.Cookie) *http.Response {
	req := httptest.NewRequest(method, target, nil)
	for _, c := range cookies {
		req.AddCookie(&http.Cookie{Name: c.Name, Value: c.Value})
	}
	rec := httptest.NewRecorder()
	h.api.ServeHTTP(rec, req)
	return rec.Result()
}

func findCookie(resp *http.Response, name string) *http.Cookie {
	for _, c := range resp.Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// login runs /auth/login and returns the flow cookie and the state the
// provider received.
func (h *authHarness) login(t *testing.T, returnTo string) (*http.Cookie, string) {
	t.Helper()
	target := "/auth/login"
	if returnTo != "" {
		target += "?return_to=" + url.QueryEscape(returnTo)
	}
	resp := h.request(http.MethodGet, target)
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	loc, _ := url.Parse(resp.Header.Get("Location"))
	if loc.Host != "idp.example" {
		t.Fatalf("login must redirect to the provider, got %s", loc)
	}
	flow := findCookie(resp, h.cookies.FlowName())
	if flow == nil {
		t.Fatal("no flow cookie")
	}
	return flow, loc.Query().Get("state")
}

func TestLoginFlowEndToEnd(t *testing.T) {
	h := newAuthHarness(t, true)

	// 1. Not signed in yet.
	if resp := h.request(http.MethodGet, "/api/me"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("/api/me before login = %d", resp.StatusCode)
	}

	// 2. Start login.
	flow, state := h.login(t, "/calendar")
	if flow.Name != "__Host-auth_flow" || !flow.HttpOnly || !flow.Secure || flow.SameSite != http.SameSiteLaxMode || flow.Path != "/" {
		t.Errorf("flow cookie attributes: %+v", flow)
	}

	// 3. Provider redirects back with code and state.
	resp := h.request(http.MethodGet, "/auth/callback?code=good-code&state="+url.QueryEscape(state), flow)
	if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/calendar" {
		t.Fatalf("callback: status=%d location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	sess := findCookie(resp, "__Host-session")
	if sess == nil || !sess.HttpOnly || !sess.Secure || sess.SameSite != http.SameSiteLaxMode || sess.MaxAge != 24*3600 {
		t.Fatalf("session cookie: %+v", sess)
	}
	if c := findCookie(resp, "__Host-auth_flow"); c == nil || c.MaxAge >= 0 {
		t.Errorf("flow cookie must be deleted after callback: %+v", c)
	}

	// 4. Signed in.
	resp = h.request(http.MethodGet, "/api/me", sess)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/me = %d", resp.StatusCode)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q", cc)
	}
	var me meResponse
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatal(err)
	}
	if me.User.Email != "alice@example.com" || me.User.Name != "Alice" || me.Family != nil {
		t.Errorf("me = %+v", me)
	}

	// 5. Logout.
	resp = h.request(http.MethodPost, "/auth/logout", sess)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout = %d", resp.StatusCode)
	}
	if c := findCookie(resp, "__Host-session"); c == nil || c.MaxAge >= 0 {
		t.Errorf("session cookie not cleared: %+v", c)
	}
	if h.db.sessionCount() != 0 {
		t.Error("session not deleted on the server")
	}

	// 6. The old cookie no longer works (server-side revocation).
	if resp := h.request(http.MethodGet, "/api/me", sess); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("/api/me after logout = %d", resp.StatusCode)
	}
}

func TestCallbackRejects(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(h *authHarness)
		query   func(state string) string
		noFlow  bool
		badFlow bool
		reason  string
	}{
		{name: "state mismatch (login CSRF)", query: func(string) string { return "code=good-code&state=attacker" }, reason: "state_mismatch"},
		{name: "no flow cookie", noFlow: true, query: func(s string) string { return "code=good-code&state=" + s }, reason: "flow_expired"},
		{name: "forged flow cookie", badFlow: true, query: func(s string) string { return "code=good-code&state=" + s }, reason: "flow_expired"},
		{name: "user cancelled", query: func(s string) string { return "error=access_denied&state=" + s }, reason: "provider_error"},
		{name: "missing code", query: func(s string) string { return "state=" + s }, reason: "missing_code"},
		{name: "bad code", query: func(s string) string { return "code=stolen&state=" + s }, reason: "exchange_failed"},
		{
			name:   "email not verified",
			setup:  func(h *authHarness) { h.provider.err = auth.ErrEmailNotVerified },
			query:  func(s string) string { return "code=good-code&state=" + s },
			reason: "email_not_verified",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newAuthHarness(t, true)
			if tt.setup != nil {
				tt.setup(h)
			}
			flow, state := h.login(t, "")
			var cookies []*http.Cookie
			switch {
			case tt.noFlow:
			case tt.badFlow:
				cookies = append(cookies, &http.Cookie{Name: flow.Name, Value: flow.Value[:len(flow.Value)-4] + "AAAA"})
			default:
				cookies = append(cookies, flow)
			}

			resp := h.request(http.MethodGet, "/auth/callback?"+tt.query(url.QueryEscape(state)), cookies...)
			want := "/login?error=" + tt.reason
			if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != want {
				t.Errorf("status=%d location=%q, want 302 %q", resp.StatusCode, resp.Header.Get("Location"), want)
			}
			if findCookie(resp, "__Host-session") != nil {
				t.Error("no session cookie may be set on failure")
			}
			if h.db.sessionCount() != 0 {
				t.Error("no session may be created on failure")
			}
		})
	}
}

func TestCallbackFlowCookieIsSingleUse(t *testing.T) {
	h := newAuthHarness(t, true)
	flow, state := h.login(t, "")
	target := "/auth/callback?code=good-code&state=" + url.QueryEscape(state)

	if resp := h.request(http.MethodGet, target, flow); findCookie(resp, "__Host-session") == nil {
		t.Fatal("first callback should sign in")
	}
	// Browser deleted the flow cookie; an attacker replaying the URL without
	// it gets nothing.
	if resp := h.request(http.MethodGet, target); resp.Header.Get("Location") != "/login?error=flow_expired" {
		t.Errorf("replay without flow cookie: %q", resp.Header.Get("Location"))
	}
}

func TestLoginRotatesSession(t *testing.T) {
	h := newAuthHarness(t, true)
	signIn := func(old ...*http.Cookie) *http.Cookie {
		flow, state := h.login(t, "")
		resp := h.request(http.MethodGet, "/auth/callback?code=good-code&state="+url.QueryEscape(state), append(old, flow)...)
		return findCookie(resp, "__Host-session")
	}
	first := signIn()
	second := signIn(first)
	if first.Value == second.Value {
		t.Error("login must issue a new session token")
	}
	if h.db.sessionCount() != 1 {
		t.Errorf("previous session of this browser must be destroyed, have %d", h.db.sessionCount())
	}
	if resp := h.request(http.MethodGet, "/api/me", first); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("old session still valid: %d", resp.StatusCode)
	}
}

func TestLoginRejectsOpenRedirect(t *testing.T) {
	h := newAuthHarness(t, true)
	flow, state := h.login(t, "https://evil.example/phish")
	resp := h.request(http.MethodGet, "/auth/callback?code=good-code&state="+url.QueryEscape(state), flow)
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("redirect after login = %q, want /", loc)
	}
}

func TestDevCookiesWithoutHostPrefix(t *testing.T) {
	h := newAuthHarness(t, false)
	flow, _ := h.login(t, "")
	if flow.Name != "auth_flow" || flow.Secure {
		t.Errorf("dev flow cookie: %+v", flow)
	}
}

func TestUnauthenticatedRequests(t *testing.T) {
	h := newAuthHarness(t, true)
	cases := map[string]*http.Cookie{
		"no cookie":      nil,
		"garbage cookie": {Name: "__Host-session", Value: "garbage"},
		"unknown token":  {Name: "__Host-session", Value: strings.Repeat("A", 43)},
	}
	for name, c := range cases {
		var cs []*http.Cookie
		if c != nil {
			cs = append(cs, c)
		}
		resp := h.request(http.MethodGet, "/api/me", cs...)
		var body errorBody
		_ = json.NewDecoder(resp.Body).Decode(&body)
		if resp.StatusCode != http.StatusUnauthorized || body.Error.Code != "unauthenticated" {
			t.Errorf("%s: status=%d body=%+v", name, resp.StatusCode, body)
		}
	}
}

func TestLogoutRequiresPOST(t *testing.T) {
	h := newAuthHarness(t, true)
	if resp := h.request(http.MethodGet, "/auth/logout"); resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /auth/logout = %d, want 405", resp.StatusCode)
	}
}

func TestAuthCodeNotLogged(t *testing.T) {
	h := newAuthHarness(t, true)
	flow, state := h.login(t, "")
	h.request(http.MethodGet, "/auth/callback?code=good-code&state="+url.QueryEscape(state), flow)
	for _, secret := range []string{"good-code", state, flow.Value} {
		if strings.Contains(h.logs.String(), secret) {
			t.Errorf("logs contain a secret %q", secret)
		}
	}
}

func TestAccessLogIncludesUserID(t *testing.T) {
	h := newAuthHarness(t, true)
	flow, state := h.login(t, "")
	resp := h.request(http.MethodGet, "/auth/callback?code=good-code&state="+url.QueryEscape(state), flow)
	sess := findCookie(resp, "__Host-session")
	h.logs.Reset()

	h.request(http.MethodGet, "/api/me", sess)
	var line map[string]any
	for _, l := range strings.Split(strings.TrimSpace(h.logs.String()), "\n") {
		_ = json.Unmarshal([]byte(l), &line)
		if line["msg"] == "http request" {
			break
		}
	}
	if line["msg"] != "http request" || line["user_id"] == nil || line["user_id"] == "" {
		t.Errorf("access log line lacks user_id: %v", line)
	}
}
