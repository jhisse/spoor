package otlp

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/jhisse/spoor/internal/store"
)

// The captures made by testdata/capture: current SDKs calling a fake
// provider that answers every OpenAI call with 1200 prompt tokens (1024
// cached), 40 output (32 reasoning), and every Anthropic call with 20 fresh,
// 1000 cache read, 200 cache write and 5 output. A "tool" capture is two
// model calls around one get_weather call. -1 is a count the SDK did not
// put on the span.
var sdkCaptures = []struct {
	file, session                             string // session "*": an id the SDK generated
	calls                                     int
	in, out, cacheRead, cacheWrite, reasoning int64
}{
	{"openinference-openai-plain", "capture-session", 1, 1200, 40, 1024, -1, 32},
	{"openinference-openai-tool", "capture-session", 2, 1200, 40, 1024, -1, 32},
	{"openinference-anthropic-plain", "capture-session", 1, 1220, 5, 1000, 200, -1},
	{"openinference-anthropic-tool", "capture-session", 2, 1220, 5, 1000, 200, -1},
	{"openinference-langchain-tool", "capture-session", 2, 1200, 40, 1024, -1, 32},
	{"openinference-llamaindex-tool", "capture-session", 2, 1200, 40, 1024, -1, 32},
	{"openinference-openai-agents-tool", "capture-session", 2, 1200, 40, -1, -1, -1},
	{"traceloop-openai-plain", "capture-session", 1, 1200, 40, 1024, -1, 32},
	{"traceloop-openai-tool", "capture-session", 2, 1200, 40, 1024, -1, 32},
	{"traceloop-anthropic-plain", "capture-session", 1, 1220, 5, 1000, 200, -1},
	{"traceloop-anthropic-tool", "capture-session", 2, 1220, 5, 1000, 200, -1},
	{"otel-openai-v2-tool", "", 2, 1200, 40, -1, -1, -1},
	{"pydantic-ai-tool", "*", 2, 1200, 40, 1024, -1, 32},
	{"vercel-ai-tool", "", 2, 1200, 40, 1024, -1, -1},
}

func sent(p *int64) int64 {
	if p == nil {
		return -1
	}
	return *p
}

func messagesOf(t *testing.T, raw *string) []message {
	t.Helper()
	var msgs []message
	if raw == nil || json.Unmarshal([]byte(*raw), &msgs) != nil {
		t.Fatalf("not a message array: %v", raw)
	}
	return msgs
}

// modelCalls are the generation spans that reported usage, oldest first.
func modelCalls(spans map[string]store.Span) []store.Span {
	var calls []store.Span
	for _, s := range spans {
		if s.Kind == store.SpanKindGeneration && s.OutputTokens != nil {
			calls = append(calls, s)
		}
	}
	slices.SortFunc(calls, func(a, b store.Span) int { return a.StartedAt.Compare(b.StartedAt) })
	return calls
}

// transcript is a message array on one line: "role: content", or
// "role: call <tool> <arguments>" for a tool call.
func transcript(t *testing.T, raw *string) string {
	t.Helper()
	var lines []string
	for _, m := range messagesOf(t, raw) {
		text := m.Content
		for _, c := range m.ToolCalls {
			text += "call " + c.Name + " " + strings.ReplaceAll(c.Arguments, " ", "")
		}
		lines = append(lines, m.Role+": "+text)
	}
	// OpenInference keeps Anthropic's role for a tool result, "user".
	return strings.ReplaceAll(strings.Join(lines, " | "), "user: 18 C", "tool: 18 C")
}

