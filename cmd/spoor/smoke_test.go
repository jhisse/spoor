package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
	"github.com/jhisse/spoor/internal/store/sqlite"
)

// binPath is a spoor binary built once for the whole suite; these tests
// drive it as a subprocess.
var binPath string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "spoor-smoke-*")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binPath = filepath.Join(dir, "spoor")
	// #nosec G204 -- fixed "go build" argv plus a self-generated temp path, not external input
	build := exec.Command("go", "build", "-o", binPath, ".")
	out, err := build.CombinedOutput()
	code := 1
	if err != nil {
		fmt.Fprintf(os.Stderr, "building spoor: %v\n%s", err, out)
	} else {
		code = m.Run()
	}
	_ = os.RemoveAll(dir) // not deferred: os.Exit runs no defers
	os.Exit(code)
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// buildEnv drops the ambient SPOOR_ vars (so shell state can't leak into the
// subprocess) and adds the overrides.
func buildEnv(overrides []string) []string {
	env := os.Environ()
	filtered := env[:0]
	for _, e := range env {
		if !strings.HasPrefix(e, "SPOOR_") {
			filtered = append(filtered, e)
		}
	}
	return append(filtered, overrides...)
}

// serveEnv gives a test server its own ports and database: the defaults are
// the operator's real ports and ./spoor.db.
func serveEnv(t *testing.T, ingestAddr, httpAddr string) []string {
	t.Helper()
	return []string{
		"SPOOR_INGEST_ADDR=" + ingestAddr,
		"SPOOR_HTTP_ADDR=" + httpAddr,
		"SPOOR_SQLITE_PATH=" + filepath.Join(t.TempDir(), "spoor.db"),
	}
}

func waitFor200(t *testing.T, client *http.Client, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			lastErr = fmt.Errorf("status %d", resp.StatusCode)
		} else {
			lastErr = err
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("GET %s did not return 200 in time: %v", url, lastErr)
}

func TestServeHealthzBothPorts(t *testing.T) {
	ingestAddr := freeAddr(t)
	httpAddr := freeAddr(t)

	cmd := exec.Command(binPath, "serve")
	cmd.Env = buildEnv(serveEnv(t, ingestAddr, httpAddr))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting spoor serve: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	})

	client := &http.Client{Timeout: time.Second}
	waitFor200(t, client, "http://"+ingestAddr+"/healthz")
	waitFor200(t, client, "http://"+httpAddr+"/healthz")
}

// A retention that is not a number of days must stop the boot, naming the
// variable: started anyway, the server would keep or delete the wrong data.
func TestServeInvalidRetentionFailsWithMessage(t *testing.T) {
	cmd := exec.Command(binPath, "serve")
	cmd.Env = buildEnv(append(serveEnv(t, freeAddr(t), freeAddr(t)), "SPOOR_RETENTION_DAYS=0"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected non-zero exit for SPOOR_RETENTION_DAYS=0, got success. output: %s", out)
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}
	if exitErr.ExitCode() != 1 {
		t.Errorf("exit code = %d, want 1", exitErr.ExitCode())
	}
	if !strings.Contains(string(out), "SPOOR_RETENTION_DAYS") {
		t.Errorf("expected output to name SPOOR_RETENTION_DAYS, got: %s", out)
	}
}

func TestServeUIRouteWiring(t *testing.T) {
	ingestAddr := freeAddr(t)
	httpAddr := freeAddr(t)

	// GET / queries traces, so this test needs a migrated database.
	dbPath := filepath.Join(t.TempDir(), "spoor.db")
	if err := sqlite.Migrate(dbPath); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	cmd := exec.Command(binPath, "serve")
	cmd.Env = buildEnv([]string{
		"SPOOR_INGEST_ADDR=" + ingestAddr,
		"SPOOR_HTTP_ADDR=" + httpAddr,
		"SPOOR_SQLITE_PATH=" + dbPath,
	})
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting spoor serve: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	})

	client := &http.Client{Timeout: time.Second}
	waitFor200(t, client, "http://"+httpAddr+"/healthz")

	resp, err := client.Get("http://" + httpAddr + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET / status = %d, want 200 (spoor has no login, the UI is open)", resp.StatusCode)
	}

	resp, err = client.Get("http://" + httpAddr + "/static/htmx.min.js")
	if err != nil {
		t.Fatalf("GET /static/htmx.min.js: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(body) == 0 {
		t.Errorf("GET /static/htmx.min.js status = %d, body len = %d, want 200 and non-empty", resp.StatusCode, len(body))
	}
}

