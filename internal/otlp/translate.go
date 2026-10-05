package otlp

import (
	"encoding/hex"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/jhisse/spoor/internal/store"
)

// Attribute to column precedence. Response model before request: it is the
// model that served the call. Each token list names one column in the OTel
// GenAI semconv, OpenLLMetry's older names, OpenInference's
// llm.token_count.*, and Claude Code's bare names.
var (
	modelKeys       = []string{"llm.model_name", "gen_ai.response.model", "gen_ai.request.model"}
	inputTokenKeys  = []string{"gen_ai.usage.input_tokens", "gen_ai.usage.prompt_tokens", "llm.token_count.prompt"}
	outputTokenKeys = []string{"gen_ai.usage.output_tokens", "gen_ai.usage.completion_tokens", "llm.token_count.completion", "output_tokens"}
	cacheReadKeys   = []string{"gen_ai.usage.cache_read.input_tokens", "gen_ai.usage.cache_read_input_tokens", "llm.token_count.prompt_details.cache_read", "cache_read_tokens"}
	cacheWriteKeys  = []string{"gen_ai.usage.cache_creation.input_tokens", "gen_ai.usage.cache_creation_input_tokens", "llm.token_count.prompt_details.cache_write", "cache_creation_tokens"}
	reasoningKeys   = []string{"gen_ai.usage.reasoning.output_tokens", "gen_ai.usage.reasoning_tokens", "gen_ai.usage.details.reasoning_tokens", "llm.token_count.completion_details.reasoning"}
	sessionIDKeys   = []string{"session.id", "thread_id", "conversation_id", "gen_ai.conversation.id", "traceloop.association.properties.session_id"}
	// A cost the sender reports replaces spoor's own calculation.
	reportedCostKeys = []string{reservedCostAttr, "llm.cost.total", "gen_ai.cost.total_cost"}
)

// operationKinds maps gen_ai.operation.name to a span kind. A value not
// listed here says nothing about the kind.
var operationKinds = map[string]store.SpanKind{
	"chat":             store.SpanKindGeneration,
	"text_completion":  store.SpanKindGeneration,
	"generate_content": store.SpanKindGeneration,
	"embeddings":       store.SpanKindEmbedding,
	"execute_tool":     store.SpanKindTool,
	"invoke_agent":     store.SpanKindAgent,
	"create_agent":     store.SpanKindAgent,
	"agent_step":       store.SpanKindChain, // Vercel AI SDK
}

const reservedCostAttr = "spoor.cost_usd"

func translateSpan(span *tracev1.Span, now time.Time) store.Span {
	attrs := stringAttrs(span.Attributes)
	allAttrs := jsonAttrs(span.Attributes)

	s := store.Span{
		TraceID:   hex.EncodeToString(span.TraceId),
		ID:        hex.EncodeToString(span.SpanId),
		Kind:      detectKind(attrs),
		Name:      span.Name,
		StartedAt: unixNanoTime(span.StartTimeUnixNano),
		EndedAt:   unixNanoTime(span.EndTimeUnixNano),
		Status:    translateStatus(span.Status),
		CreatedAt: now,
	}
	if len(span.ParentSpanId) > 0 {
		parent := hex.EncodeToString(span.ParentSpanId)
		s.ParentSpanID = &parent
	}
	s.Name = toolSpanName(s.Name, s.Kind, attrs)
	if v, ok := firstPresent(attrs, modelKeys...); ok {
		s.Model = &v
	}
	// An agent span's usage is the total of the model calls under it (the
	// Vercel AI SDK's invoke_agent): as token columns every sum over spans
	// would count it twice, so it stays in metadata.
	aggregate := s.Kind == store.SpanKindAgent
	if !aggregate {
		fillTokens(&s, attrs)
	}
	s.Input = body(attrs, false, "gen_ai.input.messages", "input.value", "gen_ai.tool.call.arguments", "gen_ai.prompt", "user_prompt")
	s.Output = body(attrs, true, "gen_ai.output.messages", "output.value", "gen_ai.tool.call.result", "gen_ai.completion")
	if v, ok := firstFloat64(attrs, reportedCostKeys...); ok {
		s.CostUSD = &v
	}
	s.Metadata = remainingMetadata(allAttrs, aggregate)
	s.Attributes = marshalAttrs(allAttrs)
	if msg := span.GetStatus().GetMessage(); msg != "" {
		s.StatusMessage = &msg
	}
	s.Events, s.Links = translateEvents(span.Events), translateLinks(span.Links)
	return s
}

