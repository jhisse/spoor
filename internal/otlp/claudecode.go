package otlp

import "github.com/jhisse/spoor/internal/store"

// Claude Code's own trace export (scope com.anthropic.claude_code.tracing),
// as in testdata/claude-code-*.pb. Its model-call spans carry gen_ai.system
// and gen_ai.request.model, so kind and model need nothing here; its usage
// has un-namespaced names (the bare keys at the end of the token key lists,
// plus input_tokens below), no cost attribute, and session.id on every span.

// spanTypeKinds maps Claude Code's span.type to a kind, for the spans that
// carry no gen_ai.* attribute. tool.execution and tool.blocked_on_user are
// phases of the tool span above them and stay generic.
var spanTypeKinds = map[string]store.SpanKind{
	"interaction": store.SpanKindAgent,
	"tool":        store.SpanKindTool,
}

// FastMode says whether a stored span was served in fast mode: Claude Code's
// speed attribute ("normal" or "fast"). The price table has no fast-mode
// rate, so such a span's cost is the standard one and the UI says so.
func FastMode(s store.Span) bool { return storedAttrs(s)["speed"] == "fast" }

// fillBareInput reads Claude Code's input_tokens, which is Anthropic's usage
// object as-is: it EXCLUDES cache. The name tells the convention apart, so
// the cache counters are always added (normalizeInputTokens can only infer
// that when cache exceeds input). A span that already has InputTokens (a
// namespaced attribute won) is left alone.
func fillBareInput(s *store.Span, attrs map[string]string) {
	v, ok := firstInt64(attrs, "input_tokens")
	if !ok || s.InputTokens != nil {
		return
	}
	v += store.Deref(s.CacheReadTokens) + store.Deref(s.CacheWriteTokens)
	s.InputTokens = &v
}
