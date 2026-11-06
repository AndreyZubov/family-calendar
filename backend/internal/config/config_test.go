package config

import (
	"log/slog"
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://u:p@localhost/db"}))
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
}

func TestLoadProduction(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"APP_ENV":      "production",
		"APP_BASE_URL": "https://calendar.example.com/",
		"LOG_LEVEL":    "warn",
		"DATABASE_URL": "postgres://u:p@db/db",
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
	for _, key := range []string{"APP_ENV", "APP_BASE_URL", "LOG_LEVEL", "DATABASE_URL", "DB_MAX_CONNS", "SHUTDOWN_TIMEOUT"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error does not mention %s:\n%v", key, err)
		}
	}
}

func TestLoadProductionRequiresHTTPS(t *testing.T) {
	_, err := Load(env(map[string]string{
		"APP_ENV":      "production",
		"APP_BASE_URL": "http://calendar.example.com",
		"DATABASE_URL": "postgres://u:p@db/db",
	}))
	if err == nil || !strings.Contains(err.Error(), "https") {
		t.Errorf("want https error, got %v", err)
	}
}

func TestLoadDoesNotLeakDatabaseURL(t *testing.T) {
	_, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://u:supersecret@db/db",
		"APP_ENV":      "bad",
	}))
	if err == nil || strings.Contains(err.Error(), "supersecret") {
		t.Errorf("error must not contain the password: %v", err)
	}
}