func fillTokens(s *store.Span, attrs map[string]string) {
	for _, f := range []struct {
		dst  **int64
		keys []string
	}{
		{&s.InputTokens, inputTokenKeys}, {&s.OutputTokens, outputTokenKeys},
		{&s.CacheReadTokens, cacheReadKeys}, {&s.CacheWriteTokens, cacheWriteKeys},
		{&s.ReasoningTokens, reasoningKeys},
	} {
		if v, ok := firstInt64(attrs, f.keys...); ok {
			*f.dst = &v
		}
	}
	fillBareInput(s, attrs)
	normalizeInputTokens(s)
}

// Reprice recalculates a stored span's cost from prices. A cost the sender
// reported is left alone, and so is one that can no longer be calculated:
// repricing never turns a known cost into an unknown one.
func Reprice(prices []store.ModelPrice, s store.Span) store.Span {
	if !CostReported(s) {
		priceSpan(prices, &s)
	}
	return s
}

// CostReported says whether the sender reported s's cost itself, so spoor did
// not calculate it. Read from the stored metadata: it needs no column.
func CostReported(s store.Span) bool {
	_, reported := firstFloat64(storedAttrs(s), reportedCostKeys...)
	return reported
}

// storedAttrs reads a stored span's scalar attributes back from its metadata.
func storedAttrs(s store.Span) map[string]string {
	var meta map[string]any
	_ = json.Unmarshal(s.Metadata, &meta) // not an object: nothing to read back
	attrs := map[string]string{}
	for k, v := range meta {
		switch x := v.(type) {
		case string:
			attrs[k] = x
		case float64:
			attrs[k] = strconv.FormatFloat(x, 'f', -1, 64)
		}
	}
	return attrs
}

// normalizeInputTokens enforces store.Span's contract: InputTokens is the
// whole prompt, cache read and cache write included. Per dialect:
//   - OTel GenAI semconv: input_tokens includes cached tokens by definition.
//   - OpenLLMetry: its Anthropic instrumentor adds both cache counters into
//     prompt_tokens; its OpenAI one passes on OpenAI's prompt_tokens, which
//     includes cached tokens (testdata/traceloop-*: 1220 sent for 20 fresh,
//     1000 read and 200 written).
//   - OpenInference: prompt_details.* are parts of prompt
//     (testdata/openinference-anthropic-*: the same 1220).
//   - Anthropic's own usage object, copied as-is by hand-written
//     instrumentation: input_tokens EXCLUDES both cache counters.
//
// So the reported value is kept unless it cannot be the whole prompt: cache
// tokens exceeding it prove the disjoint convention, and are added. Disjoint
// input whose cache is smaller than its fresh part is indistinguishable from
// the inclusive form and stays as reported.
func normalizeInputTokens(s *store.Span) {
	if s.InputTokens == nil {
		return
	}
	if cache := store.Deref(s.CacheReadTokens) + store.Deref(s.CacheWriteTokens); cache > *s.InputTokens {
		total := *s.InputTokens + cache
		s.InputTokens = &total
	}
}

func detectKind(attrs map[string]string) store.SpanKind {
	if v, ok := attrs["openinference.span.kind"]; ok {
		if kind, ok := valueKinds[strings.ToLower(v)]; ok {
			return kind
		}
	}
	if kind, ok := operationKinds[attrs["gen_ai.operation.name"]]; ok {
		return kind
	}
	for key := range attrs {
		if key == "gen_ai.provider.name" || key == "gen_ai.system" || strings.HasPrefix(key, "gen_ai.usage.") {
			return store.SpanKindGeneration
		}
	}
	if v, ok := attrs["traceloop.span.kind"]; ok {
		if kind, ok := valueKinds[strings.ToLower(v)]; ok {
			return kind
		}
	}
	if kind, ok := spanTypeKinds[attrs["span.type"]]; ok {
		return kind
	}
	return store.SpanKindGeneric
}

