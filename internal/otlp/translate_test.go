package otlp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/jhisse/spoor/internal/store"
)

// loadCapturedSpan returns the single span of one of the single-span
// captures in testdata/.
func loadCapturedSpan(t *testing.T, name string) *tracev1.Span {
	t.Helper()
	data, err := os.ReadFile("../../testdata/" + name + ".pb") // #nosec G304 -- test-only, name comes from call sites in this file, never external input
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	var req collectortrace.ExportTraceServiceRequest
	if err := proto.Unmarshal(data, &req); err != nil {
		t.Fatalf("unmarshaling testdata: %v", err)
	}
	return req.ResourceSpans[0].ScopeSpans[0].Spans[0]
}

func TestTranslateSpanRealAnthropicSimple(t *testing.T) {
	span := loadCapturedSpan(t, "openllmetry-anthropic-simple")
	s := translateSpan(span, time.Now().UTC())

	if s.Kind != store.SpanKindGeneration {
		t.Errorf("Kind = %q, want generation", s.Kind)
	}
	if s.Model == nil || *s.Model != "claude-haiku-4-5-20251001" {
		t.Errorf("Model = %v, want claude-haiku-4-5-20251001 (response model wins over request model)", s.Model)
	}
	if s.InputTokens == nil || *s.InputTokens != 19 {
		t.Errorf("InputTokens = %v, want 19", s.InputTokens)
	}
	if s.OutputTokens == nil || *s.OutputTokens != 10 {
		t.Errorf("OutputTokens = %v, want 10", s.OutputTokens)
	}
	if s.Input == nil {
		t.Fatal("Input is nil, want reconstructed prompt JSON")
	}
	var prompts []message
	if err := json.Unmarshal([]byte(*s.Input), &prompts); err != nil {
		t.Fatalf("Input is not valid JSON: %v", err)
	}
	if len(prompts) != 1 || prompts[0].Role != "user" {
		t.Errorf("prompts = %+v, want one user message", prompts)
	}
	if s.Output == nil {
		t.Fatal("Output is nil")
	}
	if s.Status != store.StatusOK {
		t.Errorf("Status = %q, want ok", s.Status)
	}
	if s.CostUSD != nil {
		t.Errorf("CostUSD = %v, want nil (no spoor.cost_usd attribute, no calculation applied here)", s.CostUSD)
	}
}

func TestTranslateSpanRealAnthropicToolUse(t *testing.T) {
	span := loadCapturedSpan(t, "openllmetry-anthropic-tool-use")
	s := translateSpan(span, time.Now().UTC())

	if s.Output == nil {
		t.Fatal("Output is nil")
	}
	var completions []message
	if err := json.Unmarshal([]byte(*s.Output), &completions); err != nil {
		t.Fatalf("Output is not valid JSON: %v", err)
	}
	if len(completions) != 1 {
		t.Fatalf("completions = %+v, want 1 message", completions)
	}
	if completions[0].FinishReason != "tool_use" {
		t.Errorf("FinishReason = %q, want tool_use", completions[0].FinishReason)
	}
	if len(completions[0].ToolCalls) != 1 || completions[0].ToolCalls[0].Name != "get_weather" {
		t.Errorf("ToolCalls = %+v, want one get_weather call", completions[0].ToolCalls)
	}

	// Anthropic's tool-schema attribute (llm.request.functions.0.input_schema)
	// has no column, so it must survive into spans.metadata.
	if s.Metadata == nil {
		t.Fatal("Metadata is nil, want the unmapped llm.request.functions.* attributes")
	}
	var meta map[string]any
	if err := json.Unmarshal(s.Metadata, &meta); err != nil {
		t.Fatalf("Metadata is not valid JSON: %v", err)
	}
	if _, ok := meta["llm.request.functions.0.input_schema"]; !ok {
		t.Errorf("Metadata missing llm.request.functions.0.input_schema, got keys: %v", metaKeys(meta))
	}
}

func TestTranslateSpanRealOpenAIToolUse(t *testing.T) {
	span := loadCapturedSpan(t, "openllmetry-openai-openrouter-tool-use")
	s := translateSpan(span, time.Now().UTC())

	var meta map[string]any
	if err := json.Unmarshal(s.Metadata, &meta); err != nil {
		t.Fatalf("Metadata is not valid JSON: %v", err)
	}
	// OpenLLMetry's OpenAI instrumentor names the tool schema "parameters",
	// where the Anthropic one says "input_schema".
	if _, ok := meta["llm.request.functions.0.parameters"]; !ok {
		t.Errorf("Metadata missing llm.request.functions.0.parameters, got keys: %v", metaKeys(meta))
	}
}

func metaKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func TestDetectKindGenericWithoutGenAIAttrs(t *testing.T) {
	attrs := map[string]string{"http.method": "GET"}
	if got := detectKind(attrs); got != store.SpanKindGeneric {
		t.Errorf("detectKind = %q, want generic", got)
	}
}

func TestReservedCostAttrOverridesCalculation(t *testing.T) {
	span := loadCapturedSpan(t, "openllmetry-anthropic-simple")
	span.Attributes = append(span.Attributes, spoorCostAttr(0.5))

	s := translateSpan(span, time.Now().UTC())
	if s.CostUSD == nil || *s.CostUSD != 0.5 {
		t.Errorf("CostUSD = %v, want 0.5 from spoor.cost_usd", s.CostUSD)
	}
	// The attribute stays in metadata: it is how the UI tells a reported
	// cost from one spoor calculated.
	if !strings.Contains(string(s.Metadata), `"spoor.cost_usd":0.5`) {
		t.Errorf("metadata = %s, want spoor.cost_usd kept", s.Metadata)
	}
}

