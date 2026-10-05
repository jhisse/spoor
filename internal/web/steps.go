package web

import (
	"slices"
	"sort"

	"github.com/jhisse/spoor/internal/store"
)

// A step is a span that did work — a model call, a tool call, a retrieval.
// Agent, chain and generic spans are structure and are not steps.
var stepKinds = map[store.SpanKind]bool{
	store.SpanKindGeneration: true,
	store.SpanKindTool:       true,
	store.SpanKindRetriever:  true,
	store.SpanKindEmbedding:  true,
	store.SpanKindReranker:   true,
}

// stepNodes returns the trace's steps in start order.
func stepNodes(roots []*SpanNode) []*SpanNode {
	steps := slices.DeleteFunc(flatten(roots), func(n *SpanNode) bool { return !stepKinds[n.Span.Kind] })
	sort.SliceStable(steps, func(i, j int) bool {
		return steps[i].Span.StartedAt.Before(steps[j].Span.StartedAt)
	})
	return steps
}