func TestSDKCapturesReadAsChatWithTokens(t *testing.T) {
	for _, c := range sdkCaptures {
		t.Run(c.file, func(t *testing.T) {
			tr, spans := postCapture(t, c.file)
			if session := store.Deref(tr.SessionID); session != c.session && (c.session != "*" || session == "") {
				t.Errorf("session = %q, want %q", session, c.session)
			}
			calls := modelCalls(spans)
			if len(calls) != c.calls {
				t.Fatalf("got %d model calls with usage, want %d", len(calls), c.calls)
			}
			for _, s := range calls {
				got := []int64{sent(s.InputTokens), sent(s.OutputTokens), sent(s.CacheReadTokens), sent(s.CacheWriteTokens), sent(s.ReasoningTokens)}
				if want := []int64{c.in, c.out, c.cacheRead, c.cacheWrite, c.reasoning}; !slices.Equal(got, want) {
					t.Errorf("%s: input, output, cache read, cache write, reasoning = %v, want %v", s.Name, got, want)
				}
				if s.Model == nil || s.CostUSD == nil {
					t.Errorf("%s: model %v, cost %v; want both", s.Name, s.Model, s.CostUSD)
				}
			}

			first, last := calls[0], calls[len(calls)-1]
			want := []string{
				"system: Answer in one word. | user: Capital of France?",
				"assistant: Paris.",
			}
			got := []string{transcript(t, first.Input), transcript(t, last.Output)}
			if c.calls == 2 {
				want = []string{
					"system: Answer in one word. | user: Weather in Paris?",
					`assistant: call get_weather {"city":"Paris"}`,
					`system: Answer in one word. | user: Weather in Paris? | assistant: call get_weather {"city":"Paris"} | tool: 18 C, clear`,
					"assistant: Paris.",
				}
				got = []string{transcript(t, first.Input), transcript(t, first.Output), transcript(t, last.Input), transcript(t, last.Output)}
			}
			if !slices.Equal(got, want) {
				t.Errorf("conversation:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func spanNamed(t *testing.T, spans map[string]store.Span, name string) store.Span {
	t.Helper()
	for _, s := range spans {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("no span named %q", name)
	return store.Span{}
}

// The Vercel AI SDK's root invoke_agent span repeats the usage of the two
// calls under it. It must not be stored as tokens or priced; its step spans
// and its tool span are read by their operation name.
func TestVercelAgentSpanUsageIsNotCountedTwice(t *testing.T) {
	tr, spans := postCapture(t, "vercel-ai-tool")
	root := spanNamed(t, spans, tr.Name)
	if root.Kind != store.SpanKindAgent || root.InputTokens != nil || root.OutputTokens != nil || root.CostUSD != nil {
		t.Errorf("root: kind %s, tokens %v/%v, cost %v; want an agent span with no usage of its own", root.Kind, root.InputTokens, root.OutputTokens, root.CostUSD)
	}
	if !strings.Contains(string(root.Metadata), `"gen_ai.usage.input_tokens":2400`) {
		t.Errorf("root metadata lost the usage the SDK sent: %s", root.Metadata)
	}
	if step := spanNamed(t, spans, "step 1"); step.Kind != store.SpanKindChain {
		t.Errorf("step kind = %s, want chain", step.Kind)
	}
	tool := spanNamed(t, spans, "execute_tool get_weather")
	if tool.Kind != store.SpanKindTool || store.Deref(tool.Input) != `{"city":"Paris"}` || !strings.Contains(store.Deref(tool.Output), "18 C, clear") {
		t.Errorf("tool span: kind %s, input %v, output %v", tool.Kind, store.Deref(tool.Input), store.Deref(tool.Output))
	}
}

// LiteLLM sends the tool call flattened as function_call, no cached-token
// count, and its own cost, which is what spoor stores: priced from the span
// alone, all 1200 prompt tokens would count as fresh ($0.000204).
func TestLiteLLMCaptureKeepsItsReportedCost(t *testing.T) {
	_, spans := postCapture(t, "litellm-otel-tool")
	call := spanNamed(t, spans, "litellm_request")
	if call.Kind != store.SpanKindGeneration || sent(call.InputTokens) != 1200 || call.CacheReadTokens != nil {
		t.Errorf("kind %s, input %d, cache read %v", call.Kind, sent(call.InputTokens), call.CacheReadTokens)
	}
	if call.CostUSD == nil || *call.CostUSD != 0.0001272 || !CostReported(call) {
		t.Errorf("cost = %v (reported %v), want the $0.0001272 LiteLLM computed", call.CostUSD, CostReported(call))
	}
	out := messagesOf(t, call.Output)
	if len(out) != 1 || len(out[0].ToolCalls) != 1 || out[0].ToolCalls[0].Name != "get_weather" {
		t.Errorf("output = %+v, want the get_weather call", out)
	}
}

// opentelemetry-instrumentation-openai-v2 in event_only mode sends prompt
// and completion as log records, which spoor discards: the span has usage
// and nothing to read.
func TestOTelOpenAIEventOnlyCaptureHasNoContent(t *testing.T) {
	_, spans := postCapture(t, "otel-openai-v2-events")
	call := spanNamed(t, spans, "chat gpt-4o-mini")
	if call.Input != nil || call.Output != nil || sent(call.InputTokens) != 1200 || call.CostUSD == nil {
		t.Errorf("input %v, output %v, tokens %d, cost %v", call.Input, call.Output, sent(call.InputTokens), call.CostUSD)
	}
}

// Gemini CLI 0.62.0, `gemini --prompt`, with telemetry.traces on: one
// llm_call span, messages in Gemini's own parts form ({"text": ...}, role
// "model"), the system instructions as one JSON string, and no cached or
// thinking token count (the fake reported 1024 cached and 32 thinking).
func TestGeminiCLICapture(t *testing.T) {
	tr, spans := postCapture(t, "gemini-cli-plain")
	call := spanNamed(t, spans, "llm_call")
	got := fmt.Sprintln(tr.SessionID != nil, store.Deref(tr.Service), call.Kind, store.Deref(call.Model),
		sent(call.InputTokens), sent(call.OutputTokens), sent(call.CacheReadTokens), sent(call.ReasoningTokens), call.CostUSD != nil)
	if want := "true gemini-cli generation gemini-3.6-flash 1200 8 -1 -1 true\n"; got != want {
		t.Errorf("session, service, kind, model, input, output, cache read, reasoning, cost = %s, want %s", got, want)
	}
	in := transcript(t, call.Input)
	if !strings.HasPrefix(in, "system: You are Gemini CLI") || !strings.HasSuffix(in, "Capital of France?") {
		t.Errorf("input = %.60q ... %q", in, in[len(in)-40:])
	}
	if out := transcript(t, call.Output); out != "model: Paris." {
		t.Errorf("output = %q", out)
	}
}

// A failed call keeps its prompt, and the model when the SDK names it.
func TestErrorCapturesKeepThePrompt(t *testing.T) {
	for file, model := range map[string]string{
		"openinference-openai-error":    "", // only inside llm.invocation_parameters
		"openinference-anthropic-error": "no-such-model",
		"traceloop-openai-error":        "no-such-model",
		"otel-openai-v2-error":          "no-such-model",
	} {
		tr, spans := postCapture(t, file)
		s := spanNamed(t, spans, tr.Name)
		if tr.Status != store.StatusError || s.Kind != store.SpanKindGeneration || store.Deref(s.Model) != model || s.CostUSD != nil {
			t.Errorf("%s: trace status %s, kind %s, model %q, cost %v", file, tr.Status, s.Kind, store.Deref(s.Model), s.CostUSD)
		}
		if in := transcript(t, s.Input); in != "system: Answer in one word. | user: Capital of France?" {
			t.Errorf("%s: input = %q", file, in)
		}
		if !strings.Contains(store.Deref(s.StatusMessage), "no-such-model") {
			t.Errorf("%s: status message = %q, want the provider's error", file, store.Deref(s.StatusMessage))
		}
	}
}

// The tool spans of the frameworks that export one.
func TestFrameworkToolSpans(t *testing.T) {
	for file, name := range map[string]string{
		"openinference-openai-agents-tool": "get_weather",
		"openinference-langchain-tool":     "get_weather",
		"openinference-llamaindex-tool":    "FunctionTool.acall get_weather",
		"pydantic-ai-tool":                 "execute_tool get_weather",
	} {
		_, spans := postCapture(t, file)
		tool := spanNamed(t, spans, name)
		if tool.Kind != store.SpanKindTool || !strings.Contains(store.Deref(tool.Input), "Paris") || !strings.Contains(store.Deref(tool.Output), "18 C, clear") {
			t.Errorf("%s: kind %s, input %q, output %q", file, tool.Kind, store.Deref(tool.Input), store.Deref(tool.Output))
		}
	}
}
