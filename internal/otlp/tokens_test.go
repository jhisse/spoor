package otlp

import (
	"testing"
	"time"

	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/jhisse/spoor/internal/store"
)

func translateAttrs(attrs map[string]string) store.Span {
	span := newTestSpan(testTraceID, "0102030405060708", "", 1, 2, tracev1.Status_STATUS_CODE_OK)
	for k, v := range attrs {
		span.Attributes = append(span.Attributes, stringAttr(k, v))
	}
	return translateSpan(span, time.Now().UTC())
}

func wantTokens(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("%s = %v, want %d", name, got, want)
	}
}

// OpenLLMetry's Anthropic instrumentor always emits both cache counters;
// in every capture they are zero, which must be stored as a reported zero.
func TestTranslateSpanRealAnthropicCacheCounters(t *testing.T) {
	inputs := map[string]int64{
		"openllmetry-anthropic-simple":        19,
		"openllmetry-anthropic-sonnet-simple": 20,
		"openllmetry-anthropic-tool-use":      583,
		"openllmetry-anthropic-multiturn-2":   31,
	}
	for name, input := range inputs {
		s := translateSpan(loadCapturedSpan(t, name), time.Now().UTC())
		wantTokens(t, name+" InputTokens", s.InputTokens, input)
		wantTokens(t, name+" CacheReadTokens", s.CacheReadTokens, 0)
		wantTokens(t, name+" CacheWriteTokens", s.CacheWriteTokens, 0)
		if s.ReasoningTokens != nil {
			t.Errorf("%s ReasoningTokens = %d, want nil (not reported)", name, *s.ReasoningTokens)
		}
	}
}

// OpenLLMetry's OpenAI instrumentor reports no cache counters at all in
// the captures: unreported must stay nil, not become zero.
func TestTranslateSpanRealOpenAINoCacheCounters(t *testing.T) {
	s := translateSpan(loadCapturedSpan(t, "openllmetry-openai-openrouter-simple"), time.Now().UTC())
	wantTokens(t, "InputTokens", s.InputTokens, 19)
	if s.CacheReadTokens != nil || s.CacheWriteTokens != nil {
		t.Errorf("cache tokens = %v/%v, want nil/nil", s.CacheReadTokens, s.CacheWriteTokens)
	}
}

// The attribute names and numbers of a real OpenInference llm.call span
// from an agent trace: prompt already includes both cache counters.
func TestTranslateSpanOpenInferenceTokenCounts(t *testing.T) {
	s := translateAttrs(map[string]string{
		"openinference.span.kind":                      "LLM",
		"gen_ai.operation.name":                        "chat",
		"llm.model_name":                               "claude-sonnet-5",
		"llm.token_count.prompt":                       "88752",
		"llm.token_count.prompt_details.cache_read":    "87486",
		"llm.token_count.prompt_details.cache_write":   "1264",
		"llm.token_count.completion":                   "633",
		"llm.token_count.completion_details.reasoning": "120",
	})
	if s.Kind != store.SpanKindGeneration {
		t.Errorf("Kind = %q, want generation", s.Kind)
	}
	wantTokens(t, "InputTokens", s.InputTokens, 88752)
	wantTokens(t, "CacheReadTokens", s.CacheReadTokens, 87486)
	wantTokens(t, "CacheWriteTokens", s.CacheWriteTokens, 1264)
	wantTokens(t, "OutputTokens", s.OutputTokens, 633)
	wantTokens(t, "ReasoningTokens", s.ReasoningTokens, 120)
}

// Anthropic's own usage object reports input_tokens without the cache
// counters; the same call as above then arrives as input 2.
func TestTranslateSpanDisjointInputIsMadeWhole(t *testing.T) {
	s := translateAttrs(map[string]string{
		"gen_ai.usage.input_tokens":                "2",
		"gen_ai.usage.cache_read.input_tokens":     "87486",
		"gen_ai.usage.cache_creation.input_tokens": "1264",
		"gen_ai.usage.reasoning.output_tokens":     "7",
	})
	wantTokens(t, "InputTokens", s.InputTokens, 88752)
	wantTokens(t, "CacheReadTokens", s.CacheReadTokens, 87486)
	wantTokens(t, "CacheWriteTokens", s.CacheWriteTokens, 1264)
	wantTokens(t, "ReasoningTokens", s.ReasoningTokens, 7)
}

func TestReportedCostFromOpenInference(t *testing.T) {
	s := translateAttrs(map[string]string{"llm.cost.total": "0.028887"})
	if s.CostUSD == nil || *s.CostUSD != 0.028887 {
		t.Errorf("CostUSD = %v, want 0.028887 from llm.cost.total", s.CostUSD)
	}
	s = translateAttrs(map[string]string{"llm.cost.total": "0.028887", "spoor.cost_usd": "0.5"})
	if s.CostUSD == nil || *s.CostUSD != 0.5 {
		t.Errorf("CostUSD = %v, want 0.5 (spoor.cost_usd wins)", s.CostUSD)
	}
}

func TestDetectKindFromOperationName(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]string
		want  store.SpanKind
	}{
		{"chat", map[string]string{"gen_ai.operation.name": "chat"}, store.SpanKindGeneration},
		{"text_completion", map[string]string{"gen_ai.operation.name": "text_completion"}, store.SpanKindGeneration},
		{"generate_content", map[string]string{"gen_ai.operation.name": "generate_content"}, store.SpanKindGeneration},
		{"embeddings", map[string]string{"gen_ai.operation.name": "embeddings"}, store.SpanKindEmbedding},
		{"execute_tool", map[string]string{"gen_ai.operation.name": "execute_tool"}, store.SpanKindTool},
		{"invoke_agent", map[string]string{"gen_ai.operation.name": "invoke_agent"}, store.SpanKindAgent},
		{"create_agent", map[string]string{"gen_ai.operation.name": "create_agent"}, store.SpanKindAgent},
		{"tool span that also names its provider", map[string]string{"gen_ai.operation.name": "execute_tool", "gen_ai.system": "anthropic"}, store.SpanKindTool},
		{"openinference kind wins", map[string]string{"gen_ai.operation.name": "chat", "openinference.span.kind": "TOOL"}, store.SpanKindTool},
		{"unknown operation alone", map[string]string{"gen_ai.operation.name": "mystery"}, store.SpanKindGeneric},
		{"unknown operation with usage", map[string]string{"gen_ai.operation.name": "mystery", "gen_ai.usage.input_tokens": "1"}, store.SpanKindGeneration},
	}
	for _, c := range cases {
		if got := detectKind(c.attrs); got != c.want {
			t.Errorf("%s: detectKind = %q, want %q", c.name, got, c.want)
		}
	}
}

// A prompt read entirely from cache, in a dialect whose prompt count already
// includes it: cache equal to input does not exceed it, so nothing is added.
func TestTranslateSpanFullyCachedInclusiveInputIsKept(t *testing.T) {
	s := translateAttrs(map[string]string{
		"llm.token_count.prompt":                    "1000",
		"llm.token_count.prompt_details.cache_read": "1000",
	})
	wantTokens(t, "InputTokens", s.InputTokens, 1000)
}
