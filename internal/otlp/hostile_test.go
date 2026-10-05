package otlp

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

func doubleAttr(key string, v float64) *commonv1.KeyValue {
	return &commonv1.KeyValue{Key: key, Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_DoubleValue{DoubleValue: v}}}
}

// A tool call's index is the sender's: a huge one is one tool call, not a
// slice of that length.
func TestFlattenedToolCallIndexIsNotASize(t *testing.T) {
	msgs := flattened(map[string]string{
		"gen_ai.completion.0.role":                                           "assistant",
		"gen_ai.completion.0.tool_calls.2000000000.name":                     "far",
		"gen_ai.completion.0.tool_calls.1.name":                              "near",
		"gen_ai.completion.0.tool_calls.99999999999999999999999.name":        "beyond int64",
		"gen_ai.completion.0.tool_calls.2000000000.arguments":                "{}",
		"gen_ai.completion.7.tool_calls.0.name":                              "another message",
		"llm.output_messages.3.message.tool_calls.5.tool_call.function.name": "openinference",
	}, true)
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3: %+v", len(msgs), msgs)
	}
	var names []string
	for _, c := range msgs[0].ToolCalls {
		names = append(names, c.Name+c.Arguments)
	}
	if got := strings.Join(names, " "); got != "near far{} beyond int64" {
		t.Errorf("tool calls in index order = %q", got)
	}
}

// One NaN or infinite attribute must not cost the span its other attributes,
// and an infinite reported cost is not a cost.
func TestNonFiniteAttributesAreKeptAsTextAndNeverACost(t *testing.T) {
	span := newTestSpan(testTraceID, "0102030405060708", "", 1, 2, tracev1.Status_STATUS_CODE_OK,
		stringAttr("gen_ai.system", "openai"), stringAttr("kept", "yes"),
		doubleAttr("temperature", math.NaN()), doubleAttr("llm.cost.total", math.Inf(1)))
	s := translateSpan(span, time.Now())
	var meta map[string]any
	if err := json.Unmarshal(s.Metadata, &meta); err != nil {
		t.Fatalf("metadata %q: %v", s.Metadata, err)
	}
	if meta["kept"] != "yes" || meta["temperature"] != "NaN" || meta["llm.cost.total"] != "+Inf" {
		t.Errorf("metadata = %v, want every attribute, the non-finite ones as text", meta)
	}
	if s.CostUSD != nil || CostReported(s) {
		t.Errorf("an infinite reported cost was stored: %v", *s.CostUSD)
	}
}
