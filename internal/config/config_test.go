package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestLoadDefaults: spoor starts with no configuration at all.
func TestLoadDefaults(t *testing.T) {
	cfg, err := load("testdata-nonexistent/.env")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	cases := []struct {
		name string
		got  any
		want any
	}{
		{"SQLitePath", cfg.SQLitePath, "./spoor.db"},
		{"IngestAddr", cfg.IngestAddr, "127.0.0.1:4318"},
		{"HTTPAddr", cfg.HTTPAddr, "127.0.0.1:8080"},
		{"RetentionDays", cfg.RetentionDays, 0},
		{"SweepInterval", cfg.SweepInterval, time.Hour},
		{"LogLevel", cfg.LogLevel, slog.LevelInfo},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestLoadInvalidDuration(t *testing.T) {
	t.Setenv("SPOOR_SWEEP_INTERVAL", "not-a-duration")

	_, err := load("testdata-nonexistent/.env")
	if err == nil {
		t.Fatal("expected error for invalid SPOOR_SWEEP_INTERVAL, got nil")
	}
}

func TestLoadDotEnvDoesNotOverwriteRealEnv(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("SPOOR_SQLITE_PATH=from-dotenv.db\nSPOOR_LOG_LEVEL=debug\nGODEBUG=http2debug=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// loadDotEnv sets the process env directly, which t.Setenv does not restore.
	t.Cleanup(func() { _ = os.Unsetenv("SPOOR_LOG_LEVEL") })

	t.Setenv("SPOOR_SQLITE_PATH", "from-real-env.db")
	// SPOOR_LOG_LEVEL is unset in the real env, so only the .env value applies.

	cfg, err := load(envPath)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SQLitePath != "from-real-env.db" {
		t.Errorf("SQLitePath = %q, want real env value to win over .env", cfg.SQLitePath)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v, want value from .env (debug)", cfg.LogLevel)
	}
	if os.Getenv("GODEBUG") != "" {
		t.Error("a non-SPOOR_ key in .env reached the process environment")
	}
}

func TestSQLitePathOverride(t *testing.T) {
	t.Setenv("SPOOR_SQLITE_PATH", "/data/spoor.db")
	path, err := sqlitePath("testdata-nonexistent/.env")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if path != "/data/spoor.db" {
		t.Errorf("path = %q, want /data/spoor.db", path)
	}
}

// A typo in the retention must stop the boot, not silently mean "keep
// forever" (or, read as 0 days, "delete everything").
func TestLoadRetentionDays(t *testing.T) {
	t.Setenv("SPOOR_RETENTION_DAYS", "30")
	cfg, err := load("testdata-nonexistent/.env")
	if err != nil || cfg.RetentionDays != 30 {
		t.Fatalf("SPOOR_RETENTION_DAYS=30: RetentionDays = %v, err %v", cfg, err)
	}
	for _, bad := range []string{"0", "-7", "thirty", "1.5", "30d", " "} {
		t.Setenv("SPOOR_RETENTION_DAYS", bad)
		if _, err := load("testdata-nonexistent/.env"); err == nil || !strings.Contains(err.Error(), "SPOOR_RETENTION_DAYS") {
			t.Errorf("SPOOR_RETENTION_DAYS=%q: err = %v, want an error naming the variable", bad, err)
		}
	}
}

func TestLoadAllowedHosts(t *testing.T) {
	t.Setenv("SPOOR_HTTP_ADDR", "192.168.1.5:8090")
	t.Setenv("SPOOR_ALLOWED_HOSTS", "spoor.internal, traces.example")

	cfg, err := load("testdata-nonexistent/.env")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"spoor.internal", "traces.example", "192.168.1.5"}
	if !slices.Equal(cfg.AllowedHosts, want) {
		t.Errorf("AllowedHosts = %q, want %q (the env list plus the listen host)", cfg.AllowedHosts, want)
	}
}

// A ticker panics on an interval that is not positive.
func TestLoadRejectsNonPositiveSweepInterval(t *testing.T) {
	for _, raw := range []string{"0", "0s", "-1h"} {
		t.Setenv("SPOOR_SWEEP_INTERVAL", raw)
		if _, err := load("testdata-nonexistent/.env"); err == nil || !strings.Contains(err.Error(), "SPOOR_SWEEP_INTERVAL") {
			t.Errorf("%q: err = %v, want one naming the variable", raw, err)
		}
	}
}
