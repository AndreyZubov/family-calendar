// Package httpapi contains the HTTP layer: routing, middleware and handlers.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"net/url"
	"sync/atomic"
	"time"
)

// Pinger checks that a dependency (the database) is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps are the dependencies of the HTTP layer.
type Deps struct {
	Log     *slog.Logger
	DB      Pinger
	Version string
	Auth    AuthDeps

	// BaseURL is the public origin. Used as the trusted origin for CSRF
	// checks; HTTPS enables HSTS.
	BaseURL *url.URL
	// TrustedProxies whose X-Forwarded-For is believed (Caddy).
	TrustedProxies []netip.Prefix
}

// API is the application's HTTP handler.
type API struct {
	deps    Deps
	ready   atomic.Bool
	handler http.Handler

	authLimiter *ipLimiter
	apiLimiter  *ipLimiter
}

// New builds the router and middleware stack. The API starts not ready;
// call SetReady(true) once startup (migrations) is complete.
func New(deps Deps) (*API, error) {
	a := &API{
		deps:        deps,
		authLimiter: newIPLimiter(AuthRateLimit),
		apiLimiter:  newIPLimiter(APIRateLimit),
	}

	csrf, err := csrfProtection(deps.BaseURL)
	if err != nil {
		return nil, err
	}

	mux := http.NewServeMux()
	a.routes(mux)

	// Order matters (outermost first):
	//   requestID   — every later log line carries the ID
	//   clientIP    — real IP for logs and rate limits
	//   accessLog   — outside recover, so it records the 500 a panic becomes
	//   recover     — catches panics from everything below
	//   headers     — set before any handler writes, including error responses
	//   limitBody   — before anything reads the body
	//   csrf        — reject cross-site writes before they reach handlers
	a.handler = chain(mux,
		requestID,
		clientIP(deps.TrustedProxies),
		accessLog(deps.Log),
		recoverPanic,
		securityHeaders(deps.BaseURL != nil && deps.BaseURL.Scheme == "https"),
		limitBody,
		csrf,
	)
	return a, nil
}

// RunBackground runs periodic housekeeping (rate limiter cleanup) until ctx
// is done. Call it in a goroutine.
func (a *API) RunBackground(ctx context.Context) {
	go a.authLimiter.cleanup(ctx)
	a.apiLimiter.cleanup(ctx)
}

func (a *API) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", a.handleHealthz)
	mux.HandleFunc("GET /readyz", a.handleReadyz)

	// Health checks are not rate limited: Docker and monitoring poll them.

	// Sign-in (browser navigations, not fetch calls). Strict rate limit.
	authRoute := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, a.authLimiter.middleware(h))
	}
	authRoute("GET /auth/login", a.handleLogin)
	authRoute("GET /auth/callback", a.handleCallback)
	authRoute("POST /auth/logout", a.handleLogout)

	// API: everything below requires a signed-in user.
	apiRoute := func(pattern string, h http.HandlerFunc) {
		mux.Handle(pattern, a.apiLimiter.middleware(h))
	}
	apiRoute("GET /api/me", a.requireUser(a.handleMe))

	// Unknown API paths get a JSON 404 instead of the default text one.
	apiRoute("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, r, http.StatusNotFound, "not_found", "endpoint not found")
	})
}

// ServeHTTP implements http.Handler.
func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) { a.handler.ServeHTTP(w, r) }

// SetReady switches the readiness probe. It is set to false at the start of
// shutdown so a load balancer stops sending new requests.
func (a *API) SetReady(ready bool) { a.ready.Store(ready) }

// NewServer wraps the handler in an http.Server with timeouts. Without them
// a slow or malicious client could hold connections open indefinitely.
func NewServer(addr string, h http.Handler, log *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 5 * time.Second,  // slowloris protection
		ReadTimeout:       15 * time.Second, // whole request incl. body
		WriteTimeout:      30 * time.Second, // handler + response
		IdleTimeout:       60 * time.Second, // keep-alive between requests
		MaxHeaderBytes:    32 << 10,         // 32 KiB is plenty for cookies
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}
}
