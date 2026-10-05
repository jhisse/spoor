package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
	"github.com/jhisse/spoor/internal/store/sqlite"
	"github.com/jhisse/spoor/internal/web"
)

// seedAgentDB builds one session of two traces, the second with a failed
// tool call under a model call.
func seedAgentDB(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spoor.db")
	must(t, sqlite.Migrate(path))
	st, err := sqlite.Open(path)
	must(t, err)
	defer func() { _ = st.Close() }()
	ctx := context.Background()

	base := time.Now().UTC().Add(-2 * time.Hour)
	session, service, model := "sess-1", "acme", "claude-test"
	in, out := int64(1200), int64(40)
	cost := 0.5
	input := `[{"role":"user","content":"Deploy the <b>blue</b> service ` + strings.Repeat("x", 3000) + `"}]`
	output := `[{"role":"assistant","finish_reason":"tool_use","tool_calls":[{"id":"t1","name":"run_deploy","arguments":"{}"}]}]`
	toolInput := `{"command":"deploy blue"}`
	statuses := []store.Status{store.StatusUnset, store.StatusError}
	for i, id := range []string{"trace-ok", "trace-bad"} {
		start := base.Add(time.Duration(i) * time.Minute)
		must(t, st.MergeTrace(ctx, store.Trace{ID: id, Service: &service, SessionID: &session, Name: "agent.run", StartedAt: start, EndedAt: start.Add(2 * time.Second), Status: statuses[i], CreatedAt: start}, true))
		must(t, st.InsertSpan(ctx, store.Span{TraceID: id, ID: "gen", Kind: store.SpanKindGeneration, Name: "anthropic.chat", StartedAt: start, EndedAt: start.Add(time.Second), Status: store.StatusOK, Model: &model, Input: &input, Output: &output, InputTokens: &in, OutputTokens: &out, CostUSD: &cost, CreatedAt: start}))
	}
	parent, statusMessage := "gen", "deploy killed (exit 137)"
	start := base.Add(time.Minute + time.Second)
	must(t, st.InsertSpan(ctx, store.Span{TraceID: "trace-bad", ID: "tool", ParentSpanID: &parent, Kind: store.SpanKindTool, Name: "run_deploy", StartedAt: start, EndedAt: start.Add(time.Second), Status: store.StatusError, Input: &toolInput, Metadata: json.RawMessage(`{"tool.exit_code":"137"}`), CreatedAt: start,
		StatusMessage: &statusMessage, Attributes: json.RawMessage(`{"tool.exit_code":"137","gen_ai.operation.name":"execute_tool"}`), Events: json.RawMessage(`[{"name":"exception","time":"2026-10-03T12:00:00Z","attributes":{"exception.type":"OOMKilled","exception.message":"container ran out of memory"}}]`)}))
	return path
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// spoor runs the built binary against dbPath and returns stdout, stderr and
// whether it exited 0.
func spoor(t *testing.T, dbPath string, args ...string) (string, string, bool) {
	t.Helper()
	cmd := exec.Command(binPath, args...) // #nosec G204 -- the test's own binary and arguments
	cmd.Env = buildEnv([]string{"SPOOR_SQLITE_PATH=" + dbPath})
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return stdout.String(), stderr.String(), err == nil
}

// dig walks decoded JSON: a string key into an object, an int into an array.
// A wrong path fails the test with a readable message instead of a panic.
func dig(t *testing.T, v any, path ...any) any {
	t.Helper()
	for _, step := range path {
		switch step := step.(type) {
		case string:
			m, ok := v.(map[string]any)
			if !ok {
				t.Fatalf("dig %v: %q applied to %T", path, step, v)
			}
			v = m[step]
		case int:
			a, ok := v.([]any)
			if !ok || step >= len(a) {
				t.Fatalf("dig %v: index %d applied to %T %v", path, step, v, v)
			}
			v = a[step]
		}
	}
	return v
}

func TestExportTraceAsJSONL(t *testing.T) {
	db := seedAgentDB(t)
	stdout, stderr, ok := spoor(t, db, "export", "--trace", "trace-bad")
	if !ok {
		t.Fatalf("spoor export failed: %s", stderr)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one line per span (2), got %d", len(lines))
	}
	var gen, tool any
	must(t, json.Unmarshal([]byte(lines[0]), &gen))
	must(t, json.Unmarshal([]byte(lines[1]), &tool))
	for key, want := range map[string]any{"id": "tool", "status": "error", "parent_span_id": "gen", "trace_name": "agent.run", "trace_status": "error", "service": "acme", "session_id": "sess-1"} {
		if got := dig(t, tool, key); got != want {
			t.Errorf("tool line: %s = %v, want %v", key, got, want)
		}
	}
	if got := dig(t, tool, "attributes", "gen_ai.operation.name"); got != "execute_tool" {
		t.Errorf("the export carries the attributes as sent, got operation %v", got)
	}
	if content, _ := dig(t, gen, "input", 0, "content").(string); len(content) < 3000 || dig(t, gen, "input_tokens") != 1200.0 {
		t.Errorf("generation line is not complete: %d chars of input, input_tokens %v", len(content), dig(t, gen, "input_tokens"))
	}
}

func TestExportSessionAsJSONL(t *testing.T) {
	db := seedAgentDB(t)
	stdout, stderr, ok := spoor(t, db, "export", "--session", "sess-1")
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if !ok || len(lines) != 3 || !strings.Contains(lines[0], `"trace_id":"trace-ok"`) {
		t.Errorf("session export: ok=%v, %d lines (want 3, first turn first), stderr %s", ok, len(lines), stderr)
	}
}

func TestExportFailsWithoutOutputOnWhatIsNotThere(t *testing.T) {
	db := seedAgentDB(t)
	for _, args := range [][]string{
		{"export", "--trace", "no-such-trace"},
		{"export", "--session", "no-such-session"},
		{"export", "--session", "sess-1", "--trace", "trace-ok"},
		{"export"},
	} {
		if stdout, _, ok := spoor(t, db, args...); ok || stdout != "" {
			t.Errorf("%v: exited 0 or wrote output %q, want a failure", args, stdout)
		}
	}
}

func TestExportHTMLIsSelfContained(t *testing.T) {
	db := seedAgentDB(t)
	for _, args := range [][]string{
		{"export", "--html", "--trace", "trace-bad"},
		{"export", "--html", "--session", "sess-1"},
	} {
		page, stderr, ok := spoor(t, db, args...)
		if !ok {
			t.Fatalf("%v failed: %s", args, stderr)
		}
		for _, want := range []string{"<!doctype html>", "<style>", "agent.run", "anthropic.chat", "run_deploy", "Deploy the &lt;b&gt;blue&lt;/b&gt; service", "deploy blue", "exit_code", "137", "claude-test", "deploy killed (exit 137)", "OOMKilled", "container ran out of memory"} {
			if !strings.Contains(page, want) {
				t.Errorf("%v: page is missing %q", args, want)
			}
		}
		// A reference to the page's own sprite (<use href="#i-err">, fill="url(#hatch)") is not a request.
		page = strings.NewReplacer(` href="#`, "", `url(#`, "").Replace(page)
		for _, banned := range []string{"<script", "<link", "<img", " src=", " href=", "hx-", "url(", "@import"} {
			if strings.Contains(page, banned) {
				t.Errorf("%v: page contains %q; it must work as a lone file with no request", args, banned)
			}
		}
	}
}

// mcpSession drives the MCP endpoint the way a client does: one JSON-RPC
// message per POST. It goes through web.SameOrigin, as in serve.
type mcpSession struct {
	t      *testing.T
	h      http.Handler
	nextID int
}

// post sends one message with the headers a client of revision 2026-07-28
// mirrors from the body, unless header overrides one ("" drops it).
func (s *mcpSession) post(msg map[string]any, header map[string]string) *httptest.ResponseRecorder {
	s.t.Helper()
	body, err := json.Marshal(msg)
	must(s.t, err)
	r := httptest.NewRequest("POST", "/mcp", bytes.NewReader(body))
	r.Host = "localhost:8080"
	set := map[string]string{}
	if params, _ := msg["params"].(map[string]any); params != nil {
		if meta, _ := params["_meta"].(map[string]any); meta != nil {
			set["MCP-Protocol-Version"], _ = meta[mcpVersionKey].(string)
			set["Mcp-Method"], _ = msg["method"].(string)
			set["Mcp-Name"], _ = params["name"].(string)
		}
	}
	for k, v := range header {
		set[k] = v
	}
	for k, v := range set {
		if v != "" {
			r.Header.Set(k, v)
		}
	}
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, r)
	return w
}

