// Package oidcdev is a minimal OpenID Connect provider for local development
// and end-to-end tests, so the app can be run and tested without a Google
// OAuth client.
//
// It implements just what the app uses: discovery, an authorization endpoint
// with a "sign in as" form (no passwords: anyone can be anyone), a token
// endpoint that enforces PKCE (S256), and a JWKS endpoint. ID tokens are
// RS256-signed with a key generated at startup.
//
// NEVER expose it outside localhost. The app accepts a plain-HTTP issuer only
// in development mode and only on localhost (see package config).
package oidcdev

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// Config configures the provider.
type Config struct {
	Issuer       string // public base URL, e.g. http://localhost:9090
	ClientID     string
	ClientSecret string
	Log          *slog.Logger
}

// Server is the provider's HTTP handler.
type Server struct {
	cfg    Config
	key    *rsa.PrivateKey
	signer jose.Signer
	mux    *http.ServeMux

	mu    sync.Mutex
	codes map[string]grant
}

type grant struct {
	clientID    string
	redirectURI string
	challenge   string
	nonce       string
	email, name string
	expires     time.Time
}

const keyID = "dev-1"

// New creates a provider with a fresh signing key.
func New(cfg Config) (*Server, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader("kid", keyID))
	if err != nil {
		return nil, err
	}
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	s := &Server{cfg: cfg, key: key, signer: signer, mux: http.NewServeMux(), codes: map[string]grant{}}
	s.mux.HandleFunc("GET /.well-known/openid-configuration", s.discovery)
	s.mux.HandleFunc("GET /jwks", s.jwks)
	s.mux.HandleFunc("GET /authorize", s.authorizeForm)
	s.mux.HandleFunc("POST /authorize", s.authorizeSubmit)
	s.mux.HandleFunc("POST /token", s.token)
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                s.cfg.Issuer,
		"authorization_endpoint":                s.cfg.Issuer + "/authorize",
		"token_endpoint":                        s.cfg.Issuer + "/token",
		"jwks_uri":                              s.cfg.Issuer + "/jwks",
		"response_types_supported":              []string{"code"},
		"subject_types_supported":               []string{"public"},
		"id_token_signing_alg_values_supported": []string{"RS256"},
		"code_challenge_methods_supported":      []string{"S256"},
		"token_endpoint_auth_methods_supported": []string{"client_secret_basic", "client_secret_post"},
	})
}

func (s *Server) jwks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
		{Key: &s.key.PublicKey, KeyID: keyID, Algorithm: "RS256", Use: "sig"},
	}})
}

// authParams are the query parameters of an authorization request, carried
// through the form as hidden fields.
var authParams = []string{"client_id", "redirect_uri", "state", "nonce", "code_challenge", "code_challenge_method", "response_type", "scope"}

// Quick-pick identities on the sign-in page.
var presets = []struct{ Name, Email string }{
	{"Alice Smith", "alice@example.com"},
	{"Bob Smith", "bob@example.com"},
	{"Carol Jones", "carol@example.com"},
}

