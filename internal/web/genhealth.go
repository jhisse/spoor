package web

import (
	"strings"

	"github.com/jhisse/spoor/internal/store"
)

// healthBadge is a deterministic pointer on a generation, never a verdict.
type healthBadge struct{ Label, Title string }

// Lower-cased finish reasons of OpenAI, Anthropic and Gemini.
var finishLabels = map[string]string{
	"length": "truncated", "max_tokens": "truncated",
	"content_filter": "filtered", "refusal": "filtered", "safety": "filtered",
}

// genHealth reads a generation's parsed output. "empty" also needs a
// recorded prompt: without one the SDK was not capturing content at all.
func genHealth(sp store.Span, out []chatMessage) []healthBadge {
	if sp.Kind != store.SpanKindGeneration {
		return nil
	}
	var badges []healthBadge
	empty := len(out) > 0 && sp.Input != nil
	for _, m := range out {
		if label := finishLabels[strings.ToLower(m.FinishReason)]; label != "" {
			badges = append(badges, healthBadge{label, "finish_reason: " + m.FinishReason})
		}
		empty = empty && strings.TrimSpace(m.Content) == "" && len(m.ToolCalls) == 0
	}
	if empty {
		badges = append(badges, healthBadge{"empty", "output with no content and no tool call"})
	}
	return badges
}

// healthGlyph is the tree row's version: the badge labels, or "".
func healthGlyph(sp store.Span) string {
	var labels []string
	for _, b := range genHealth(sp, parseMessages(sp.Output)) {
		labels = append(labels, b.Label)
	}
	return strings.Join(labels, ", ")
}