// startMCP opens the endpoint over the seeded database and performs the
// handshake of an earlier revision's client.
func startMCP(t *testing.T) *mcpSession {
	t.Helper()
	ro, err := sqlite.OpenReadOnly(seedAgentDB(t))
	must(t, err)
	t.Cleanup(func() { _ = ro.Close() })
	mux := http.NewServeMux()
	mux.HandleFunc("POST /mcp", mcpHandler(ro))
	s := &mcpSession{t: t, h: web.SameOrigin(nil, mux)}

	init := s.call("initialize", map[string]any{"protocolVersion": "2025-06-18", "capabilities": map[string]any{}, "clientInfo": map[string]any{"name": "test", "version": "0"}})
	if dig(t, init, "result", "protocolVersion") != "2025-06-18" || dig(t, init, "result", "capabilities", "tools") == nil || dig(t, init, "result", "serverInfo", "name") != "spoor" {
		t.Fatalf("initialize answer = %v", init)
	}
	if w := s.post(map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"}, nil); w.Code != http.StatusAccepted || w.Body.Len() != 0 {
		t.Fatalf("notification: status %d, body %q; want 202 and no body", w.Code, w.Body.String())
	}
	return s
}

func (s *mcpSession) call(method string, params any) any {
	s.t.Helper()
	s.nextID++
	w := s.post(map[string]any{"jsonrpc": "2.0", "id": s.nextID, "method": method, "params": params}, nil)
	var resp any
	must(s.t, json.Unmarshal(w.Body.Bytes(), &resp))
	if dig(s.t, resp, "jsonrpc") != "2.0" || dig(s.t, resp, "id") != float64(s.nextID) {
		s.t.Fatalf("%s: answer %s does not match request id %d", method, w.Body.String(), s.nextID)
	}
	return resp
}

