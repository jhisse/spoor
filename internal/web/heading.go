package web

import (
	"slices"

	"github.com/jhisse/spoor/internal/store"
)

// askedOf is what the person asked, read the way the list reads it: the
// newest user message of the earliest model call, else the prompt on the
// earliest agent span. spans are oldest first. It is in one line, whole;
// "" when nothing recorded it or it is the trace's own name.
func askedOf(spans []store.Span, name string) string {
	for _, kind := range []store.SpanKind{store.SpanKindGeneration, store.SpanKindAgent} {
		if i := slices.IndexFunc(spans, func(sp store.Span) bool { return sp.Kind == kind }); i >= 0 {
			if t := askedFull(spans[i].Input); t != "" && t != name {
				return t
			}
		}
	}
	return ""
}

// trailStep is one span on the way from a root to the selected span.
type trailStep struct{ ID, Name string }

// spanTrail is the path from a root to the span id, and its place among all
// the trace's spans in the tree's display order.
func spanTrail(roots []*SpanNode, id string) (trail []trailStep, at, of int) {
	var walk func(n *SpanNode, path []trailStep)
	walk = func(n *SpanNode, path []trailStep) {
		of++
		path = append(path, trailStep{n.Span.ID, n.Span.Name})
		if n.Span.ID == id {
			at, trail = of, slices.Clone(path)
		}
		for _, c := range n.Children {
			walk(c, path)
		}
	}
	for _, r := range roots {
		walk(r, nil)
	}
	return trail, at, of
}