func TestDetectKindFromInstrumentorAttrs(t *testing.T) {
	cases := []struct {
		name  string
		attrs map[string]string
		want  store.SpanKind
	}{
		{"openinference tool", map[string]string{"openinference.span.kind": "TOOL"}, store.SpanKindTool},
		{"openinference wins over gen_ai.system", map[string]string{"openinference.span.kind": "AGENT", "gen_ai.system": "x"}, store.SpanKindAgent},
		{"traceloop workflow", map[string]string{"traceloop.span.kind": "workflow"}, store.SpanKindChain},
		{"unknown kind value falls through", map[string]string{"traceloop.span.kind": "mystery"}, store.SpanKindGeneric},
	}
	for _, c := range cases {
		// Repeated: map iteration order is randomized, and the result must not depend on it.
		for range 20 {
			if got := detectKind(c.attrs); got != c.want {
				t.Fatalf("%s: detectKind = %q, want %q", c.name, got, c.want)
			}
		}
	}
}

func TestMetadataPreservesNonStringAttrs(t *testing.T) {
	span := newTestSpan(testTraceID, "0102030405060708", "", 1, 2, tracev1.Status_STATUS_CODE_UNSET,
		&commonv1.KeyValue{Key: "retries", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: 3}}},
		&commonv1.KeyValue{Key: "tags", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_ArrayValue{ArrayValue: &commonv1.ArrayValue{
			Values: []*commonv1.AnyValue{{Value: &commonv1.AnyValue_StringValue{StringValue: "a"}}},
		}}}},
	)
	s := translateSpan(span, time.Now().UTC())

	var meta map[string]any
	if err := json.Unmarshal(s.Metadata, &meta); err != nil {
		t.Fatalf("Metadata is not valid JSON: %v", err)
	}
	if meta["retries"] != float64(3) {
		t.Errorf("retries = %#v, want the number 3", meta["retries"])
	}
	if tags, ok := meta["tags"].([]any); !ok || len(tags) != 1 || tags[0] != "a" {
		t.Errorf("tags = %#v, want [a]", meta["tags"])
	}
}

// Every attribute the sender put on a span is kept as sent, the ones read
// into columns and the flattened prompt messages included: metadata leaves
// those out, so a changed reading can be applied to what is stored.
func TestAttributesKeepEverythingAsSent(t *testing.T) {
	span := loadCapturedSpan(t, "openllmetry-anthropic-simple")
	s := translateSpan(span, time.Now().UTC())

	var kept, meta map[string]any
	if err := json.Unmarshal(s.Attributes, &kept); err != nil {
		t.Fatalf("Attributes is not a JSON object: %v", err)
	}
	_ = json.Unmarshal(s.Metadata, &meta)
	if len(kept) != len(span.Attributes) {
		t.Errorf("%d attributes kept, %d sent", len(kept), len(span.Attributes))
	}
	left := 0
	for _, kv := range span.Attributes {
		got, ok := kept[kv.Key]
		if !ok {
			t.Errorf("attribute %q was not kept", kv.Key)
			continue
		}
		if want := kv.Value.GetStringValue(); want != "" && got != want {
			t.Errorf("attribute %q changed: %v", kv.Key, got)
		}
		if _, inMeta := meta[kv.Key]; !inMeta {
			left++
		}
	}
	if left == 0 {
		t.Error("the capture should have attributes that metadata leaves out (the point of keeping them)")
	}
	if none := translateSpan(newTestSpan(testTraceID, "0102030405060708", "", 1, 2, tracev1.Status_STATUS_CODE_UNSET), time.Now()); none.Attributes != nil {
		t.Errorf("a span without attributes keeps %q, want nil", none.Attributes)
	}
}

// Pydantic AI's output of a call with thinking: the thinking part is a part of its
// own, and the answer stays what it was.
func TestPartsMessagesKeepThinkingApart(t *testing.T) {
	raw := `[{"role":"assistant","parts":[{"type":"thinking","content":"Day 8: 7 m + 3 m."},{"type":"text","content":"On the 8th day."}],"finish_reason":"stop"}]`
	got := partsMessages(raw, "")
	if len(got) != 1 || got[0].Reasoning != "Day 8: 7 m + 3 m." || got[0].Content != "On the 8th day." || got[0].FinishReason != "stop" {
		t.Errorf("partsMessages = %+v, want the thinking in Reasoning and the answer in Content", got)
	}
}

// An image sent inline (Pydantic AI, the GenAI blob part) reads as its kind and size.
func TestPartsMessagesBlobIsDescribedNotDumped(t *testing.T) {
	raw := `[{"role":"user","parts":[{"type":"text","content":"What colour?"},{"type":"blob","mime_type":"image/png","modality":"image","content":"iVBORw0KGgo="}]}]`
	got := partsMessages(raw, "")
	if want := "What colour?\n\n[image: image/png, 8 bytes]"; len(got) != 1 || got[0].Content != want {
		t.Errorf("partsMessages = %+v, want content %q", got, want)
	}
}

// OpenInference's picture in a message (LangChain, a data URL) reads as its type and size.
func TestFlattenedImageIsDescribedNotDumped(t *testing.T) {
	attrs := map[string]string{
		"llm.input_messages.0.message.role":                                       "user",
		"llm.input_messages.0.message.contents.0.message_content.text":            "What colour?",
		"llm.input_messages.0.message.contents.1.message_content.image.image.url": "data:image/png;base64,iVBORw0KGgo=",
	}
	got := flattened(attrs, false)
	if want := "What colour?\n\n[image: image/png, 8 bytes]"; len(got) != 1 || got[0].Content != want {
		t.Errorf("flattened = %+v, want content %q", got, want)
	}
}