// tool calls a tool and returns its text payload (decoded when it is JSON;
// an error text is not), the text itself, and isError.
func (s *mcpSession) tool(name string, args map[string]any) (any, string, bool) {
	s.t.Helper()
	resp := s.call("tools/call", map[string]any{"name": name, "arguments": args})
	if dig(s.t, resp, "result", "content", 0, "type") != "text" {
		s.t.Fatalf("tools/call %s: no text content in %v", name, resp)
	}
	text := dig(s.t, resp, "result", "content", 0, "text").(string)
	var payload any
	_ = json.Unmarshal([]byte(text), &payload)
	return payload, text, dig(s.t, resp, "result", "isError") == true
}

func TestMCPLifecycleAndToolList(t *testing.T) {
	s := startMCP(t)

	var names []string
	for _, tool := range dig(t, s.call("tools/list", nil), "result", "tools").([]any) {
		names = append(names, dig(t, tool, "name").(string))
		if dig(t, tool, "description") == "" || dig(t, tool, "inputSchema", "type") != "object" {
			t.Errorf("tool %v lacks a description or an object inputSchema", dig(t, tool, "name"))
		}
	}
	if got := strings.Join(names, ","); got != "list_traces,get_trace,search_spans" {
		t.Errorf("tools = %s", got)
	}

	if resp := s.call("ping", nil); dig(t, resp, "result") == nil || dig(t, resp, "error") != nil {
		t.Errorf("ping = %v, want a result", resp)
	}
	if resp := s.call("resources/list", nil); dig(t, resp, "error", "code") != -32601.0 {
		t.Errorf("unknown method = %v, want error -32601", resp)
	}
}

// With nothing but the MCP endpoint, a client finds the failed trace and
// names the failing step.
func TestMCPFindsTheFailedTraceAndTheFailingStep(t *testing.T) {
	s := startMCP(t)

	// "since" as an age: both traces started within the last 3 hours, none in the last minute.
	payload, text, isErr := s.tool("list_traces", map[string]any{"status": "error", "since": "3h"})
	if isErr || len(dig(t, payload, "traces").([]any)) != 1 || dig(t, payload, "traces", 0, "error_span_count") != 1.0 {
		t.Fatalf("list_traces status=error: %s", text)
	}
	failedID := dig(t, payload, "traces", 0, "id")
	if failedID != "trace-bad" {
		t.Fatalf("failed trace = %v, want trace-bad", failedID)
	}
	if payload, text, _ := s.tool("list_traces", map[string]any{"since": "1m"}); len(dig(t, payload, "traces").([]any)) != 0 {
		t.Errorf("list_traces since=1m: %s, want no traces", text)
	}

	payload, text, isErr = s.tool("get_trace", map[string]any{"trace_id": failedID, "status": "error"})
	if isErr || len(dig(t, payload, "spans").([]any)) != 1 {
		t.Fatalf("get_trace status=error must return the failing step alone: %s", text)
	}
	if got := dig(t, payload, "spans", 0, "events", 0, "attributes", "exception.type"); got != "OOMKilled" {
		t.Errorf("failing step must carry its events as JSON, exception.type = %v", got)
	}
	for key, want := range map[string]any{"name": "run_deploy", "kind": "tool", "status": "error", "parent_span_id": "gen", "status_message": "deploy killed (exit 137)"} {
		if got := dig(t, payload, "spans", 0, key); got != want {
			t.Errorf("failing step: %s = %v, want %v", key, got, want)
		}
	}
	if dig(t, payload, "spans", 0, "metadata", "tool.exit_code") != "137" || dig(t, payload, "spans", 0, "input", "command") != "deploy blue" {
		t.Errorf("failing step must carry its metadata and input as JSON: %s", text)
	}
	if note, _ := dig(t, payload, "spans_not_shown").(string); !strings.HasPrefix(note, "1 spans") {
		t.Errorf("the answer must say a span was left out, got %q", note)
	}
}

