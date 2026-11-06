package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"time"
)

// Middleware wraps a handler with extra behavior.
type Middleware func(http.Handler) http.Handler

// chain applies middlewares so that the first one listed runs first.
func chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type ctxKey int

const (
	requestIDKey ctxKey = iota
	loggerKey
)

// RequestIDHeader carries the request ID to and from clients and proxies.
const RequestIDHeader = "X-Request-ID"

// Incoming IDs (e.g. from Caddy) are trusted only if they look sane, so a
// client cannot inject arbitrary text into our logs.
var validRequestID = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// requestID assigns every request an ID, stores it in the context and echoes
// it in the response, so a user-visible error can be matched to log lines.
func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get(RequestIDHeader)
		if !validRequestID.MatchString(id) {
			id = newRequestID()
		}
		w.Header().Set(RequestIDHeader, id)
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newRequestID() string {
	var b [12]byte
	_, _ = rand.Read(b[:]) // crypto/rand.Read never returns an error
	return hex.EncodeToString(b[:])
}

// RequestIDFrom returns the request ID stored in ctx, or "".
func RequestIDFrom(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey).(string)
	return id
}

// logFrom returns the request-scoped logger (with request_id attached), or
// the default logger outside a request.
func logFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}

// accessLog puts a request-scoped logger into the context and writes one
// line per request after it completes. Health checks are logged at debug
// level: Docker probes them every few seconds.
func accessLog(base *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			log := base.With(slog.String("request_id", RequestIDFrom(r.Context())))
			ctx := context.WithValue(r.Context(), loggerKey, log)

			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r.WithContext(ctx))

			level := slog.LevelInfo
			switch {
			case rec.status >= 500:
				level = slog.LevelError
			case r.URL.Path == "/healthz" || r.URL.Path == "/readyz":
				level = slog.LevelDebug
			}
			// Log the path only: query strings may carry tokens (e.g. the
			// OIDC callback's ?code=...).
			log.LogAttrs(ctx, level, "http request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rec.status),
				slog.Int64("bytes", rec.bytes),
				slog.Float64("duration_ms", float64(time.Since(start).Microseconds())/1000),
				slog.String("remote_addr", r.RemoteAddr),
			)
		})
	}
}

// recoverPanic turns a panic in a handler into a 500 response and an error
// log with a stack trace, instead of a dropped connection.
func recoverPanic(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			p := recover()
			if p == nil {
				return
			}
			// http.ErrAbortHandler is the stdlib's way to abort silently.
			if err, ok := p.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(p)
			}
			logFrom(r.Context()).ErrorContext(r.Context(), "panic in handler",
				slog.Any("panic", p),
				slog.String("stack", string(debug.Stack())),
			)
			// If the handler already started the response, the status line is
			// gone and cannot be replaced; the access log still records it.
			if rec, ok := w.(*statusRecorder); ok && rec.wroteHeader {
				return
			}
			writeError(w, r, http.StatusInternalServerError, "internal", "internal server error")
		}()
		next.ServeHTTP(w, r)
	})
}

// statusRecorder captures the status code and size of a response.
type statusRecorder struct {
	http.ResponseWriter
	status      int
	bytes       int64
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(code int) {
	if !r.wroteHeader {
		r.status = code
		r.wroteHeader = true
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wroteHeader = true
	n, err := r.ResponseWriter.Write(b)
	r.bytes += int64(n)
	return n, err
}

// Unwrap lets http.ResponseController reach the underlying writer
// (flushing, per-request deadlines).
func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }
