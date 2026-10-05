package web

import (
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestParseMessagesPlainExchange(t *testing.T) {
	raw := strPtr(`[{"role":"user","content":"What is the capital of France? Answer in one sentence."}]`)
	got := parseMessages(raw)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	if got[0].Role != "user" || got[0].Content != "What is the capital of France? Answer in one sentence." {
		t.Errorf("got %+v", got[0])
	}
}

// The shape of testdata/openllmetry-anthropic-tool-use-2's completion, as
// internal/otlp stores it.
func TestParseMessagesToolCall(t *testing.T) {
	raw := strPtr(`[{"role":"assistant","finish_reason":"tool_use","tool_calls":[{"id":"toolu_0197GGRkb9q4w7hrrtfU6z1q","name":"convert_currency","arguments":"{\"amount\": 250, \"from_currency\": \"USD\", \"to_currency\": \"EUR\"}"}]}]`)
	got := parseMessages(raw)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	msg := got[0]
	if msg.Role != "assistant" || msg.FinishReason != "tool_use" {
		t.Errorf("got %+v", msg)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "convert_currency" {
		t.Fatalf("ToolCalls = %+v", msg.ToolCalls)
	}
	// Object-shaped arguments render as a key/value tree.
	want := []metadataNode{
		{Key: "amount", Value: "250"},
		{Key: "from_currency", Value: "USD"},
		{Key: "to_currency", Value: "EUR"},
	}
	if !reflect.DeepEqual(msg.ToolCalls[0].ArgumentsTree, want) {
		t.Errorf("ArgumentsTree = %+v, want %+v", msg.ToolCalls[0].ArgumentsTree, want)
	}
	if msg.ToolCalls[0].Arguments != "" {
		t.Errorf("Arguments = %q, want empty (tree rendering used instead)", msg.ToolCalls[0].Arguments)
	}
}

// The second anthropic.chat span of testdata/openllmetry-langgraph-tool-loop:
// an assistant turn whose content is a JSON-encoded array of Anthropic
// content blocks, because the turn is a tool call.
func TestParseMessagesAnthropicToolUseContentBlock(t *testing.T) {
	raw := strPtr(`[{"role":"assistant","content":"[{\"type\": \"tool_use\", \"name\": \"get_stock_price\", \"input\": {\"ticker\": \"GOOG\"}, \"id\": \"toolu_01VTj8Br4GDTn7ewFdgVmFK5\", \"caller\": {\"type\": \"direct\"}}]"}]`)
	got := parseMessages(raw)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	msg := got[0]
	if msg.Content != "" {
		t.Errorf("Content = %q, want empty — should have expanded into ToolCalls", msg.Content)
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "get_stock_price" {
		t.Fatalf("ToolCalls = %+v", msg.ToolCalls)
	}
	want := []metadataNode{{Key: "ticker", Value: "GOOG"}}
	if !reflect.DeepEqual(msg.ToolCalls[0].ArgumentsTree, want) {
		t.Errorf("ArgumentsTree = %+v, want %+v", msg.ToolCalls[0].ArgumentsTree, want)
	}
}

// The paired tool_result turn (a "user" message reporting the tool's output
// back), from the same capture.
func TestParseMessagesAnthropicToolResultContentBlock(t *testing.T) {
	raw := strPtr(`[{"role":"user","content":"[{\"type\": \"tool_result\", \"content\": \"$312.44\", \"tool_use_id\": \"toolu_01VTj8Br4GDTn7ewFdgVmFK5\", \"is_error\": false}]"}]`)
	got := parseMessages(raw)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	msg := got[0]
	if len(msg.ToolResults) != 1 {
		t.Fatalf("ToolResults = %+v, want 1", msg.ToolResults)
	}
	tr := msg.ToolResults[0]
	if tr.Content != "$312.44" || tr.IsError || tr.ToolUseID != "toolu_01VTj8Br4GDTn7ewFdgVmFK5" {
		t.Errorf("ToolResults[0] = %+v", tr)
	}
}

// A tool_result whose content is an object, not a plain string, renders as
// a tree like tool call arguments.
func TestParseMessagesToolResultObjectContentRendersAsTree(t *testing.T) {
	raw := strPtr(`[{"role":"user","content":"[{\"type\": \"tool_result\", \"content\": {\"price\": \"312.44\", \"currency\": \"USD\"}, \"tool_use_id\": \"toolu_1\", \"is_error\": false}]"}]`)
	got := parseMessages(raw)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	if len(got[0].ToolResults) != 1 {
		t.Fatalf("ToolResults = %+v, want 1", got[0].ToolResults)
	}
	tr := got[0].ToolResults[0]
	want := []metadataNode{
		{Key: "currency", Value: "USD"},
		{Key: "price", Value: "312.44"},
	}
	if !reflect.DeepEqual(tr.ContentTree, want) {
		t.Errorf("ContentTree = %+v, want %+v", tr.ContentTree, want)
	}
	if tr.Content != "" {
		t.Errorf("Content = %q, want empty (tree rendering used instead)", tr.Content)
	}
}

// A turn with a text block and a tool_use block: the text stays in Content
// next to the tool call.
func TestParseMessagesContentBlockWithTextAndToolUse(t *testing.T) {
	raw := strPtr(`[{"role":"assistant","content":"[{\"type\": \"text\", \"text\": \"Let me check that.\"}, {\"type\": \"tool_use\", \"name\": \"get_weather\", \"input\": {\"city\": \"Paris\"}}]"}]`)
	got := parseMessages(raw)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	msg := got[0]
	if msg.Content != "Let me check that." {
		t.Errorf("Content = %q, want %q", msg.Content, "Let me check that.")
	}
	if len(msg.ToolCalls) != 1 || msg.ToolCalls[0].Name != "get_weather" {
		t.Errorf("ToolCalls = %+v", msg.ToolCalls)
	}
}

// An array with an unknown block type is left as text, never guessed at.
func TestParseMessagesUnrecognizedContentBlockTypeLeftAsText(t *testing.T) {
	raw := strPtr(`[{"role":"assistant","content":"[{\"type\": \"future_block_type\", \"stuff\": 1}]"}]`)
	got := parseMessages(raw)
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(got), got)
	}
	want := `[{"type": "future_block_type", "stuff": 1}]`
	if got[0].Content != want {
		t.Errorf("Content = %q, want unchanged %q", got[0].Content, want)
	}
}

// A Claude call with extended thinking, as OpenLLMetry's Anthropic instrumentation
// wrote it: the thinking block is one JSON object in the message's content, with the
// tool call beside it.
func TestParseMessagesReasoningObjectBecomesReasoning(t *testing.T) {
	raw := strPtr(`[{"role":"assistant","content":"{\"type\": \"reasoning\", \"content\": \"Train arrives 17:25.\"}","tool_calls":[{"name":"calculate","arguments":"{\"expression\": \"17 * 23\"}"}]}]`)
	got := parseMessages(raw)
	if len(got) != 1 || got[0].Reasoning != "Train arrives 17:25." || got[0].Content != "" || len(got[0].ToolCalls) != 1 {
		t.Errorf("got %+v, want the reasoning apart, no text and the tool call kept", got)
	}
}

// Anthropic's own thinking block, next to the answer.
func TestParseMessagesThinkingBlock(t *testing.T) {
	raw := strPtr(`[{"role":"assistant","content":"[{\"type\": \"thinking\", \"thinking\": \"Add the durations.\", \"signature\": \"x\"}, {\"type\": \"text\", \"text\": \"18:05.\"}]"}]`)
	got := parseMessages(raw)
	if len(got) != 1 || got[0].Reasoning != "Add the durations." || got[0].Content != "18:05." {
		t.Errorf("got %+v, want the thinking apart and the answer as text", got)
	}
}

// The reasoning internal/otlp keeps apart (the GenAI parts form) reaches the view as it is.
func TestParseMessagesReasoningField(t *testing.T) {
	raw := strPtr(`[{"role":"assistant","content":"On the 8th day.","reasoning":"Day 8: 7 m + 3 m.","finish_reason":"stop"}]`)
	if got := parseMessages(raw); len(got) != 1 || got[0].Reasoning != "Day 8: 7 m + 3 m." || got[0].Content != "On the 8th day." {
		t.Errorf("got %+v, want reasoning and answer apart", got)
	}
}

// A message that is a JSON object without a block type stays text.
func TestParseMessagesObjectWithoutTypeStaysText(t *testing.T) {
	raw := strPtr(`[{"role":"tool","content":"{\"city\": \"Paris\"}"}]`)
	if got := parseMessages(raw); len(got) != 1 || got[0].Content != `{"city": "Paris"}` || got[0].Reasoning != "" {
		t.Errorf("got %+v, want the object unchanged as text", got)
	}
}

// A long text is handed over as a head and a counted remainder that
// together lose nothing but the white space at the cut.
func TestExcerpt(t *testing.T) {
	if got := excerpt("short"); got.Head != "short" || got.More != "" {
		t.Errorf("short text = %+v, want it whole", got)
	}
	if got := excerpt(strings.Repeat("x", excerptLimit+10)); got.More != "" {
		t.Errorf("a text barely over the limit must stay whole, got a remainder of %d", got.N)
	}
	para := strings.Repeat("word ", 80) + "\n" + strings.Repeat("é", 700)
	got := excerpt(para)
	if got.Head != strings.Repeat("word ", 80) || got.More != strings.Repeat("é", 700) || got.N != 700 {
		t.Errorf("want the cut at the line break and N in characters, got head %q and N %d", got.Head, got.N)
	}
	solid := excerpt(strings.Repeat("é", 1000)) // two bytes each: the cut must not split one
	if !utf8.ValidString(solid.Head) || solid.Head+solid.More != strings.Repeat("é", 1000) {
		t.Errorf("unbroken text must be cut on a rune boundary, losing nothing")
	}
}

func TestPrettyJSONValid(t *testing.T) {
	got := prettyJSON(`{"a":1,"b":"two"}`)
	want := "{\n  \"a\": 1,\n  \"b\": \"two\"\n}"
	if got != want {
		t.Errorf("prettyJSON = %q, want %q", got, want)
	}
}

func TestPrettyJSONInvalidReturnsUnchanged(t *testing.T) {
	got := prettyJSON("not json")
	if got != "not json" {
		t.Errorf("prettyJSON(invalid) = %q, want unchanged", got)
	}
}

// What is not a message list gives nil, and the page shows the body raw.
func TestParseMessagesNotAMessageList(t *testing.T) {
	for name, raw := range map[string]*string{"missing": nil, "empty array": strPtr(`[]`), "plain string": strPtr("just a plain string, not JSON at all")} {
		if got := parseMessages(raw); got != nil {
			t.Errorf("%s: parseMessages = %+v, want nil", name, got)
		}
	}
}
