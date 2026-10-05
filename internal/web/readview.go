package web

import (
	"context"
	"html/template"
	"sort"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// readMinCalls is where the Read view is offered: a transcript needs two
// model calls to read as one.
const readMinCalls = 2

// readCall is one model call of the Read view: what the prompt added since
// the previous call of the same context, the answer, and the tool spans
// started before the next call.
type readCall struct {
	genStep
	spanIO
	N     int
	URL   string
	Delta *contextDelta
	Tools []store.Span
}

func (c readCall) Duration() time.Duration { return c.Span.EndedAt.Sub(c.Span.StartedAt) }

// spanIO is a span's input and output as chat messages, or pretty-printed
// when they are not.
type spanIO struct {
	InputMessages  []chatMessage
	InputRaw       string // pretty-printed fallback; empty when InputMessages is used or Input is nil
	OutputMessages []chatMessage
	OutputRaw      string
}

func buildSpanIO(sp *store.Span) (io spanIO) {
	in, out := toolContent(sp)
	if io.InputMessages = parseMessages(in); len(io.InputMessages) == 0 && in != nil {
		io.InputRaw = prettyJSON(*in)
	}
	if io.OutputMessages = parseMessages(out); len(io.OutputMessages) == 0 && out != nil {
		io.OutputRaw = prettyJSON(*out)
	}
	return io
}

func (h *Handlers) readView(ctx context.Context, trace store.Trace, spans []store.Span, keep template.URL) ([]readCall, error) {
	prices, err := h.Store.ListModelPrices(ctx)
	if err != nil {
		return nil, err
	}
	calls := buildRead(trace, spans, prices, keep)
	// The first call of a turn is compared with the session's previous turn, as the span panel does.
	if len(calls) > 0 && calls[0].Prev == nil && trace.SessionID != nil {
		prev, err := h.lastGenerationBefore(ctx, trace, calls[0].ScopeName, store.Deref(calls[0].Span.Model))
		if err != nil {
			return nil, err
		}
		if prev != nil {
			d := buildContextDelta(prev, calls[0].Span)
			calls[0].Delta = &d
		}
	}
	return calls, nil
}

// buildRead lays the trace out as a transcript: the model calls in start
// order, each with what it added to its context, its answer and the tool
// spans started before the next call.
func buildRead(trace store.Trace, spans []store.Span, prices []store.ModelPrice, keep template.URL) []readCall {
	steps := genSteps(spans)
	markSteps(steps, prices)
	var tools []store.Span
	for _, sp := range spans {
		if sp.Kind == store.SpanKindTool {
			tools = append(tools, sp)
		}
	}
	sort.SliceStable(tools, func(i, j int) bool { return tools[i].StartedAt.Before(tools[j].StartedAt) })
	calls := make([]readCall, len(steps))
	for i, st := range steps {
		d := buildContextDelta(st.Prev, st.Span)
		c := readCall{genStep: st, spanIO: buildSpanIO(&st.Span), N: i + 1, URL: spanHref(trace.ID, st.Span.ID, keep), Delta: &d}
		for len(tools) > 0 && (i+1 == len(steps) || tools[0].StartedAt.Before(steps[i+1].Span.StartedAt)) {
			c.Tools, tools = append(c.Tools, tools[0]), tools[1:]
		}
		calls[i] = c
	}
	return calls
}