var formTmpl = template.Must(template.New("form").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Dev sign-in</title>
<style>
body{font-family:system-ui,sans-serif;max-width:26rem;margin:3rem auto;padding:0 1rem;color:#0f172a}
.warn{background:#fef3c7;border:1px solid #f59e0b;padding:.6rem .8rem;border-radius:.5rem;font-size:.9rem}
button{width:100%;margin:.3rem 0;padding:.6rem;border-radius:.5rem;border:1px solid #cbd5e1;background:#fff;font:inherit;cursor:pointer}
button.primary{background:#2563eb;color:#fff;border-color:#2563eb}
input{width:100%;padding:.5rem;margin:.2rem 0 .6rem;border:1px solid #cbd5e1;border-radius:.5rem;font:inherit;box-sizing:border-box}
</style></head><body>
<h1>Dev sign-in</h1>
<p class="warn">Local development identity provider. No passwords — anyone can sign in as anyone. Never use in production.</p>
{{range .Presets}}
<form method="post" action="/authorize">{{range $k, $v := $.Params}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}
<input type="hidden" name="email" value="{{.Email}}"><input type="hidden" name="name" value="{{.Name}}">
<button type="submit">Sign in as {{.Name}}</button></form>
{{end}}
<h2>Or someone else</h2>
<form method="post" action="/authorize">{{range $k, $v := .Params}}<input type="hidden" name="{{$k}}" value="{{$v}}">{{end}}
<label>Name<input name="name" required></label><label>Email<input name="email" type="email" required></label>
<button class="primary" type="submit">Sign in</button></form>
</body></html>`))

func (s *Server) authorizeForm(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if msg := s.checkAuthRequest(q); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	params := map[string]string{}
	for _, k := range authParams {
		params[k] = q.Get(k)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = formTmpl.Execute(w, map[string]any{"Params": params, "Presets": presets})
}

func (s *Server) checkAuthRequest(q url.Values) string {
	switch {
	case q.Get("client_id") != s.cfg.ClientID:
		return "unknown client_id"
	case q.Get("response_type") != "code":
		return "response_type must be code"
	case q.Get("code_challenge_method") != "S256" || q.Get("code_challenge") == "":
		return "PKCE with S256 is required"
	case !isLocalRedirect(q.Get("redirect_uri")):
		return "redirect_uri must be http(s)://localhost or 127.0.0.1"
	}
	return ""
}

func isLocalRedirect(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	h := u.Hostname()
	return h == "localhost" || h == "127.0.0.1" || h == "::1"
}

func (s *Server) authorizeSubmit(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	f := r.PostForm
	if msg := s.checkAuthRequest(f); msg != "" {
		http.Error(w, msg, http.StatusBadRequest)
		return
	}
	email := strings.TrimSpace(f.Get("email"))
	name := strings.TrimSpace(f.Get("name"))
	if email == "" || !strings.Contains(email, "@") {
		http.Error(w, "email required", http.StatusBadRequest)
		return
	}
	if name == "" {
		name = email
	}

	code := random()
	s.mu.Lock()
	s.codes[code] = grant{
		clientID: f.Get("client_id"), redirectURI: f.Get("redirect_uri"),
		challenge: f.Get("code_challenge"), nonce: f.Get("nonce"),
		email: email, name: name, expires: time.Now().Add(time.Minute),
	}
	s.mu.Unlock()
	s.cfg.Log.Info("dev oidc: signed in", "email", email)

	u, _ := url.Parse(f.Get("redirect_uri"))
	q := u.Query()
	q.Set("code", code)
	q.Set("state", f.Get("state"))
	u.RawQuery = q.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound) //nolint:gosec // G710: redirect_uri was checked by checkAuthRequest (localhost only)
}

func (s *Server) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		tokenError(w, "invalid_request")
		return
	}
	f := r.PostForm
	clientID, secret, ok := r.BasicAuth()
	if ok {
		clientID, _ = url.QueryUnescape(clientID)
		secret, _ = url.QueryUnescape(secret)
	} else {
		clientID, secret = f.Get("client_id"), f.Get("client_secret")
	}
	if clientID != s.cfg.ClientID || subtle.ConstantTimeCompare([]byte(secret), []byte(s.cfg.ClientSecret)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid_client"})
		return
	}
	if f.Get("grant_type") != "authorization_code" {
		tokenError(w, "unsupported_grant_type")
		return
	}

	s.mu.Lock()
	g, found := s.codes[f.Get("code")]
	delete(s.codes, f.Get("code")) // single use
	s.mu.Unlock()

	sum := sha256.Sum256([]byte(f.Get("code_verifier")))
	switch {
	case !found || time.Now().After(g.expires):
		tokenError(w, "invalid_grant")
		return
	case g.clientID != clientID || g.redirectURI != f.Get("redirect_uri"):
		tokenError(w, "invalid_grant")
		return
	case base64.RawURLEncoding.EncodeToString(sum[:]) != g.challenge:
		tokenError(w, "invalid_grant") // PKCE verification failed
		return
	}

	now := time.Now()
	sub := sha256.Sum256([]byte(strings.ToLower(g.email)))
	claims := map[string]any{
		"iss": s.cfg.Issuer, "aud": clientID, "sub": hex.EncodeToString(sub[:12]),
		"iat": now.Unix(), "exp": now.Add(10 * time.Minute).Unix(), "nonce": g.nonce,
		"email": g.email, "email_verified": true, "name": g.name,
	}
	idToken, err := jwt.Signed(s.signer).Claims(claims).Serialize()
	if err != nil {
		tokenError(w, "server_error")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": random(), "token_type": "Bearer", "expires_in": 600, "id_token": idToken,
	})
}

func tokenError(w http.ResponseWriter, code string) {
	writeJSON(w, http.StatusBadRequest, map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func random() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
