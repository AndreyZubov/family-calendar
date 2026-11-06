// Package httpapi contains the HTTP layer: routing, middleware and handlers.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
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
}

// API is the application's HTTP handler.
type API struct {
	deps    Deps
	ready   atomic.Bool
	handler http.Handler
}

// New builds the router and middleware stack. The API starts not ready;
// call SetReady(true) once startup (migrations) is complete.
func New(deps Deps) *API {
	a := &API{deps: deps}

	mux := http.NewServeMux()
	a.routes(mux)

	// Order matters: request ID first so every log line has it; access log
	// outside recover so it records the 500 a panic turns into.
	a.handler = chain(mux,
		requestID,
		accessLog(deps.Log),
		recoverPanic,
	)
	return a
}

func (a *API) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /healthz", a.handleHealthz)
	mux.HandleFunc("GET /readyz", a.handleReadyz)

	// Unknown API paths get a JSON 404 instead of the default text one.
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
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
