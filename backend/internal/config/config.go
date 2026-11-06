// Package config loads application settings from environment variables
// (12-factor). Nothing is read from files: locally the Makefile exports .env,
// in production docker compose passes the variables to the container.
package config

import (
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
}

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