func TestMCPGetTraceCutsLongValuesAndSaysSo(t *testing.T) {
	s := startMCP(t)

	payload, text, isErr := s.tool("get_trace", map[string]any{"trace_id": "trace-bad"})
	if isErr || len(dig(t, payload, "spans").([]any)) != 2 || dig(t, payload, "spans_not_shown") != nil || dig(t, payload, "trace", "span_count") != 2.0 {
		t.Fatalf("get_trace: %.300s", text)
	}
	if in, _ := dig(t, payload, "spans", 0, "input").(string); len(in) != 1000 || dig(t, payload, "spans", 0, "input_truncated_from_chars").(float64) < 3000 {
		t.Errorf("long input: %d chars; want 1000 and the full length reported", len(in))
	}
	if dig(t, payload, "spans", 0, "output_truncated_from_chars") != nil || dig(t, payload, "spans", 0, "output", 0, "finish_reason") != "tool_use" {
		t.Errorf("a short output must be complete, embedded JSON")
	}

	payload, _, _ = s.tool("get_trace", map[string]any{"trace_id": "trace-bad", "max_chars": 0})
	if content, _ := dig(t, payload, "spans", 0, "input", 0, "content").(string); len(content) < 3000 {
		t.Errorf("max_chars=0 must return the complete input as JSON, got %d chars", len(content))
	}

	if _, text, isErr := s.tool("get_trace", map[string]any{"trace_id": "nope"}); !isErr || !strings.Contains(text, "no trace with id") {
		t.Errorf("get_trace on an unknown id: isError=%v, %s", isErr, text)
	}
}

func TestMCPSearchAndBadArguments(t *testing.T) {
	s := startMCP(t)

	payload, text, _ := s.tool("search_spans", map[string]any{"q": "EXIT_CODE"})
	if len(dig(t, payload, "spans").([]any)) != 1 || dig(t, payload, "spans", 0, "span_id") != "tool" || dig(t, payload, "spans", 0, "trace_id") != "trace-bad" {
		t.Errorf("search_spans: %s", text)
	}
	if snippet, _ := dig(t, payload, "spans", 0, "snippet").(string); !strings.Contains(snippet, "**exit_code**") {
		t.Errorf("the first match of a trace carries a snippet with the word marked: %s", text)
	}
	if payload, text, _ := s.tool("search_spans", map[string]any{"q": "-EXIT_CODE"}); len(dig(t, payload, "spans").([]any)) != 0 {
		t.Errorf("a search of only excluded words names nothing to look for: %s", text)
	}

	// What a model gets wrong: an argument of the wrong type, a tool that does not exist.
	if _, text, isErr := s.tool("list_traces", map[string]any{"limit": "ten"}); !isErr {
		t.Errorf("wrongly typed argument: want isError, got %s", text)
	}
	if _, text, isErr := s.tool("drop_everything", nil); !isErr || !strings.Contains(text, "unknown tool") {
		t.Errorf("unknown tool: isError=%v, %s", isErr, text)
	}
}

