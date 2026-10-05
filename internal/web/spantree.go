package web

import (
	"sort"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

type SpanNode struct {
	Span     store.Span
	Children []*SpanNode

	// HasError is true when this span's status is error, or any descendant's:
	// a row can flag "error below" without the viewer expanding every branch.
	HasError bool

	// Orphan marks a root whose ParentSpanID is set but never arrived.
	Orphan bool

	// Offset and Width place the span inside the trace's duration, as fractions
	// of it (the in-row waterfall). Width has a visible floor; it is 0 only
	// when the whole trace has zero duration.
	Offset, Width float64

	// Tokens and Cost are the span's own usage — or, when any descendant
	// carries usage, the sum over descendants instead (TokensSum/CostSum
	// true, rendered as Σ). Never both: instrumentors often repeat a
	// child's usage on its parent, so only the deepest spans that carry
	// usage are counted.
	Tokens             TokenMix
	Cost               *float64
	TokensSum, CostSum bool

	// Wait is the time this span's waiting-phase children took, Denied that
	// one of them ended in a refusal; PhasesOnly says its children are
	// nothing but phases (see notePhases).
	Wait               time.Duration
	Denied, PhasesOnly bool
	tools              int // tool spans at or below
}

// minBarWidth keeps a very short span visible on the in-row duration bar.
const minBarWidth = 0.03

// BuildSpanTree arranges a trace's spans into a forest. A span whose
// ParentSpanID is nil, or names no span in the slice (an orphan), becomes a
// root instead of being dropped. Roots and children are in start order.
func BuildSpanTree(spans []store.Span) []*SpanNode {
	byID := make(map[string]*SpanNode, len(spans))
	var start, end time.Time
	for _, sp := range spans {
		byID[sp.ID] = &SpanNode{Span: sp}
		if start.IsZero() || sp.StartedAt.Before(start) {
			start = sp.StartedAt
		}
		if sp.EndedAt.After(end) {
			end = sp.EndedAt
		}
	}
	if total := end.Sub(start).Seconds(); total > 0 {
		for _, n := range byID {
			n.Width = max(n.Span.EndedAt.Sub(n.Span.StartedAt).Seconds()/total, minBarWidth)
			n.Offset = min(n.Span.StartedAt.Sub(start).Seconds()/total, 1-n.Width)
		}
	}

	var roots []*SpanNode
	for _, sp := range spans {
		node := byID[sp.ID]
		if sp.ParentSpanID == nil {
			roots = append(roots, node)
			continue
		}
		parent, ok := byID[*sp.ParentSpanID]
		if !ok {
			node.Orphan = true
			roots = append(roots, node) // dangling parent_span_id, still shown at root
			continue
		}
		parent.Children = append(parent.Children, node)
	}

	sortByStartedAt(roots)
	for _, node := range byID {
		sortByStartedAt(node.Children)
	}
	for _, root := range roots {
		rollUp(root)
	}
	return roots
}

func sortByStartedAt(nodes []*SpanNode) {
	sort.Slice(nodes, func(i, j int) bool {
		return nodes[i].Span.StartedAt.Before(nodes[j].Span.StartedAt)
	})
}

// rollUp fills HasError, Tokens and Cost bottom-up in one post-order pass;
// usage follows the rule documented on SpanNode.
func rollUp(n *SpanNode) {
	n.HasError = n.Span.Status == store.StatusError
	n.Tokens, n.Cost = n.Span.Mix(), n.Span.CostUSD
	var tokens TokenMix
	var cost float64
	var haveCost bool
	for _, child := range n.Children {
		rollUp(child)
		n.HasError = n.HasError || child.HasError
		tokens = tokens.Add(child.Tokens)
		if child.Cost != nil {
			cost += *child.Cost
			haveCost = true
		}
	}
	notePhases(n)
	if tokens.Total() > 0 {
		n.Tokens, n.TokensSum = tokens, true
	}
	if haveCost {
		n.Cost, n.CostSum = &cost, true
	}
}

// flatten lists roots and everything under them in the order span_row
// renders them (pre-order), which is the order the arrow keys move through.
func flatten(roots []*SpanNode) []*SpanNode {
	var out []*SpanNode
	for _, n := range roots {
		out = append(append(out, n), flatten(n.Children)...)
	}
	return out
}