func TestServeSIGTERMCleanShutdown(t *testing.T) {
	ingestAddr := freeAddr(t)
	httpAddr := freeAddr(t)

	cmd := exec.Command(binPath, "serve")
	cmd.Env = buildEnv(serveEnv(t, ingestAddr, httpAddr))
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting spoor serve: %v", err)
	}

	client := &http.Client{Timeout: time.Second}
	waitFor200(t, client, "http://"+ingestAddr+"/healthz")

	if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("sending SIGTERM: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("process exited with error after SIGTERM: %v\nstderr:\n%s", err, stderr.String())
		}
		if log := stderr.String(); !strings.Contains(log, "addr="+httpAddr) || strings.Contains(log, "no login") || strings.Contains(log, "no auth") {
			t.Errorf("loopback listeners must log their address and no exposure warning, got:\n%s", log)
		}
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("process did not exit within 5s of SIGTERM (possible orphan)")
	}
}

// A span stored with a price fingerprint that is not today's: serve says so
// in one log line and leaves the stored cost alone.
func TestServeLogsSpansPricedWithAnOlderTable(t *testing.T) {
	env := serveEnv(t, freeAddr(t), freeAddr(t))
	dbPath := strings.TrimPrefix(env[2], "SPOOR_SQLITE_PATH=")
	if err := sqlite.Migrate(dbPath); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	st, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	model, row, fingerprint, cost := "claude-opus-5-5[1m]", "claude-opus-5", "0badf00d", 0.9108
	if err := st.InsertSpan(ctx, store.Span{TraceID: "t", ID: "s", Kind: store.SpanKindGeneration, StartedAt: now, EndedAt: now,
		Status: store.StatusOK, CreatedAt: now, Model: &model, CostUSD: &cost, PricePattern: &row, PriceFingerprint: &fingerprint}); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}
	_ = st.Close()

	cmd := exec.Command(binPath, "serve")
	cmd.Env = buildEnv(env)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting spoor serve: %v", err)
	}
	waitFor200(t, &http.Client{Timeout: time.Second}, "http://"+strings.TrimPrefix(env[1], "SPOOR_HTTP_ADDR=")+"/healthz")
	_ = cmd.Process.Signal(syscall.SIGTERM)
	_ = cmd.Wait()
	if log := stderr.String(); !strings.Contains(log, "spans were priced with a price table that has since changed") || !strings.Contains(log, "spans=1") {
		t.Errorf("serve did not report the stale span, got:\n%s", log)
	}
	st, err = sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("reopening: %v", err)
	}
	defer func() { _ = st.Close() }()
	spans, err := st.QuerySpans(ctx, store.SpanQuery{})
	if err != nil || len(spans) != 1 || *spans[0].CostUSD != cost {
		t.Errorf("stored cost changed at startup: %+v, err %v", spans, err)
	}
}

