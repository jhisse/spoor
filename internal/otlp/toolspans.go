package otlp

import (
	"encoding/json"
	"strings"

	"github.com/jhisse/spoor/internal/store"
)

// What a coding agent's spans say beyond the columns. web asks here; it
// never reads an attribute or event name itself.

// toolNameKeys name the tool a tool span ran: OTel GenAI, OpenInference,
// Claude Code. The GenAI convention names such a span "execute_tool {tool}";
// a sender that gives every tool span one fixed name (claude_code.tool) gets
// the tool appended, so the UI can tell Read from Bash.
var toolNameKeys = []string{"gen_ai.tool.name", "tool.name", "tool_name"}

func toolSpanName(name string, kind store.SpanKind, attrs map[string]string) string {
	if tool, ok := firstPresent(attrs, toolNameKeys...); ok && kind == store.SpanKindTool && !strings.Contains(name, tool) {
		return name + " " + tool
	}
	return name
}

// Phase is a part of a tool call that the sender exports as a child span.
type Phase int

const (
	NotAPhase    Phase = iota
	PhaseWaiting       // waiting for permission to run: a person, or an automatic check
	PhaseDenied        // a wait that ended with permission refused: the tool never ran
	PhaseRunning       // the tool's own execution
)

// PhaseOf reads a stored span's phase from Claude Code's span.type.
func PhaseOf(s store.Span) Phase {
	attrs := storedAttrs(s)
	switch t := attrs["span.type"]; {
	case strings.HasSuffix(t, "tool.blocked_on_user") && attrs["decision"] == "reject":
		return PhaseDenied
	case strings.HasSuffix(t, "tool.blocked_on_user"):
		return PhaseWaiting
	case strings.HasSuffix(t, "tool.execution"):
		return PhaseRunning
	}
	return NotAPhase
}

// Purpose is what the sender says a model call was for (the user's own
// turn, a permission check, a title), as sent; "" when it does not say.
func Purpose(s store.Span) string {
	v, _ := firstPresent(storedAttrs(s), "query_source", "query_source_safe")
	return v
}

// ToolIO reads a tool call's content from its tool.output event, for a
// sender that puts it there and not in an attribute: the output or content
// attribute is what came out, every other attribute is what went in.
func ToolIO(s store.Span) (in, out *string) {
	var events []store.SpanEvent
	_ = json.Unmarshal(s.Events, &events) // NULL column: no events
	for _, e := range events {
		if e.Name != "tool.output" {
			continue
		}
		var attrs map[string]any
		_ = json.Unmarshal(e.Attributes, &attrs) // no attributes: nothing to show
		for _, k := range []string{"output", "content"} {
			if v, ok := attrs[k].(string); ok && out == nil {
				out = &v
				delete(attrs, k)
			}
		}
		if len(attrs) > 0 {
			b, _ := json.Marshal(attrs) // decoded from JSON: cannot fail
			v := string(b)
			in = &v
		}
	}
	return in, out
}
