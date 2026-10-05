package web

import (
	"context"
	"slices"

	"github.com/jhisse/spoor/internal/otlp"
	"github.com/jhisse/spoor/internal/store"
)

// notePhases fills Wait and PhasesOnly once n's children are rolled up. A
// coding agent exports the parts of a tool call (waiting for permission,
// running) as child spans: structure of the call, not steps of the trace.
func notePhases(n *SpanNode) {
	n.Wait, n.Denied, n.tools = 0, false, 0
	phases := 0
	for _, c := range n.Children {
		n.tools += c.tools
		if c.Span.Kind != store.SpanKindGeneric {
			continue
		}
		switch p := otlp.PhaseOf(c.Span); p {
		case otlp.PhaseWaiting, otlp.PhaseDenied:
			n.Wait += c.Span.EndedAt.Sub(c.Span.StartedAt)
			n.Denied = n.Denied || p == otlp.PhaseDenied
			phases++
		case otlp.PhaseRunning:
			phases++
		}
	}
	// A tool call below a phase is a sub-agent's work, which is not folded away.
	n.PhasesOnly = phases > 0 && phases == len(n.Children) && n.tools == 0
	if n.Span.Kind == store.SpanKindTool {
		n.tools++
	}
}

// phasesClosed says n's phase rows start collapsed: always, whatever the
// trace's size, unless one is of interest or everything was asked for.
func (f *folder) phasesClosed(n *SpanNode) bool {
	if !n.PhasesOnly || n.HasError || f.proto.Unfolded {
		return false
	}
	for _, c := range n.Children {
		if f.path[c.Span.ID] {
			return false
		}
	}
	return true
}

// rootless says no root span has arrived: every top-level row is a span
// whose parent is missing. The sender is still working, or lost the root.
func rootless(roots []*SpanNode) bool {
	return len(roots) > 0 && !slices.ContainsFunc(roots, func(r *SpanNode) bool { return !r.Orphan })
}

// toolContent is the input and output the panel shows for sp: its own, or
// what its events carry when the columns are empty.
func toolContent(sp *store.Span) (in, out *string) {
	if sp.Input == nil && sp.Output == nil {
		return otlp.ToolIO(*sp)
	}
	return sp.Input, sp.Output
}

// turnPrompts previews each turn that has no model-call body with the
// prompt on an agent span (where a coding agent records what the user typed).
func (h *Handlers) turnPrompts(ctx context.Context, q store.SpanQuery, turns map[string]*sessionTurn) error {
	q.Kind = store.SpanKindAgent
	agents, err := h.Store.QuerySpans(ctx, q)
	for _, a := range agents { // oldest first: each turn keeps its first
		if turn := turns[a.TraceID]; turn != nil && turn.Input == "" && a.Input != nil {
			turn.Input = shortText(*a.Input)
		}
	}
	return err
}