var valueKinds = map[string]store.SpanKind{
	"llm": store.SpanKindGeneration, "generation": store.SpanKindGeneration,
	"agent": store.SpanKindAgent, "tool": store.SpanKindTool,
	"chain": store.SpanKindChain, "workflow": store.SpanKindChain, "task": store.SpanKindChain,
	"retriever": store.SpanKindRetriever, "retrieval": store.SpanKindRetriever,
	"embedding": store.SpanKindEmbedding, "embeddings": store.SpanKindEmbedding,
	"reranker": store.SpanKindReranker, "rerank": store.SpanKindReranker,
}

func translateStatus(s *tracev1.Status) store.Status {
	switch s.GetCode() {
	case tracev1.Status_STATUS_CODE_OK:
		return store.StatusOK
	case tracev1.Status_STATUS_CODE_ERROR:
		return store.StatusError
	default:
		return store.StatusUnset
	}
}

// body is a span's input or output as a JSON message array: rebuilt from
// the flattened attributes, else from the OTel GenAI parts form in keys[0]
// (the system instructions, a separate attribute, lead the input). With
// neither, the first of keys present, as sent.
func body(attrs map[string]string, output bool, keys ...string) *string {
	msgs := flattened(attrs, output)
	if len(msgs) == 0 {
		system := attrs["gen_ai.system_instructions"]
		if output {
			system = ""
		}
		msgs = partsMessages(attrs[keys[0]], system)
	}
	if len(msgs) > 0 {
		// OpenInference reports why the call stopped once, outside the messages.
		if last := &msgs[len(msgs)-1]; output && last.FinishReason == "" {
			last.FinishReason = attrs["llm.finish_reason"]
		}
		b, _ := json.Marshal(msgs) // strings only: cannot fail
		v := string(b)
		return &v
	}
	if v, ok := firstPresent(attrs, keys...); ok {
		return &v
	}
	return nil
}

// consumedExact are the attribute keys mapped to a spans column, left out
// of spans.metadata. gen_ai.conversation.id stays in metadata although it is
// promoted to traces.session_id, and so do the reported-cost attributes:
// they are how the UI and reprice know a cost came from the sender.
var consumedExact = map[string]bool{
	"gen_ai.response.model":          true,
	"gen_ai.request.model":           true,
	"gen_ai.usage.input_tokens":      true,
	"gen_ai.usage.prompt_tokens":     true,
	"gen_ai.usage.output_tokens":     true,
	"gen_ai.usage.completion_tokens": true,
	"gen_ai.input.messages":          true,
	"gen_ai.output.messages":         true,
	"gen_ai.prompt":                  true,
	"gen_ai.completion":              true,
	"gen_ai.tool.call.arguments":     true,
	"gen_ai.tool.call.result":        true,
	"input.value":                    true,
	"output.value":                   true,
}

// remainingMetadata is every attribute not stored in a column; keepUsage
// says the usage attributes were not.
func remainingMetadata(attrs map[string]any, keepUsage bool) json.RawMessage {
	remaining := map[string]any{}
	for k, v := range attrs {
		if consumedExact[k] && (!keepUsage || !strings.Contains(k, ".usage.")) {
			continue
		}
		if messageField.MatchString(k) {
			continue
		}
		remaining[k] = v
	}
	return marshalAttrs(remaining)
}

// marshalAttrs is attrs as a JSON object; nil when there are none.
func marshalAttrs(attrs map[string]any) json.RawMessage {
	if len(attrs) == 0 {
		return nil
	}
	b, err := json.Marshal(attrs)
	if err != nil {
		return nil
	}
	return b
}

func extractSessionID(span *tracev1.Span) *string {
	if v, ok := firstPresent(stringAttrs(span.Attributes), sessionIDKeys...); ok {
		return &v
	}
	return nil
}
