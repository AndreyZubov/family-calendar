package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeDB struct{ err error }

func (f fakeDB) Ping(context.Context) error { return f.err }

func newTestAPI(t *testing.T, db Pinger) (*API, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	api, err := New(Deps{Log: log, DB: db, Version: "test"})
	if err != nil {
		t.Fatal(err)
	}
	return api, &logs
}

func do(h http.Handler, method, path string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHealthz(t *testing.T) {
	// Liveness does not depend on readiness or the database.
	api, _ := newTestAPI(t, fakeDB{err: errors.New("down")})
	rec := do(api, http.MethodGet, "/healthz", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body healthResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil || body.Status != "ok" || body.Version != "test" {
		t.Errorf("body = %+v err=%v", body, err)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestReadyz(t *testing.T) {
	tests := []struct {
		name  string
		ready bool
		dbErr error
		want  int
		code  string
	}{
		{"starting", false, nil, http.StatusServiceUnavailable, "not_ready"},
		{"db down", true, errors.New("connection refused"), http.StatusServiceUnavailable, "db_unavailable"},
		{"ready", true, nil, http.StatusOK, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			api, _ := newTestAPI(t, fakeDB{err: tt.dbErr})
			api.SetReady(tt.ready)
			rec := do(api, http.MethodGet, "/readyz", nil)
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d", rec.Code, tt.want)
			}
			if tt.code != "" {
				var body errorBody
				_ = json.NewDecoder(rec.Body).Decode(&body)
				if body.Error.Code != tt.code {
					t.Errorf("error code = %q, want %q", body.Error.Code, tt.code)
				}
			}
		})
	}
}

func TestMethodNotAllowed(t *testing.T) {
	api, _ := newTestAPI(t, fakeDB{})
	rec := do(api, http.MethodPost, "/healthz", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestUnknownAPIPathReturnsJSON404(t *testing.T) {
	api, _ := newTestAPI(t, fakeDB{})
	rec := do(api, http.MethodGet, "/api/nope", nil)
	var body errorBody
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if rec.Code != http.StatusNotFound || body.Error.Code != "not_found" {
		t.Errorf("status=%d body=%+v", rec.Code, body)
	}
}

func TestRequestID(t *testing.T) {
	api, logs := newTestAPI(t, fakeDB{})

	// Generated when absent.
	rec := do(api, http.MethodGet, "/healthz", nil)
	id := rec.Header().Get(RequestIDHeader)
	if len(id) != 24 {
		t.Errorf("generated id = %q", id)
	}
	if !strings.Contains(logs.String(), `"request_id":"`+id+`"`) {
		t.Errorf("access log lacks request_id %s:\n%s", id, logs)
	}

	// A well-formed incoming ID (e.g. from Caddy) is kept.
	rec = do(api, http.MethodGet, "/healthz", map[string]string{RequestIDHeader: "abc-123"})
	if got := rec.Header().Get(RequestIDHeader); got != "abc-123" {
		t.Errorf("incoming id not kept: %q", got)
	}

	// Garbage is replaced, so clients cannot inject text into logs.
	rec = do(api, http.MethodGet, "/healthz", map[string]string{RequestIDHeader: "bad id\nlevel=ERROR"})
	if got := rec.Header().Get(RequestIDHeader); got == "bad id\nlevel=ERROR" || len(got) != 24 {
		t.Errorf("invalid id not replaced: %q", got)
	}
}

func TestRecoverPanic(t *testing.T) {
	var logs bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&logs, nil))
	h := chain(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	}), requestID, accessLog(log), recoverPanic)

	rec := do(h, http.MethodGet, "/api/x", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	var body errorBody
	_ = json.NewDecoder(rec.Body).Decode(&body)
	if body.Error.Code != "internal" || strings.Contains(rec.Body.String(), "boom") {
		t.Errorf("response must be generic, got %q", rec.Body.String())
	}
	out := logs.String()
	if !strings.Contains(out, "panic in handler") || !strings.Contains(out, "boom") || !strings.Contains(out, `"status":500`) {
		t.Errorf("panic not logged properly:\n%s", out)
	}
}

func TestAccessLogOmitsQueryString(t *testing.T) {
	api, logs := newTestAPI(t, fakeDB{})
	do(api, http.MethodGet, "/api/callback?code=secret-oauth-code", nil)
	if strings.Contains(logs.String(), "secret-oauth-code") {
		t.Errorf("query string leaked into logs:\n%s", logs)
	}
}

func TestNewServerHasTimeouts(t *testing.T) {
	srv := NewServer(":0", http.NotFoundHandler(), slog.New(slog.DiscardHandler))
	if srv.ReadHeaderTimeout == 0 || srv.ReadTimeout == 0 || srv.WriteTimeout == 0 || srv.IdleTimeout == 0 || srv.MaxHeaderBytes == 0 {
		t.Errorf("server without limits: %+v", srv)
	}
}

func TestRunBackgroundStopsWithContext(t *testing.T) {
	api, _ := newTestAPI(t, fakeDB{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { api.RunBackground(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("RunBackground did not return after cancel")
	}
}
