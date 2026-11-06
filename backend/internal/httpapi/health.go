package httpapi

import (
	"context"
	"net/http"
	"time"
)

type healthResponse struct {
	Status  string `json:"status"`
	Version string `json:"version,omitempty"`
}

// handleHealthz is the liveness probe: the process is up and serving HTTP.
// It deliberately does not touch the database: a database outage should not
// make the orchestrator restart a healthy app in a loop.
func (a *API) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, r, http.StatusOK, healthResponse{Status: "ok", Version: a.deps.Version})
}

// handleReadyz is the readiness probe: the app can serve real traffic.
// Not ready while starting (migrations), shutting down, or when the database
// is unreachable.
func (a *API) handleReadyz(w http.ResponseWriter, r *http.Request) {
	if !a.ready.Load() {
		writeError(w, r, http.StatusServiceUnavailable, "not_ready", "starting or shutting down")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.deps.DB.Ping(ctx); err != nil {
		logFrom(r.Context()).WarnContext(r.Context(), "readiness: database unreachable", "err", err)
		writeError(w, r, http.StatusServiceUnavailable, "db_unavailable", "database unreachable")
		return
	}

	writeJSON(w, r, http.StatusOK, healthResponse{Status: "ready", Version: a.deps.Version})
}