// The quick start with the real binary: `spoor serve` on a database file
// that does not exist yet, a captured payload posted, and the trace read
// back in the pages a browser would open.
func TestServeIngestAndRenderTraceEndToEnd(t *testing.T) {
	ingestAddr := freeAddr(t)
	httpAddr := freeAddr(t)
	cmd := exec.Command(binPath, "serve")
	cmd.Env = buildEnv(serveEnv(t, ingestAddr, httpAddr))
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting spoor serve: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	})

	client := &http.Client{Timeout: time.Second}
	waitFor200(t, client, "http://"+httpAddr+"/healthz")

	payload, err := os.ReadFile("../../testdata/openllmetry-anthropic-simple.pb")
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+ingestAddr+"/v1/traces", strings.NewReader(string(payload)))
	if err != nil {
		t.Fatalf("building ingest request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/traces: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/traces status = %d, want 200", resp.StatusCode)
	}

	resp, err = client.Get("http://" + httpAddr + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if !strings.Contains(string(body), "anthropic.chat") {
		t.Errorf("GET / body missing the ingested trace's name, got: %s", body)
	}
	checkShell(t, httpAddr, string(body))

	const traceID = "6a3a6ad8c726ce1be8fc1a7faba8aab6" // testdata/openllmetry-anthropic-simple.json's own traceId
	resp, err = client.Get("http://" + httpAddr + "/traces/" + traceID)
	if err != nil {
		t.Fatalf("GET /traces/%s: %v", traceID, err)
	}
	body, _ = io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /traces/%s status = %d, want 200, body: %s", traceID, resp.StatusCode, body)
	}
	if !strings.Contains(string(body), "What is the capital of France") {
		t.Errorf("GET /traces/%s body missing the ingested prompt content, got: %s", traceID, body)
	}

	// The capture's service.name groups it; the other pages are routed.
	if list := getBody(t, client, "http://"+httpAddr+"/?service=spoor-testdata-capture"); !strings.Contains(list, "anthropic.chat") {
		t.Errorf("?service= of the capture's own service should list it, got: %s", list)
	}
	if list := getBody(t, client, "http://"+httpAddr+"/?service=another"); strings.Contains(list, "anthropic.chat") {
		t.Errorf("?service=another should not list the capture, got: %s", list)
	}
	for path, want := range map[string]string{"/sessions": "<h1>Sessions</h1>", "/blind-spots": "SPOOR_RETENTION_DAYS", "/help": "claude mcp add"} {
		if page := getBody(t, client, "http://"+httpAddr+path); !strings.Contains(page, want) {
			t.Errorf("GET %s should contain %q, got: %s", path, want, page)
		}
	}
	checkNotFound(t, client, httpAddr)
}

// checkNotFound: an unknown address is answered with the shell around it,
// not a bare line.
func checkNotFound(t *testing.T, client *http.Client, httpAddr string) {
	t.Helper()
	missing, err := client.Get("http://" + httpAddr + "/nowhere")
	if err != nil {
		t.Fatalf("GET /nowhere: %v", err)
	}
	defer func() { _ = missing.Body.Close() }()
	if page, _ := io.ReadAll(missing.Body); missing.StatusCode != http.StatusNotFound || !strings.Contains(string(page), "Page not found") || !strings.Contains(string(page), ">Blind spots</a>") {
		t.Errorf("GET /nowhere: status %d, want 404 with the page's header, got: %s", missing.StatusCode, page)
	}
}

// checkShell covers the header every page shares: the service menu, the
// tabs, and the theme buttons' route behind the same-origin guard.
func checkShell(t *testing.T, httpAddr, body string) {
	t.Helper()
	for _, want := range []string{`<details class="menu">`, `aria-current="page">Traces</a>`, `>Sessions</a>`, `>Blind spots</a>`, `>spoor-testdata-capture</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("GET / header missing %s", want)
		}
	}
	req, err := http.NewRequest(http.MethodPost, "http://"+httpAddr+"/theme", strings.NewReader("theme=dark"))
	if err != nil {
		t.Fatalf("building theme request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Origin", "http://"+httpAddr)
	resp, err := http.DefaultTransport.RoundTrip(req) // the client would follow the redirect
	if err != nil {
		t.Fatalf("POST /theme: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.HasPrefix(resp.Header.Get("Set-Cookie"), "spoor_theme=dark") {
		t.Errorf("POST /theme: status %d, Set-Cookie %q; want 303 and spoor_theme=dark", resp.StatusCode, resp.Header.Get("Set-Cookie"))
	}
}

// A server that cannot bind is a failed run: a supervisor reads the exit status.
func TestServeExitsNonZeroWhenAPortIsTaken(t *testing.T) {
	taken, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = taken.Close() }()
	cmd := exec.Command(binPath, "serve")
	cmd.Env = buildEnv(serveEnv(t, freeAddr(t), taken.Addr().String()))
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() == 0 {
		t.Fatalf("spoor serve on a taken port: err = %v, want a non-zero exit\n%s", err, out)
	}
	if !strings.Contains(string(out), "address already in use") {
		t.Errorf("output does not say why:\n%s", out)
	}
}
