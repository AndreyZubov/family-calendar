// Command server runs the Family Calendar HTTP API.
//
// Startup: load config → connect to Postgres → apply migrations → serve.
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

	api := httpapi.New(httpapi.Deps{Log: log, DB: db, Version: version})
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

// newLogger returns JSON logs in production (machine-readable, one object
// per line) and human-friendly text logs in development.
func newLogger(cfg config.Config) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.IsProduction() {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
