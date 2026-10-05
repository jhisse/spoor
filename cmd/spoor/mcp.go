package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// POST /mcp answers the Model Context Protocol (Streamable HTTP), read-only,
// written by hand against revision 2026-07-28 of the specification
// (https://modelcontextprotocol.io/specification/2026-07-28). That revision
// is stateless: no handshake, every request names its version in _meta,
// server/discover says what the server speaks, and every result carries
// resultType. Implemented: server/discover, tools/list, tools/call.
//
// A client of an earlier revision opens with initialize and is answered as
// revision 2025-06-18 (initialize, ping), since it cannot move forward on
// its own. The new revision's extra fields are harmless to it.
const (
	mcpProtocolVersion = "2026-07-28"
	mcpLegacyVersion   = "2025-06-18"
	mcpVersionKey      = "io.modelcontextprotocol/protocolVersion"
)

var mcpServerInfo = map[string]any{"io.modelcontextprotocol/serverInfo": map[string]any{"name": "spoor", "version": "0"}}

// mcpGuide is what an agent needs to read the rows correctly;
// docs/schema.md is the long form.
const mcpGuide = `spoor is a store of LLM application traces. It records and counts; it never summarises or judges, so read the rows and draw your own conclusions.

- A trace is one run (a request, an agent task). Its spans are the steps. spans.kind: generation (one model call), tool (one tool execution), agent, chain, retriever, embedding, reranker, generic. parent_span_id builds the tree; a span whose parent is absent is a root.
- status is ok, error or unset. unset is what most SDKs send for a step that worked and does NOT mean failure. A trace's status is error when any of its spans is error, otherwise its root span's status. The failing steps are the spans with status "error" (error_span_count in a trace row says how many); the reason is in the failing span's status_message when the SDK sent one, else in its events, output or metadata.
- events is a JSON array of {name, time, attributes} in the order sent, null when the span has none. A recorded exception is the event named "exception" with attributes "exception.type", "exception.message" and "exception.stacktrace". links is a JSON array of {trace_id, span_id, attributes} pointing at spans of other traces.
- Tokens: input_tokens is the WHOLE prompt, cache included. cache_read_tokens and cache_write_tokens are parts of it (fresh input = input_tokens - cache_read_tokens - cache_write_tokens). reasoning_tokens is part of output_tokens. null means not reported, never zero.
- cost_usd null means the model has no known price: unknown, not free.
- metadata is a JSON object of every attribute the SDK sent that has no column, keyed by its flat dotted name ("tool.name"). SDK-specific error details (exit codes, failure kinds) live here.
- input and output are text, usually JSON. On a tool span, input is the tool's arguments. On a generation, input is an array of {role, content} messages and output an array of {role, content, finish_reason, tool_calls:[{id, name, arguments}]}. A later model call usually repeats the earlier messages of the same conversation.
- Timestamps are UTC RFC 3339 text and compare correctly as strings. A trace row has duration_ms.
- service is the sender's OpenTelemetry service.name: which application sent the trace. null when it set none.`

type mcpTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

var mcpTools = []mcpTool{
	{"list_traces", "List traces, newest first, one row each (duration_ms, span_count, error_span_count, token sums, cost_usd, models: the one that cost most first). Start here: status=error gives the runs in which a step failed.",
		json.RawMessage(`{"type":"object","properties":{"service":{"type":"string","description":"only traces of this service (service.name)"},"status":{"type":"string","enum":["ok","error","unset"]},"since":{"type":"string","description":"only traces started at or after this: a UTC RFC 3339 timestamp, a date (2026-10-02), or an age such as 24h or 90m"},"limit":{"type":"integer","description":"default 20"}}}`)},
	{"get_trace", "One trace: its row and every span in start order, with the raw input and output. To find the failing step look for spans with status error; parent_span_id gives the tree. A big trace (see span_count in list_traces) is a big answer: pass status=error to get only the failed spans. Each input, output, metadata and events longer than max_chars is cut, and the span then carries input_truncated_from_chars (or output_/metadata_/events_) with the full length; nothing is cut silently. Pass max_chars=0 for the complete values.",
		json.RawMessage(`{"type":"object","properties":{"trace_id":{"type":"string"},"status":{"type":"string","enum":["ok","error","unset"],"description":"only spans with this status"},"max_chars":{"type":"integer","description":"cut each input, output, metadata and events at this many characters; default 1000, 0 = complete"}},"required":["trace_id"]}`)},
	{"search_spans", "Find spans whose name, input, output, metadata, status message or events hold every word of q, newest first, through a full-text index. A word matches a whole word or the start of one (error finds errors and error_code, not ValueError), in any order, ignoring case and accents. \"a quoted phrase\" matches those whole words in that order; -word excludes spans holding it (q needs at least one word that is not excluded). Nothing else is syntax. Returns trace_id, span_id, name, kind and status of each match, newest trace first, and for the first match of each trace a short snippet with each matched word between **; use get_trace for the full text.",
		json.RawMessage(`{"type":"object","properties":{"q":{"type":"string","description":"words, \"quoted phrases\" and -excluded words"},"service":{"type":"string","description":"only spans of this service's traces"},"limit":{"type":"integer","description":"default 20"}},"required":["q"]}`)},
}

