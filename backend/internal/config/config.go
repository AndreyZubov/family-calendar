// Package config loads application settings from environment variables
// (12-factor). Nothing is read from files: locally the Makefile exports .env,
// in production docker compose passes the variables to the container.
package config

import (
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Environments.
const (
	EnvDevelopment = "development"
	EnvProduction  = "production"
)

// Config is the complete application configuration.
type Config struct {
	// Env is "development" or "production". Production enables JSON logs
	// and stricter defaults.
	Env string
	// HTTPAddr is the listen address, e.g. ":8080".
	HTTPAddr string
	// BaseURL is the public URL users open in the browser.
	BaseURL *url.URL
	// LogLevel is the minimum level written to the log.
	LogLevel slog.Level

	// DatabaseURL is the Postgres connection string. Contains a password:
	// never log it.
	DatabaseURL string
	// DBMaxConns caps the connection pool size.
	DBMaxConns int32

	// ShutdownTimeout is how long in-flight requests get to finish on stop.
	ShutdownTimeout time.Duration

	// OIDC identity provider (Google by default).
	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCClientSecret string // secret: never log

	// SessionSecret is the decoded SESSION_SECRET (>= 32 bytes). Used to
	// derive keys, e.g. for the encrypted login-flow cookie. Secret.
	SessionSecret []byte
	// SessionTTL is how long a session lives without activity.
	SessionTTL time.Duration
}

// OIDCRedirectURL is where the provider sends the browser after sign-in.
// It must be registered in the provider's console exactly as returned here.
func (c Config) OIDCRedirectURL() string { return c.BaseURL.String() + "/auth/callback" }

// SecureCookies reports whether cookies must be marked Secure (HTTPS).
func (c Config) SecureCookies() bool { return c.BaseURL.Scheme == "https" }

// IsProduction reports whether the app runs in production mode.
func (c Config) IsProduction() bool { return c.Env == EnvProduction }

// Load reads the configuration using getenv (os.Getenv in main; a map in
// tests). All problems are reported together, not just the first one.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}

	cfg := Config{
		Env:         get("APP_ENV", EnvDevelopment),
		HTTPAddr:    get("HTTP_ADDR", ":8080"),
		DatabaseURL: get("DATABASE_URL", ""),

		OIDCIssuerURL:    get("OIDC_ISSUER_URL", "https://accounts.google.com"),
		OIDCClientID:     get("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: get("OIDC_CLIENT_SECRET", ""),
	}

	if cfg.Env != EnvDevelopment && cfg.Env != EnvProduction {
		errs = append(errs, fmt.Errorf("APP_ENV: must be %q or %q, got %q", EnvDevelopment, EnvProduction, cfg.Env))
	}

	baseURL, err := parseBaseURL(get("APP_BASE_URL", "http://localhost:5173"))
	if err != nil {
		errs = append(errs, fmt.Errorf("APP_BASE_URL: %w", err))
	} else {
		cfg.BaseURL = baseURL
		if cfg.Env == EnvProduction && baseURL.Scheme != "https" {
			errs = append(errs, errors.New("APP_BASE_URL: must use https in production"))
		}
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(get("LOG_LEVEL", "info"))); err != nil {
		errs = append(errs, fmt.Errorf("LOG_LEVEL: %w", err))
	}

	if cfg.DatabaseURL == "" {
		errs = append(errs, errors.New("DATABASE_URL: required"))
	}

	maxConns, err := strconv.ParseInt(get("DB_MAX_CONNS", "5"), 10, 32)
	if err != nil || maxConns < 1 || maxConns > 50 {
		errs = append(errs, errors.New("DB_MAX_CONNS: must be an integer between 1 and 50"))
	}
	cfg.DBMaxConns = int32(maxConns)

	cfg.ShutdownTimeout, err = time.ParseDuration(get("SHUTDOWN_TIMEOUT", "15s"))
	if err != nil || cfg.ShutdownTimeout <= 0 {
		errs = append(errs, errors.New("SHUTDOWN_TIMEOUT: must be a positive duration, e.g. 15s"))
	}

	if u, err := url.Parse(cfg.OIDCIssuerURL); err != nil || u.Scheme != "https" || u.Host == "" {
		errs = append(errs, errors.New("OIDC_ISSUER_URL: must be an https URL"))
	}
	if cfg.OIDCClientID == "" {
		errs = append(errs, errors.New("OIDC_CLIENT_ID: required"))
	}
	if cfg.OIDCClientSecret == "" {
		errs = append(errs, errors.New("OIDC_CLIENT_SECRET: required"))
	}

	// Error messages never include the secret's value.
	secret, err := base64.StdEncoding.DecodeString(get("SESSION_SECRET", ""))
	if err != nil || len(secret) < 32 {
		errs = append(errs, errors.New("SESSION_SECRET: required, base64 of at least 32 random bytes (openssl rand -base64 32)"))
	}
	cfg.SessionSecret = secret

	cfg.SessionTTL, err = time.ParseDuration(get("SESSION_TTL", "720h"))
	if err != nil || cfg.SessionTTL < time.Hour {
		errs = append(errs, errors.New("SESSION_TTL: must be a duration of at least 1h, e.g. 720h"))
	}

	if err := errors.Join(errs...); err != nil {
		return Config{}, fmt.Errorf("invalid configuration:\n%w", err)
	}
	return cfg, nil
}

func parseBaseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, errors.New("not a valid URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, errors.New("scheme must be http or https")
	}
	if u.Host == "" {
		return nil, errors.New("host is required")
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("must not contain a path, query or fragment")
	}
	u.Path = ""
	return u, nil
}
