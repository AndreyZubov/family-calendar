// Command server runs the Family Calendar HTTP API.
//
// Startup: load config → connect to Postgres → apply migrations →
// discover the OIDC provider → serve.
// On SIGINT/SIGTERM it stops accepting new connections, lets in-flight
// requests finish (up to SHUTDOWN_TIMEOUT) and closes the database pool.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/AndreyZubov/family-calendar/backend/internal/auth"
	"github.com/AndreyZubov/family-calendar/backend/internal/config"
	"github.com/AndreyZubov/family-calendar/backend/internal/httpapi"
	"github.com/AndreyZubov/family-calendar/backend/internal/store"
)

// version is set at build time: -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	if err := run(); err != nil {
		// The logger may not exist yet (config errors), so use stderr.
		fmt.Fprintln(os.Stderr, "server:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	log := newLogger(cfg)
	slog.SetDefault(log)
	log.Info("starting", "version", version, "env", cfg.Env, "addr", cfg.HTTPAddr)

	// ctx is cancelled on the first SIGINT/SIGTERM (Ctrl+C, docker stop).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.New(ctx, store.Config{URL: cfg.DatabaseURL, MaxConns: cfg.DBMaxConns})
	if err != nil {
		return err
	}
	defer db.Close()
	log.Info("database connected")

	if err := db.Migrate(ctx, log); err != nil {
		return err
	}
	log.Info("migrations up to date")

	authDeps, err := newAuth(ctx, cfg, db)
	if err != nil {
		return err
	}
	go cleanupSessions(ctx, authDeps.Sessions, log)

	api := httpapi.New(httpapi.Deps{Log: log, DB: db, Version: version, Auth: authDeps})
	srv := httpapi.NewServer(cfg.HTTPAddr, api, log)

	serveErr := make(chan error, 1)
	go func() {
		// ListenAndServe always returns a non-nil error; ErrServerClosed
		// means a normal Shutdown.
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
		close(serveErr)
	}()
	api.SetReady(true)
	log.Info("listening", "addr", cfg.HTTPAddr)

	select {
	case err := <-serveErr:
		// e.g. the port is already in use.
		return fmt.Errorf("http server: %w", err)
	case <-ctx.Done():
	}
	stop() // a second Ctrl+C now kills the process immediately

	log.Info("shutting down", "timeout", cfg.ShutdownTimeout.String())
	api.SetReady(false)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	log.Info("stopped")
	return nil
}

// newAuth wires the sign-in flow. OIDC discovery is a network request to the
// provider; it gets its own timeout so a provider outage fails startup fast.
func newAuth(ctx context.Context, cfg config.Config, db *store.Store) (httpapi.AuthDeps, error) {
	discoverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	provider, err := auth.NewOIDCProvider(discoverCtx, auth.OIDCConfig{
		IssuerURL:    cfg.OIDCIssuerURL,
		ClientID:     cfg.OIDCClientID,
		ClientSecret: cfg.OIDCClientSecret,
		RedirectURL:  cfg.OIDCRedirectURL(),
	})
	if err != nil {
		return httpapi.AuthDeps{}, err
	}

	flow, err := auth.NewFlowSealer(cfg.SessionSecret)
	if err != nil {
		return httpapi.AuthDeps{}, fmt.Errorf("login flow sealer: %w", err)
	}

	return httpapi.AuthDeps{
		Provider: provider,
		Sessions: auth.NewSessions(db, cfg.SessionTTL),
		Flow:     flow,
		Cookies:  auth.Cookies{Secure: cfg.SecureCookies()},
		Users:    db,
	}, nil
}

// cleanupSessions deletes expired sessions every hour until ctx is done.
// Expired sessions are already rejected on lookup; this only reclaims space.
func cleanupSessions(ctx context.Context, s *auth.Sessions, log *slog.Logger) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			n, err := s.DeleteExpired(ctx)
			if err != nil {
				log.Warn("cleanup expired sessions", "err", err)
				continue
			}
			if n > 0 {
				log.Info("expired sessions deleted", "count", n)
			}
		}
	}
}

// newLogger returns JSON logs in production (machine-readable, one object
// per line) and human-friendly text logs in development.
func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.IsProduction() {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
