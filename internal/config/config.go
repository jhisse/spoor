// Package config loads spoor's configuration from SPOOR_-prefixed
// environment variables, optionally seeded by a .env file.
package config

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	SQLitePath    string
	IngestAddr    string
	HTTPAddr      string
	AllowedHosts  []string // Host values the UI answers under, besides loopback
	RetentionDays int      // 0 = keep forever
	SweepInterval time.Duration
	LogLevel      slog.Level
}

func Load() (*Config, error) {
	return load(".env")
}

// SQLitePath returns SPOOR_SQLITE_PATH (or its default), loading .env
// first: all that the commands other than serve need.
func SQLitePath() (string, error) {
	return sqlitePath(".env")
}

func sqlitePath(dotenvPath string) (string, error) {
	if err := loadDotEnv(dotenvPath); err != nil {
		return "", fmt.Errorf("reading %s: %w", dotenvPath, err)
	}
	return getenvDefault("SPOOR_SQLITE_PATH", "./spoor.db"), nil
}

func load(dotenvPath string) (*Config, error) {
	path, err := sqlitePath(dotenvPath)
	if err != nil {
		return nil, err
	}
	cfg := &Config{
		SQLitePath: path,
		IngestAddr: getenvDefault("SPOOR_INGEST_ADDR", "127.0.0.1:4318"),
		HTTPAddr:   getenvDefault("SPOOR_HTTP_ADDR", "127.0.0.1:8080"),
	}
	listenHost, _, _ := net.SplitHostPort(cfg.HTTPAddr)
	cfg.AllowedHosts = append(strings.FieldsFunc(os.Getenv("SPOOR_ALLOWED_HOSTS"), func(r rune) bool { return r == ',' || r == ' ' }), listenHost)

	if raw := os.Getenv("SPOOR_RETENTION_DAYS"); raw != "" {
		if cfg.RetentionDays, err = strconv.Atoi(raw); err != nil || cfg.RetentionDays < 1 {
			return nil, fmt.Errorf("SPOOR_RETENTION_DAYS: %q is not a whole number of days, 1 or more (unset it to keep traces forever)", raw)
		}
	}
	if cfg.SweepInterval, err = parseDuration("SPOOR_SWEEP_INTERVAL", "1h"); err != nil {
		return nil, err
	}
	raw := getenvDefault("SPOOR_LOG_LEVEL", "info")
	if err = cfg.LogLevel.UnmarshalText([]byte(raw)); err != nil {
		return nil, fmt.Errorf("SPOOR_LOG_LEVEL: invalid level %q: %w", raw, err)
	}
	return cfg, nil
}

func getenvDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func parseDuration(key, def string) (time.Duration, error) {
	raw := getenvDefault(key, def)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s: invalid duration %q: %w", key, raw, err)
	}
	if d <= 0 { // a ticker refuses it with a panic
		return 0, fmt.Errorf("%s: %q is not a positive duration", key, raw)
	}
	return d, nil
}
