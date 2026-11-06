// Command devoidc runs a local OpenID Connect provider for development and
// end-to-end tests: sign in as any made-up user without a Google account.
//
// Usage (see README, "Run locally without Google"):
//
//	go run ./cmd/devoidc            # http://localhost:9090, client "dev"/"dev"
//
// and in .env:
//
//	OIDC_ISSUER_URL=http://localhost:9090
//	OIDC_CLIENT_ID=dev
//	OIDC_CLIENT_SECRET=dev
//
// It listens on localhost only and must never be deployed.
package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/AndreyZubov/family-calendar/backend/internal/oidcdev"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "devoidc:", err)
		os.Exit(1)
	}
}

func run() error {
	port := os.Getenv("DEV_OIDC_PORT")
	if port == "" {
		port = "9090"
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	srv, err := oidcdev.New(oidcdev.Config{
		Issuer:       "http://localhost:" + port,
		ClientID:     envOr("DEV_OIDC_CLIENT_ID", "dev"),
		ClientSecret: envOr("DEV_OIDC_CLIENT_SECRET", "dev"),
		Log:          log,
	})
	if err != nil {
		return err
	}
	log.Warn("development identity provider: anyone can sign in as anyone; never deploy this", "url", "http://localhost:"+port)
	hs := &http.Server{
		Addr:              "127.0.0.1:" + port, // localhost only
		Handler:           srv,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := hs.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
