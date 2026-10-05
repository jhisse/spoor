package web

import (
	"fmt"
	"html/template"
	"net/url"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// kindClass is the one class per span kind (spoor.css, .k-*): it sets the
// kind's hue for whatever mark carries it — tag, bar, strip mark, swatch.
// Never red: that is reserved for status.
func kindClass(kind store.SpanKind) string { return "k-" + string(kind) }

// kindLetters is the tag of a tree row. Hue alone never carries the kind.
var kindLetters = map[store.SpanKind]string{
	store.SpanKindGeneration: "G", store.SpanKindTool: "T", store.SpanKindRetriever: "R", store.SpanKindEmbedding: "E",
	store.SpanKindReranker: "K", store.SpanKindAgent: "A", store.SpanKindChain: "C", store.SpanKindGeneric: "S",
}

// spanHref is the trace page with one span selected; keep is "&fold=0" or "".
func spanHref(traceID, spanID string, keep template.URL) string {
	return "/traces/" + url.PathEscape(traceID) + "?span=" + url.QueryEscape(spanID) + string(keep)
}

// rowDuration is fmtDuration cut to the tree's time column: from a minute
// up, one unit with a decimal ("2.5 min", "1.2 h").
func rowDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%.1f h", d.Hours())
	case d >= 59950*time.Millisecond:
		return fmt.Sprintf("%.1f min", d.Minutes())
	}
	return fmtDuration(d)
}

// rowCost is a cost cut to the tree's cost column: four decimals under a
// dollar, as everywhere, then fewer as the dollars need the room, so a Σ
// before it still fits. The cell's title has the full figure.
func rowCost(c *float64) string {
	switch {
	case *c < 1:
		return usd4(*c)
	case *c < 10:
		return fmt.Sprintf("$%.3f", *c)
	}
	return fmt.Sprintf("$%.2f", *c)
}

const timeBarWidth = 56

// timeBar is the waterfall inside the row: a mark on a rail, where the span
// sits in the trace's duration. An empty cell when the trace has no duration
// to place it in, so the columns after it stay in place.
func timeBar(n *SpanNode) template.HTML {
	if n.Width == 0 {
		return `<span class="t-pos"></span>`
	}
	return template.HTML(fmt.Sprintf( // #nosec G203 -- numbers, fixed strings and an escaped class
		`<svg class="t-pos" width="%d" height="8" role="img" aria-label="position in trace"><rect y="3" width="%d" height="2" class="f-rail"/><rect x="%.1f" y="1" width="%.1f" height="6" rx="1" class="k %s"/></svg>`,
		timeBarWidth, timeBarWidth, n.Offset*timeBarWidth, n.Width*timeBarWidth, template.HTMLEscapeString(kindClass(n.Span.Kind))))
}

// maxSpanTokens is the common scale of every token bar in the tree: the
// largest single span, so rows compare with each other. A Σ row larger
// than that simply fills its bar.
func maxSpanTokens(spans []store.Span) int64 {
	var m int64
	for _, sp := range spans {
		m = max(m, sp.Mix().Total())
	}
	return m
}
