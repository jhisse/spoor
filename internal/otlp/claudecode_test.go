package otlp

import (
	"os"
	"testing"

	"github.com/jhisse/spoor/internal/store"
)

// postCapture ingests a capture's wire bytes through the handler.
func postCapture(t *testing.T, name string) (store.Trace, map[string]store.Span) {
	t.Helper()
	body, err := os.ReadFile("../../testdata/" + name + ".pb") // #nosec G304 -- test-only, name comes from call sites in this file
	if err != nil {
		t.Fatal(err)
	}
	st := newHandlerTestStore(t)
	if w := postTraces(&Handler{Store: st}, body); w.Code != 200 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	traces, _, err := st.QueryTraces(t.Context(), store.TraceQuery{Limit: 10})
	if err != nil || len(traces) != 1 {
		t.Fatalf("QueryTraces: %v, %d traces, want 1", err, len(traces))
	}
	tr, spans, err := st.GetTraceByID(t.Context(), traces[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]store.Span{}
	for _, s := range spans {
		byID[s.ID] = s
	}
	return tr, byID
}

// Claude Code 2.1.287, `claude -p` reading one file: an interaction with
// three model calls and one tool call, exported in two batches.
func TestClaudeCodeRealToolReadCapture(t *testing.T) {
	tr, spans := postCapture(t, "claude-code-tool-read")
	if tr.Name != "claude_code.interaction" {
		t.Errorf("trace name = %q, want the root interaction span's", tr.Name)
	}
	if tr.SessionID == nil || *tr.SessionID != "d0f3dfdb-e920-4602-b7fa-dc9a65327872" {
		t.Errorf("session id = %v, want the session.id every span carries", tr.SessionID)
	}
	if len(spans) != 7 {
		t.Fatalf("got %d spans, want 7", len(spans))
	}

	const root, tool = "c4423c16bfb0b2b0", "8770da9d733d0a19"
	for id, want := range map[string]store.SpanKind{
		root:               store.SpanKindAgent,
		tool:               store.SpanKindTool,
		"5664d7131ec47111": store.SpanKindGeneric, // claude_code.tool.execution
		"5ad25b41c3d69785": store.SpanKindGeneration,
		"0134704ba200c9ed": store.SpanKindGeneration, // the agent_classifier side call
	} {
		if got := spans[id].Kind; got != want {
			t.Errorf("span %s (%s): kind = %q, want %q", id, spans[id].Name, got, want)
		}
	}
	// Model and tool calls are siblings under the interaction; only the
	// tool's own phases nest under it.
	for id, parent := range map[string]string{tool: root, "5ad25b41c3d69785": root, "5664d7131ec47111": tool, "780fafd9d9aa743d": tool} {
		if p := spans[id].ParentSpanID; p == nil || *p != parent {
			t.Errorf("span %s: parent = %v, want %s", id, p, parent)
		}
	}
	if in := spans[root].Input; in == nil || *in != "Read the file note.txt in the current directory and reply with the number it mentions" {
		t.Errorf("interaction input = %v, want the user prompt", in)
	}
}

// input_tokens 10, cache_read_tokens 26500, cache_creation_tokens 9245,
// output_tokens 150: disjoint on the wire, whole prompt in the column.
func TestClaudeCodeRealUsageAndCost(t *testing.T) {
	_, spans := postCapture(t, "claude-code-tool-read")
	gen := spans["5ad25b41c3d69785"]
	if gen.Model == nil || *gen.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("model = %v", gen.Model)
	}
	wantTokens(t, "input", gen.InputTokens, 10+26500+9245)
	wantTokens(t, "cache read", gen.CacheReadTokens, 26500)
	wantTokens(t, "cache write", gen.CacheWriteTokens, 9245)
	wantTokens(t, "output", gen.OutputTokens, 150)
	if m := gen.Mix(); m.Fresh != 10 {
		t.Errorf("fresh input = %d, want 10", m.Fresh)
	}
	// Claude Code puts no cost on spans: priced from the catalog's haiku-4-5
	// row (1/M input, 0.10/M cache read, 1.25/M cache write, 5/M output).
	approx(t, gen.CostUSD, 10*1e-6+26500*1e-7+9245*1.25e-6+150*5e-6)
	if CostReported(gen) {
		t.Error("cost must count as calculated, not reported")
	}
}

func TestClaudeCodeRealSimpleCapture(t *testing.T) {
	tr, spans := postCapture(t, "claude-code-simple")
	if tr.SessionID == nil || *tr.SessionID != "216cc261-eb08-4eda-b19b-0503873cd877" {
		t.Errorf("session id = %v", tr.SessionID)
	}
	// cache_creation_tokens is 0 here and input (50) is far below cache
	// read (4511): still disjoint.
	side := spans["812a115916a2d4e5"]
	wantTokens(t, "input", side.InputTokens, 50+4511)
	wantTokens(t, "cache write", side.CacheWriteTokens, 0)
}

// The case normalizeInputTokens cannot infer: fresh input larger than the
// cache. Claude Code's attribute name alone says it is disjoint.
func TestBareInputTokensAreAlwaysDisjoint(t *testing.T) {
	attrs := map[string]string{"gen_ai.system": "anthropic", "input_tokens": "5000", "cache_read_tokens": "4000", "output_tokens": "7"}
	s := translateAttrs(attrs)
	wantTokens(t, "input", s.InputTokens, 9000)

	// A namespaced count on the same span wins and is not touched.
	// (9100, not the 9000 the bare count plus cache would also give).
	attrs["gen_ai.usage.input_tokens"] = "9100"
	wantTokens(t, "namespaced input", translateAttrs(attrs).InputTokens, 9100)
}
