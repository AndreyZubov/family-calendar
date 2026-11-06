package config

import (
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// testSecret is base64 of 32 zero bytes.
const testSecret = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="

// valid returns a minimal valid environment with overrides applied.
func valid(overrides map[string]string) func(string) string {
	m := map[string]string{
		"DATABASE_URL":       "postgres://u:p@localhost/db",
		"OIDC_CLIENT_ID":     "client-id",
		"OIDC_CLIENT_SECRET": "client-secret",
		"SESSION_SECRET":     testSecret,
	}
	for k, v := range overrides {
		m[k] = v
	}
	return env(m)
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(valid(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Env != EnvDevelopment || cfg.IsProduction() {
		t.Errorf("Env = %q", cfg.Env)
	}
	if cfg.HTTPAddr != ":8080" {
		t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
	}
	if cfg.BaseURL.String() != "http://localhost:5173" {
		t.Errorf("BaseURL = %q", cfg.BaseURL)
	}
	if cfg.LogLevel != slog.LevelInfo {
		t.Errorf("LogLevel = %v", cfg.LogLevel)
	}
	if cfg.DBMaxConns != 5 {
		t.Errorf("DBMaxConns = %d", cfg.DBMaxConns)
	}
	if cfg.ShutdownTimeout != 15*time.Second {
		t.Errorf("ShutdownTimeout = %v", cfg.ShutdownTimeout)
	}
	if cfg.OIDCIssuerURL != "https://accounts.google.com" || cfg.SessionTTL != 720*time.Hour || len(cfg.SessionSecret) != 32 {
		t.Errorf("auth defaults: issuer=%q ttl=%v secret=%d bytes", cfg.OIDCIssuerURL, cfg.SessionTTL, len(cfg.SessionSecret))
	}
	if cfg.OIDCRedirectURL() != "http://localhost:5173/auth/callback" || cfg.SecureCookies() {
		t.Errorf("redirect=%q secure=%v", cfg.OIDCRedirectURL(), cfg.SecureCookies())
	}
}

func TestLoadProduction(t *testing.T) {
	cfg, err := Load(valid(map[string]string{
		"APP_ENV":      "production",
		"APP_BASE_URL": "https://calendar.example.com/",
		"LOG_LEVEL":    "warn",
		"DB_MAX_CONNS": "3",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsProduction() || cfg.LogLevel != slog.LevelWarn || cfg.DBMaxConns != 3 {
		t.Errorf("unexpected config: %+v", cfg)
	}
	if cfg.BaseURL.String() != "https://calendar.example.com" {
		t.Errorf("trailing slash not normalized: %q", cfg.BaseURL)
	}
	if cfg.OIDCRedirectURL() != "https://calendar.example.com/auth/callback" || !cfg.SecureCookies() {
		t.Errorf("redirect=%q secure=%v", cfg.OIDCRedirectURL(), cfg.SecureCookies())
	}
}

func TestLoadReportsAllErrors(t *testing.T) {
	_, err := Load(env(map[string]string{
		"APP_ENV":          "staging",
		"APP_BASE_URL":     "ftp://x",
		"LOG_LEVEL":        "loud",
		"DB_MAX_CONNS":     "0",
		"SHUTDOWN_TIMEOUT": "soon",
	}))
	if err == nil {
		t.Fatal("want error")
	}
	for _, key := range []string{"APP_ENV", "APP_BASE_URL", "LOG_LEVEL", "DATABASE_URL", "DB_MAX_CONNS", "SHUTDOWN_TIMEOUT",
		"OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET", "SESSION_SECRET"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s:\n%v", key, err)
		}
	}
}

func TestLoadProductionRequiresHTTPS(t *testing.T) {
	_, err := Load(valid(map[string]string{
		"APP_ENV":      "production",
		"APP_BASE_URL": "http://calendar.example.com",
	}))
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("want https error, got %v", err)
	}
}

func TestLoadDoesNotLeakSecrets(t *testing.T) {
	_, err := Load(env(map[string]string{
		"DATABASE_URL":       "postgres://u:supersecret@db/db",
		"OIDC_CLIENT_SECRET": "oidcsecret",
		"SESSION_SECRET":     "tooshortsecret",
		"APP_ENV":            "bad",
	}))
	if err == nil {
		t.Fatal("want error")
	}
	for _, s := range []string{"supersecret", "oidcsecret", "tooshortsecret"} {
		if strings.Contains(err.Error(), s) {
			t.Errorf("error leaks %q: %v", s, err)
		}
	}
}

func TestLoadRejectsWeakSessionSecret(t *testing.T) {
	// base64 of 16 bytes: valid encoding, too short.
	_, err := Load(valid(map[string]string{"SESSION_SECRET": "AAAAAAAAAAAAAAAAAAAAAA=="}))
	if err == nil || !strings.Contains(err.Error(), "SESSION_SECRET") {
		t.Errorf("want SESSION_SECRET error, got %v", err)
	}
}

func TestLoadTrustedProxies(t *testing.T) {
	cfg, err := Load(valid(map[string]string{"TRUSTED_PROXIES": " 172.16.0.0/12, 10.0.0.5 ,::1"}))
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(cfg.TrustedProxies)
	if got != "[172.16.0.0/12 10.0.0.5/32 ::1/128]" {
		t.Errorf("TrustedProxies = %s", got)
	}

	cfg, _ = Load(valid(nil))
	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("default must trust no proxies, got %v", cfg.TrustedProxies)
	}

	if _, err := Load(valid(map[string]string{"TRUSTED_PROXIES": "caddy"})); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Errorf("want TRUSTED_PROXIES error, got %v", err)
	}
}

func TestLoadIssuerScheme(t *testing.T) {
	cases := []struct {
		env, issuer string
		ok          bool
	}{
		{"development", "https://accounts.google.com", true},
		{"development", "http://localhost:9090", true},
		{"development", "http://127.0.0.1:9090", true},
		{"development", "http://idp.example.com", false},
		{"production", "http://localhost:9090", false}, // dev provider can never be used in production
		{"production", "https://accounts.google.com", true},
		{"development", "ftp://localhost", false},
	}
	for _, c := range cases {
		over := map[string]string{"APP_ENV": c.env, "OIDC_ISSUER_URL": c.issuer}
		if c.env == "production" {
			over["APP_BASE_URL"] = "https://calendar.example.com"
		}
		_, err := Load(valid(over))
		if (err == nil) != c.ok {
			t.Errorf("%s %s: err = %v, want ok=%v", c.env, c.issuer, err, c.ok)
		}
	}
}
