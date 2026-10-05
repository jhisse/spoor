package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/jhisse/spoor/internal/config"
	"github.com/jhisse/spoor/internal/otlp"
	"github.com/jhisse/spoor/internal/store"
	"github.com/jhisse/spoor/internal/store/sqlite"
	"github.com/jhisse/spoor/internal/web"
)

const (
	shutdownTimeout   = 10 * time.Second
	readHeaderTimeout = 10 * time.Second
	ioTimeout         = time.Minute // a 64 MiB ingest body, or the largest page
)

func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), `usage: spoor serve

Starts the OTLP ingestion server (SPOOR_INGEST_ADDR) and the web UI /
management server (SPOOR_HTTP_ADDR) in the same process. Configuration is
read from SPOOR_-prefixed environment variables (and an optional .env file).
`)
	}
	_ = fs.Parse(args) // ExitOnError: never returns an error

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	return serve(cfg)
}

// serve runs both servers until SIGINT/SIGTERM; `demo` calls it with a
// config built in memory.
func serve(cfg *config.Config) error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))
	slog.SetDefault(logger) // internal/web and internal/otlp log their 500s and 503s through it

	// Logged before, not after: migrating a large database takes minutes with no port open.
	logger.Info("applying pending schema migrations, if any", "path", cfg.SQLitePath)

	from, to, err := sqlite.MigrateVersions(cfg.SQLitePath)
	if err != nil {
		return fmt.Errorf("migrating storage: %w", err)
	}
	if from != to {
		logger.Info("schema migrations applied", "from_version", from, "to_version", to)
	}

	st, err := sqlite.Open(cfg.SQLitePath)
	if err != nil {
		return fmt.Errorf("opening storage: %w", err)
	}
	defer func() { _ = st.Close() }()

	// Said once, never fixed here: a stored cost was right for its day.
	// Repricing is the operator's call.
	prices, err := st.ListModelPrices(context.Background())
	if err != nil {
		return fmt.Errorf("reading model prices: %w", err)
	}
	pricings, err := st.SpanPricings(context.Background())
	if err != nil {
		return fmt.Errorf("reading span pricings: %w", err)
	}
	if stale := store.StalePricings(prices, pricings); stale > 0 {
		logger.Warn("spans were priced with a price table that has since changed; `spoor reprice --dry-run` shows the difference", "spans", stale)
	}

	// The MCP endpoint reads through a connection SQLite itself keeps read-only.
	ro, err := sqlite.OpenReadOnly(cfg.SQLitePath)
	if err != nil {
		return fmt.Errorf("opening storage read-only: %w", err)
	}
	defer func() { _ = ro.Close() }()

	ingest := &otlp.Handler{Store: st}
	webHandlers := &web.Handlers{Store: st, Ingest: ingest, IngestAddr: cfg.IngestAddr, RetentionDays: cfg.RetentionDays, Version: versionString()}

	ingestServer := &http.Server{Addr: cfg.IngestAddr, Handler: ingestMux(st, ingest), ReadHeaderTimeout: readHeaderTimeout, ReadTimeout: ioTimeout, WriteTimeout: ioTimeout, IdleTimeout: 2 * ioTimeout}
	httpServer := &http.Server{Addr: cfg.HTTPAddr, Handler: web.SameOrigin(cfg.AllowedHosts, httpMux(st, ro, webHandlers)), ReadHeaderTimeout: readHeaderTimeout, ReadTimeout: ioTimeout, WriteTimeout: ioTimeout, IdleTimeout: 2 * ioTimeout}

	if host, _, _ := net.SplitHostPort(cfg.HTTPAddr); !web.LoopbackHost(host) {
		logger.Warn("no login: anyone who can reach this port can read prompts", "addr", cfg.HTTPAddr)
	}
	if host, _, _ := net.SplitHostPort(cfg.IngestAddr); !web.LoopbackHost(host) {
		logger.Warn("no auth: anyone who can reach this port can write traces", "addr", cfg.IngestAddr)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var wg sync.WaitGroup
	errs := make(chan error, 2)

	start := func(name string, srv *http.Server) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			logger.Info("http server starting", "server", name, "addr", srv.Addr)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errs <- fmt.Errorf("%s server: %w", name, err)
			}
		}()
	}
	start("ingest", ingestServer)
	start("http", httpServer)

	if cfg.RetentionDays > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runSweepLoop(ctx, st, cfg.RetentionDays, cfg.SweepInterval, logger)
		}()
	}

	var startErr error // returned, so the process exits non-zero and a supervisor sees the failure
	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case startErr = <-errs:
		logger.Error("server failed to start", "error", startErr)
		stop()
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	shutdownErr := errors.Join(startErr, ingestServer.Shutdown(shutdownCtx), httpServer.Shutdown(shutdownCtx))

	wg.Wait()
	logger.Info("shutdown complete")
	return shutdownErr
}

// runSweepLoop deletes the traces older than retentionDays, every interval.
// A sweep error is logged and the loop keeps ticking: one bad sweep must not
// take the server down.
func runSweepLoop(ctx context.Context, st store.Store, retentionDays int, interval time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			traces, spans, err := st.SweepExpired(ctx, time.Now().AddDate(0, 0, -retentionDays))
			if err != nil {
				logger.Error("sweep failed", "error", err)
				continue
			}
			logger.Info("sweep completed", "traces_deleted", traces, "spans_deleted", spans)
		}
	}
}

func healthzHandler(st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := st.Ping(r.Context()); err != nil {
			http.Error(w, "storage unreachable", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}

// The two servers share only /healthz: /v1/* belongs on the ingest port,
// the UI and /mcp on the http port.
func ingestMux(st store.Store, ingest *otlp.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler(st))
	mux.Handle("POST /v1/traces", ingest)
	mux.HandleFunc("POST /v1/logs", ingest.Discard)
	mux.HandleFunc("POST /v1/metrics", ingest.Discard)
	return mux
}

func httpMux(st, ro store.Store, webHandlers *web.Handlers) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", healthzHandler(st))
	mux.HandleFunc("GET /{$}", webHandlers.List)
	mux.HandleFunc("GET /traces/{trace_id}", webHandlers.Detail)
	mux.HandleFunc("GET /sessions", webHandlers.Sessions)
	mux.HandleFunc("GET /", webHandlers.NotFound)
	mux.HandleFunc("GET /blind-spots", webHandlers.BlindSpots)
	mux.HandleFunc("GET /help", webHandlers.Help)
	mux.HandleFunc("POST /theme", web.Theme)
	mux.HandleFunc("POST /mcp", mcpHandler(ro))
	mux.Handle("GET /static/", web.StaticHandler())
	mux.HandleFunc("GET /sample.pb", otlp.Sample)
	return mux
}