// toolArgs is the union of every tool's arguments.
type toolArgs struct {
	Service  string `json:"service"`
	Status   string `json:"status"`
	Since    string `json:"since"`
	Limit    int    `json:"limit"`
	TraceID  string `json:"trace_id"`
	MaxChars *int   `json:"max_chars"`
	Q        string `json:"q"`
}

// mcpHandler takes one JSON-RPC message per request and answers one JSON
// object. A request of revision 2026-07-28 (it names its version in _meta)
// must mirror version, method and tool name in its headers; an earlier
// client's request carries none of that and is answered as it is.
func mcpHandler(st store.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		var req mcpRequest
		_ = json.Unmarshal(body, &req) // handleMCP answers a body that does not parse
		version, modern := req.Params.Meta[mcpVersionKey]
		var resp map[string]any
		// Checked before anything runs: a request the headers contradict is refused, not executed.
		if modern && (r.Header.Get("MCP-Protocol-Version") != version || r.Header.Get("Mcp-Method") != req.Method ||
			(req.Method == "tools/call" && mcpHeaderValue(r.Header.Get("Mcp-Name")) != req.Params.Name)) {
			resp = map[string]any{"jsonrpc": "2.0", "id": req.ID, "error": map[string]any{"code": -32020,
				"message": "Header mismatch: MCP-Protocol-Version, Mcp-Method and Mcp-Name must be present and equal the request body"}}
		} else if resp = handleMCP(r.Context(), st, body); resp == nil { // a notification
			w.WriteHeader(http.StatusAccepted)
			return
		}
		status := http.StatusOK
		if e, failed := resp["error"].(map[string]any); modern && failed {
			status = map[any]int{-32601: http.StatusNotFound}[e["code"]]
			if status == 0 {
				status = http.StatusBadRequest
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(resp)
	}
}

// mcpHeaderValue undoes the encoding a client uses for a header value that
// is not plain ASCII: =?base64?...?=
func mcpHeaderValue(v string) string {
	if enc, ok := strings.CutPrefix(v, "=?base64?"); ok && strings.HasSuffix(enc, "?=") {
		if raw, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(enc, "?=")); err == nil {
			return string(raw)
		}
	}
	return v
}

type mcpRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      map[string]any  `json:"_meta"`
	} `json:"params"`
}

// handleMCP answers one message; nil means no answer is due (a notification,
// which carries no id).
func handleMCP(ctx context.Context, st store.Store, line []byte) map[string]any {
	var req mcpRequest
	if len(bytes.TrimSpace(line)) == 0 {
		return nil
	}
	if err := json.Unmarshal(line, &req); err != nil {
		return map[string]any{"jsonrpc": "2.0", "id": nil, "error": map[string]any{"code": -32700, "message": "parse error: " + err.Error()}}
	}
	if req.ID == nil {
		return nil
	}
	resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
	// A request that names a version must name one this server speaks.
	if v, named := req.Params.Meta[mcpVersionKey]; named && v != mcpProtocolVersion {
		resp["error"] = map[string]any{"code": -32022, "message": "Unsupported protocol version",
			"data": map[string]any{"supported": []string{mcpProtocolVersion}, "requested": v}}
		return resp
	}
	var result map[string]any
	switch req.Method {
	case "server/discover":
		result = map[string]any{
			"supportedVersions": []string{mcpProtocolVersion},
			"capabilities":      map[string]any{"tools": map[string]any{}},
			"instructions":      mcpGuide,
			"ttlMs":             3600000, "cacheScope": "public",
		}
	case "initialize": // an earlier revision's client
		result = map[string]any{
			"protocolVersion": mcpLegacyVersion,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      mcpServerInfo["io.modelcontextprotocol/serverInfo"],
			"instructions":    mcpGuide,
		}
	case "ping": // removed in 2026-07-28; earlier clients still send it
		result = map[string]any{}
	case "tools/list": // the list never changes while the process runs
		result = map[string]any{"tools": mcpTools, "ttlMs": 3600000, "cacheScope": "public"}
	case "tools/call":
		result = mcpCall(ctx, st, req.Params.Name, req.Params.Arguments)
	default:
		resp["error"] = map[string]any{"code": -32601, "message": "method not found: " + req.Method}
		return resp
	}
	result["resultType"], result["_meta"] = "complete", mcpServerInfo
	resp["result"] = result
	return resp
}

// mcpCall runs one tool, for 30 seconds at most and no longer than the
// client waits. A failed call is a result the model can read and correct,
// not a protocol error.
func mcpCall(ctx context.Context, st store.Store, name string, arguments json.RawMessage) map[string]any {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var args toolArgs
	err := json.Unmarshal(arguments, &args)
	var result any
	if err == nil || len(arguments) == 0 {
		result, err = callTool(ctx, st, name, args)
	}
	text := bytes.NewBufferString("")
	if err != nil {
		text.WriteString(err.Error())
	} else {
		enc := json.NewEncoder(text)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(result) // plain structs and RawMessages that json.Valid accepted: always encodable
	}
	return map[string]any{"content": []map[string]any{{"type": "text", "text": text.String()}}, "isError": err != nil}
}
