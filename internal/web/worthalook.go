package web

import (
	"slices"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// lookLink is one pointer of the "worth a look" line. A pointer says where
// to look, never whether what is there is good or bad.
type lookLink struct {
	Label, SpanID, Name, Value string
}

// firstFailure is the earliest span where an error originated: status
// error with no failing descendant. An error usually propagates to every
// ancestor, and those start earlier — picking by start time alone would
// always land on the root.
func firstFailure(roots []*SpanNode) *SpanNode {
	var first *SpanNode
	for _, n := range flatten(roots) {
		below := slices.ContainsFunc(n.Children, func(c *SpanNode) bool { return c.HasError })
		if n.Span.Status == store.StatusError && !below && (first == nil || n.Span.StartedAt.Before(first.Span.StartedAt)) {
			first = n
		}
	}
	return first
}

var lookQuestions = []struct {
	label  string
	score  func(store.Span) float64
	format func(float64) string
}{
	{"slowest", func(sp store.Span) float64 { return float64(sp.EndedAt.Sub(sp.StartedAt)) },
		func(v float64) string { return fmtDuration(time.Duration(v)) }},
	{"most expensive", func(sp store.Span) float64 { return store.Deref(sp.CostUSD) }, usd4},
	{"most tokens", func(sp store.Span) float64 { return float64(sp.Mix().Total()) },
		func(v float64) string { return fmtCount(int64(v)) + " tokens" }},
}

// worthALook builds the line: first failure, then the slowest, most
// expensive and largest-token step. A question with no data yields no
// link, and with fewer than two steps there is nothing to rank. Questions
// that name the same step are one link with each reason.
func worthALook(roots, steps []*SpanNode) []lookLink {
	var links []lookLink
	if f := firstFailure(roots); f != nil {
		links = append(links, lookLink{Label: "first failure", SpanID: f.Span.ID, Name: f.Span.Name, Value: clipHead([]rune(errorReason(f.Span)), 80)})
	}
	if len(steps) < 2 {
		return links
	}
	at := map[string]int{} // the links of the three questions, by span
	for _, q := range lookQuestions {
		var best *SpanNode
		var bestScore float64
		for _, n := range steps {
			if s := q.score(n.Span); s > bestScore {
				best, bestScore = n, s
			}
		}
		if best == nil {
			continue
		}
		if i, ok := at[best.Span.ID]; ok {
			links[i].Label, links[i].Value = links[i].Label+" · "+q.label, links[i].Value+" · "+q.format(bestScore)
			continue
		}
		at[best.Span.ID] = len(links)
		links = append(links, lookLink{Label: q.label, SpanID: best.Span.ID, Name: best.Span.Name, Value: q.format(bestScore)})
	}
	return links
}
