package otlp

import (
	"math"
	"os"
	"strings"
	"testing"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/jhisse/spoor/internal/store"
)

// An interactive Claude Code turn (sanitised): four Bash calls, each with
// its two phase spans and a permission-classifier model call under the
// waiting phase, plus a title call and the main thread.
func TestClaudeCodeInteractiveCapture(t *testing.T) {
	_, spans := postCapture(t, "claude-code-interactive-tools")
	if len(spans) != 20 {
		t.Fatalf("got %d spans, want 20", len(spans))
	}
	names, phases, purposes := map[string]int{}, map[Phase]int{}, map[string]int{}
	for _, s := range spans {
		names[s.Name]++
		phases[PhaseOf(s)]++
		purposes[Purpose(s)]++
	}
	if names["claude_code.tool Bash"] != 4 || names["claude_code.tool"] != 0 {
		t.Errorf("span names = %v, want the four tool spans named after their tool", names)
	}
	if phases[PhaseWaiting] != 4 || phases[PhaseRunning] != 4 || phases[NotAPhase] != 12 {
		t.Errorf("phases = %v, want 4 waiting, 4 running, 12 other", phases)
	}
	if purposes["auto_mode"] != 3 || purposes["generate_session_title"] != 1 || purposes["repl_main_thread"] != 3 || purposes[""] != 13 {
		t.Errorf("purposes = %v", purposes)
	}
}

func TestClaudeCodeInteractiveToolContentAndCost(t *testing.T) {
	_, spans := postCapture(t, "claude-code-interactive-tools")
	for _, s := range spans {
		in, out := ToolIO(s)
		if isTool := s.Kind == store.SpanKindTool; isTool != (out != nil) {
			t.Errorf("%s: tool output from the tool.output event = %v, want it exactly on tool spans", s.Name, out)
		} else if isTool && (*out != "note.txt\nREADME.md" || !strings.Contains(*in, `"bash_command":"ls -la"`)) {
			t.Errorf("%s: ToolIO = %q, %q", s.Name, *in, *out)
		}
	}

	// claude-opus-5-5[1m]: fresh 2, cache read 25656, cache write 48061,
	// output 587, at the claude-opus-5-5 row ($4, $0.20, $5, $20 per million):
	// the [1m] tag must not send it to another row.
	const want = (2*4 + 25656*0.2 + 48061*5 + 587*20) / 1e6
	var found bool
	for _, s := range spans {
		if s.Model != nil && *s.Model == "claude-opus-5-5[1m]" && *s.OutputTokens == 587 {
			found = true
			if s.CostUSD == nil || math.Abs(*s.CostUSD-want) > 1e-9 {
				t.Errorf("cost = %v, want %v", s.CostUSD, want)
			}
		}
	}
	if !found {
		t.Error("the first main-thread call is missing")
	}
}

// What a live turn looks like: Claude Code exports the interaction span when
// the turn ends, so every earlier batch is rootless.
func TestClaudeCodeRootArrivesLast(t *testing.T) {
	raw, err := os.ReadFile("../../testdata/claude-code-interactive-parallel.pb")
	if err != nil {
		t.Fatal(err)
	}
	var early, last collectortrace.ExportTraceServiceRequest
	for _, req := range []*collectortrace.ExportTraceServiceRequest{&early, &last} {
		if err := proto.Unmarshal(raw, req); err != nil {
			t.Fatal(err)
		}
	}
	keep := func(req *collectortrace.ExportTraceServiceRequest, root bool) {
		ss := req.ResourceSpans[0].ScopeSpans[0]
		var spans []*tracev1.Span
		for _, s := range ss.Spans {
			if (len(s.ParentSpanId) == 0) == root {
				spans = append(spans, s)
			}
		}
		ss.Spans = spans
	}
	keep(&early, false)
	keep(&last, true)

	st := newHandlerTestStore(t)
	h := &Handler{Store: st}
	for _, step := range []struct {
		req      *collectortrace.ExportTraceServiceRequest
		name     string
		rootless bool
	}{
		{&early, "claude_code.llm_request", true},
		{&last, "claude_code.interaction", false},
	} {
		body, _ := proto.Marshal(step.req)
		if w := postTraces(h, body); w.Code != 200 {
			t.Fatalf("status = %d", w.Code)
		}
		traces, _, err := st.QueryTraces(t.Context(), store.TraceQuery{Limit: 10})
		if err != nil || len(traces) != 1 {
			t.Fatalf("QueryTraces: %v, %d traces", err, len(traces))
		}
		if traces[0].Name != step.name || traces[0].Rootless != step.rootless {
			t.Errorf("name %q rootless %v, want %q %v", traces[0].Name, traces[0].Rootless, step.name, step.rootless)
		}
	}
}

// The rule is not Claude Code's: any tool span whose name leaves the tool
// out gets it, from whichever convention names it.
func TestToolSpanName(t *testing.T) {
	for _, c := range []struct {
		name  string
		kind  store.SpanKind
		attrs map[string]string
		want  string
	}{
		{"claude_code.tool", store.SpanKindTool, map[string]string{"tool_name": "Read"}, "claude_code.tool Read"},
		{"ToolNode", store.SpanKindTool, map[string]string{"tool.name": "search_web"}, "ToolNode search_web"},
		{"execute_tool get_weather", store.SpanKindTool, map[string]string{"gen_ai.tool.name": "get_weather"}, "execute_tool get_weather"},
		{"tool.Bash", store.SpanKindTool, map[string]string{"tool.name": "Bash"}, "tool.Bash"},
		{"chat", store.SpanKindGeneration, map[string]string{"gen_ai.tool.name": "get_weather"}, "chat"},
		{"claude_code.tool", store.SpanKindTool, nil, "claude_code.tool"},
	} {
		if got := toolSpanName(c.name, c.kind, c.attrs); got != c.want {
			t.Errorf("toolSpanName(%q) = %q, want %q", c.name, got, c.want)
		}
	}
}
