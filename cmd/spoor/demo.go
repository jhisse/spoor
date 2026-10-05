package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/jhisse/spoor/internal/config"
	"github.com/jhisse/spoor/internal/otlp"
	"github.com/jhisse/spoor/internal/store/sqlite"
)

func runDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	db := fs.String("db", "", "database file to keep (default: a temporary one, removed on exit)")
	httpAddr := fs.String("http", "127.0.0.1:8080", "UI address; a free port is used if this one is taken")
	ingestAddr := fs.String("ingest", "127.0.0.1:4318", "OTLP address; a free port is used if this one is taken")
	_ = fs.Parse(args) // ExitOnError: never returns an error

	kept := "kept after exit"
	if *db == "" {
		dir, err := os.MkdirTemp("", "spoor-demo-")
		if err != nil {
			return err
		}
		defer func() { _ = os.RemoveAll(dir) }()
		*db, kept = filepath.Join(dir, "spoor.db"), "removed on exit; pass --db=<path> to keep it"
	}

	cfg := &config.Config{SQLitePath: *db, SweepInterval: time.Hour, LogLevel: slog.LevelWarn,
		HTTPAddr: availableAddr(*httpAddr), IngestAddr: availableAddr(*ingestAddr)}

	if err := sqlite.Migrate(cfg.SQLitePath); err != nil {
		return err
	}
	st, err := sqlite.Open(cfg.SQLitePath)
	if err != nil {
		return err
	}
	n, err := (&otlp.Handler{Store: st}).LoadDemo(context.Background(), time.Now())
	if err != nil {
		return err
	}
	_ = st.Close() // serve opens its own

	fmt.Printf(`spoor demo: %[1]d traces from real sessions loaded.

  Open      http://%[2]s/
  Sessions  http://%[2]s/sessions
  Database  %[3]s (%[4]s)

Send your own trace (OTLP/HTTP, no key):

  Endpoint  http://%[5]s/v1/traces

  curl -s http://%[2]s/sample.pb | curl -X POST http://%[5]s/v1/traces -H "Content-Type: application/x-protobuf" --data-binary @-

  OTEL_EXPORTER_OTLP_ENDPOINT=http://%[5]s

For real use: spoor serve. Ctrl+C to stop.
`, n, cfg.HTTPAddr, *db, kept, cfg.IngestAddr)

	return serve(cfg)
}

// availableAddr returns preferred, or a free loopback port if something
// already answers there (dialed, because on macOS a loopback bind succeeds
// even while another process holds the wildcard address).
// The port can still be taken between this check and the bind.
func availableAddr(preferred string) string {
	if c, err := net.Dial("tcp", preferred); err == nil {
		_ = c.Close()
		preferred = "127.0.0.1:0"
	}
	l, err := net.Listen("tcp", preferred)
	if err != nil {
		return preferred // serve reports the bind error
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String()
}
