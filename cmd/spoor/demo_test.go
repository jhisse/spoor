package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"
)

func getBody(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d, body: %s", url, resp.StatusCode, body)
	}
	return string(body)
}

// readBanner returns what `spoor demo` prints up to its last line.
func readBanner(stdout io.Reader) string {
	var printed strings.Builder
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		printed.WriteString(scanner.Text() + "\n")
		if strings.Contains(scanner.Text(), "Ctrl+C") {
			break
		}
	}
	return printed.String()
}

func postProtobuf(t *testing.T, client *http.Client, url, body string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-protobuf")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	_ = resp.Body.Close()
	return resp.StatusCode
}

// `spoor demo` with no SPOOR_ variable at all, then everything a newcomer does
// next, using only what the command printed.
func TestDemoOneCommandShowsTracesAndASession(t *testing.T) {
	ingestAddr, httpAddr := freeAddr(t), freeAddr(t)
	// #nosec G204 -- the test-built binary and self-generated loopback addresses, not external input
	cmd := exec.Command(binPath, "demo", "--http="+httpAddr, "--ingest="+ingestAddr)
	cmd.Env = buildEnv(nil)
	cmd.Dir = t.TempDir() // no .env, no repo checkout: the binary alone
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting spoor demo: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
	})

	out := readBanner(stdout)
	for _, want := range []string{"Open      http://" + httpAddr + "/\n", "http://" + ingestAddr + "/v1/traces", "no key", "curl -s http://" + httpAddr + "/sample.pb | curl -X POST http://" + ingestAddr + `/v1/traces -H "Content-Type: application/x-protobuf" --data-binary @-`} {
		if !strings.Contains(out, want) {
			t.Fatalf("demo output is missing %q:\n%s", want, out)
		}
	}
	sessionsURL := regexp.MustCompile(`http://\S+/sessions`).FindString(out)
	dbPath := regexp.MustCompile(`Database\s+(\S+)`).FindStringSubmatch(out)[1]

	client := &http.Client{Timeout: 2 * time.Second}
	waitFor200(t, client, "http://"+httpAddr+"/healthz")
	waitFor200(t, client, "http://"+ingestAddr+"/healthz")

	list := getBody(t, client, "http://"+httpAddr+"/")
	if n := strings.Count(list, `<a class="rowlink name" href="/traces/`); n != 21 {
		t.Errorf("trace list shows %d traces, want the 21 captures", n)
	}
	for _, want := range []string{".kickoff", "anthropic.chat", "claude_code.interaction", "now"} {
		if !strings.Contains(list, want) {
			t.Errorf("trace list should contain %q (a real capture, dated now)", want)
		}
	}
	if sessions := getBody(t, client, sessionsURL); !strings.Contains(sessions, "demo-openllmetry-anthropic") {
		t.Errorf("sessions page should list the multi-turn demo session, got: %s", sessions)
	}

	// The printed curl, step by step: fetch the sample, post it as it is.
	sample := getBody(t, client, "http://"+httpAddr+"/sample.pb")
	for _, path := range []string{"/v1/traces", "/v1/logs", "/v1/metrics"} {
		if status := postProtobuf(t, client, "http://"+ingestAddr+path, sample); status != http.StatusOK {
			t.Errorf("POST %s: status %d, want 200", path, status)
		}
	}
	if n := strings.Count(getBody(t, client, "http://"+httpAddr+"/"), `<a class="rowlink name" href="/traces/`); n != 22 {
		t.Errorf("after sending the sample, the list shows %d traces, want 22", n)
	}

	_ = cmd.Process.Signal(syscall.SIGTERM)
	if err := cmd.Wait(); err != nil {
		t.Errorf("spoor demo should exit cleanly on SIGTERM: %v", err)
	}
	if matches, _ := filepath.Glob(filepath.Dir(dbPath) + "/*"); len(matches) != 0 {
		t.Errorf("the throwaway database directory should be removed on exit, still has: %v", matches)
	}
}

func TestAvailableAddrFallsBackWhenPreferredIsTaken(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	taken := l.Addr().String()

	got := availableAddr(taken)
	if got == taken || !strings.HasPrefix(got, "127.0.0.1:") {
		t.Errorf("availableAddr(%q) = %q, want a different loopback port", taken, got)
	}
	if free := freeAddr(t); availableAddr(free) != free {
		t.Errorf("availableAddr(%q) should keep a free preferred address", free)
	}
}

// `spoor serve` on a database file that does not exist yet comes up usable,
// with no `spoor migrate` first.
func TestServeMigratesAFreshDatabase(t *testing.T) {
	ingestAddr, httpAddr := freeAddr(t), freeAddr(t)
	dbPath := filepath.Join(t.TempDir(), "fresh.db") // never created by `spoor migrate`
	run := func() string {
		cmd := exec.Command(binPath, "serve")
		cmd.Env = buildEnv([]string{"SPOOR_INGEST_ADDR=" + ingestAddr, "SPOOR_HTTP_ADDR=" + httpAddr, "SPOOR_SQLITE_PATH=" + dbPath})
		var stderr strings.Builder
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting spoor serve: %v", err)
		}
		client := &http.Client{Timeout: 2 * time.Second}
		waitFor200(t, client, "http://"+httpAddr+"/healthz")
		// GET / queries traces: a 500 without the schema.
		if body := getBody(t, client, "http://"+httpAddr+"/"); !strings.Contains(body, "spoor demo") {
			t.Errorf("an empty instance should show the onboarding empty state, got: %s", body)
		}
		_ = cmd.Process.Signal(syscall.SIGTERM)
		_ = cmd.Wait()
		return stderr.String()
	}

	if log := run(); !strings.Contains(log, "schema migrations applied") || !strings.Contains(log, "from_version=0") {
		t.Errorf("first start should log the applied migrations, got: %s", log)
	}
	if log := run(); strings.Contains(log, "schema migrations applied") {
		t.Errorf("second start has nothing to apply and should not claim it did, got: %s", log)
	}
}