// Revision 2026-07-28 has no handshake: a client names its version on every
// request, may ask server/discover first, and gets resultType on every result.
func TestMCPStatelessRevision(t *testing.T) {
	s := startMCP(t)
	meta := func(version string) map[string]any {
		return map[string]any{"_meta": map[string]any{"io.modelcontextprotocol/protocolVersion": version}}
	}

	d := s.call("server/discover", meta("2026-07-28"))
	if dig(t, d, "result", "supportedVersions", 0) != "2026-07-28" || dig(t, d, "result", "resultType") != "complete" ||
		dig(t, d, "result", "capabilities", "tools") == nil ||
		dig(t, d, "result", "_meta", "io.modelcontextprotocol/serverInfo", "name") != "spoor" {
		t.Errorf("server/discover = %v", d)
	}

	l := s.call("tools/list", meta("2026-07-28"))
	if dig(t, l, "result", "resultType") != "complete" || dig(t, l, "result", "cacheScope") != "public" ||
		dig(t, l, "result", "ttlMs") == nil || dig(t, l, "result", "tools", 0, "name") == nil {
		t.Errorf("tools/list = %v", l)
	}

	params := meta("2026-07-28")
	params["name"], params["arguments"] = "list_traces", map[string]any{}
	if c := s.call("tools/call", params); dig(t, c, "result", "resultType") != "complete" || dig(t, c, "result", "isError") != false {
		t.Errorf("tools/call = %v", c)
	}

	e := s.call("tools/list", meta("1900-01-01"))
	if dig(t, e, "error", "code") != float64(-32022) || dig(t, e, "error", "data", "supported", 0) != "2026-07-28" ||
		dig(t, e, "error", "data", "requested") != "1900-01-01" {
		t.Errorf("unsupported version answer = %v", e)
	}
}

// Over HTTP a request of revision 2026-07-28 mirrors version, method and
// tool name in its headers, and the status says what kind of failure it was.
func TestMCPOverHTTP(t *testing.T) {
	s := startMCP(t)
	meta := map[string]any{mcpVersionKey: "2026-07-28"}
	call := map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": "list_traces", "arguments": map[string]any{}, "_meta": meta}}
	for _, c := range []struct {
		name   string
		msg    map[string]any
		header map[string]string
		status int
		code   float64 // JSON-RPC error code, 0 for a result
	}{
		{"a modern call", call, nil, 200, 0},
		{"tool name sent base64", call, map[string]string{"Mcp-Name": "=?base64?bGlzdF90cmFjZXM=?="}, 200, 0},
		{"tool name header differs from the body", call, map[string]string{"Mcp-Name": "get_trace"}, 400, -32020},
		{"method header missing", call, map[string]string{"Mcp-Method": ""}, 400, -32020},
		{"version header differs from the body", call, map[string]string{"MCP-Protocol-Version": "2025-06-18"}, 400, -32020},
		{"unsupported version", map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/list",
			"params": map[string]any{"_meta": map[string]any{mcpVersionKey: "1900-01-01"}}}, nil, 400, -32022},
		{"unknown method", map[string]any{"jsonrpc": "2.0", "id": 3, "method": "resources/list",
			"params": map[string]any{"_meta": meta}}, nil, 404, -32601},
		{"a browser page of another site", call, map[string]string{"Origin": "https://evil.example"}, 403, 0},
	} {
		w := s.post(c.msg, c.header)
		if w.Code != c.status {
			t.Errorf("%s: status %d, want %d (%s)", c.name, w.Code, c.status, w.Body.String())
			continue
		}
		if c.status == 403 {
			continue
		}
		var resp map[string]any
		must(t, json.Unmarshal(w.Body.Bytes(), &resp))
		if e, _ := resp["error"].(map[string]any); (c.code == 0) != (e == nil) || (e != nil && e["code"] != c.code) {
			t.Errorf("%s: answer %s, want error code %v", c.name, w.Body.String(), c.code)
		}
	}
	if w := httptest.NewRecorder(); true {
		r := httptest.NewRequest("GET", "/mcp", nil)
		r.Host = "localhost:8080"
		if s.h.ServeHTTP(w, r); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("GET /mcp: status %d, want 405", w.Code)
		}
	}
}

// since is an age, an RFC 3339 time or a date, always read as UTC.
func TestParseSince(t *testing.T) {
	if got, err := parseSince("90m"); err != nil || time.Since(got) < 89*time.Minute || time.Since(got) > 91*time.Minute {
		t.Errorf("90m = %v, %v", got, err)
	}
	for in, want := range map[string]string{
		"2026-10-02":                "2026-10-02T00:00:00Z",
		"2026-10-02T04:05":          "2026-10-02T04:05:00Z",
		"2026-10-02T04:05:06Z":      "2026-10-02T04:05:06Z",
		"2026-10-02T01:05:06-03:00": "2026-10-02T04:05:06Z",
	} {
		if got, err := parseSince(in); err != nil || got.UTC().Format(time.RFC3339) != want {
			t.Errorf("%s = %v, %v; want %s", in, got, err, want)
		}
	}
	if _, err := parseSince("yesterday"); err == nil {
		t.Error("yesterday should be refused with a message that says what is accepted")
	}
}
