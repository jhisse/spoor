package web

import (
	"errors"
	"html/template"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/jhisse/spoor/internal/store"
)

type detailPageData struct {
	shell
	Trace  store.Trace
	Roots  []*SpanNode
	Prev   *store.Trace
	Next   *store.Trace
	Totals traceTotals

	SelectedID   string
	PrevSpanID   string      // "" at the first span in display order; the ArrowUp target
	NextSpanID   string      // "" at the last span in display order; the ArrowDown target
	SelectedSpan *store.Span // nil only when the trace has zero spans
	spanIO

	TokenScale int64         // the largest span's tokens, the tree's common token-bar scale
	Look       []lookLink    // the "worth a look" line
	Asked      string        // what the person asked; "" when no span recorded it or it is the trace's name
	Trail      []trailStep   // the selected span's ancestors and itself, root first
	At, Of     int           // the selected span's place among all the trace's spans
	Chart      *contextChart // nil for a trace with fewer than two generations
	Delta      *contextDelta // nil unless the selected span is a generation
	Explain    spanExplain   // cost breakdown, health badges, baselines of the selected span

	Tree             []treeRow    // Roots after collapse by interest
	Fold             foldStats    // what Tree hides, and why
	FoldURL          string       // toggles ?fold=0
	Keep             template.URL // what span links carry on: the search, and "&fold=0" while a long trace is shown unfolded
	Unfolded         bool         // ?fold=0
	Search           *traceSearch // nil unless the URL carries span filters
	ListURL          string       // the trace list: the service's, or the search's results
	Read             []readCall   // non-nil when ?view=read
	TreeURL, ReadURL string       // the view switch; empty for a trace with fewer than readMinCalls model calls
}

// traceTotals is the header summary, computed over the spans GetTraceByID
// returned.
type traceTotals struct {
	CostUSD      *float64 // nil when no span has a cost: unknown, not zero
	InputTokens  int64
	OutputTokens *int64 // nil when no span reported output tokens: unknown, not zero
	Tokens       TokenMix
	SpanCount    int
	Models       []string
	CacheUnknown int64 // spans priced without any cache count from the sender
}

// computeTraceTotals sums usage over the tree's roots, so the header and
// the tree's Σ rows agree: a parent that repeats its child's usage is
// counted once (the rule on SpanNode.Tokens).
func computeTraceTotals(spans []store.Span, roots []*SpanNode) traceTotals {
	t := traceTotals{SpanCount: len(spans)}
	var sum float64
	var out int64
	for _, root := range roots {
		t.InputTokens += root.Tokens.Total() - root.Tokens.Output
		t.Tokens = t.Tokens.Add(root.Tokens)
		out += root.Tokens.Output
		if root.Cost != nil {
			sum += *root.Cost
			t.CostUSD = &sum
		}
	}
	models := map[string]struct{}{}
	for _, sp := range spans {
		if sp.CacheUnknown() {
			t.CacheUnknown++
		}
		if sp.OutputTokens != nil {
			t.OutputTokens = &out
		}
		if sp.Kind == store.SpanKindGeneration && sp.Model != nil {
			models[*sp.Model] = struct{}{}
		}
	}
	t.Models = slices.Sorted(maps.Keys(models))
	return t
}

// Detail renders GET /traces/{trace_id}: the trace header and its span tree.
func (h *Handlers) Detail(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	trace, spans, err := h.Store.GetTraceByID(ctx, r.PathValue("trace_id"))
	if errors.Is(err, store.ErrNotFound) {
		h.NotFound(w, r)
		return
	}
	var prev, next *store.Trace
	if err == nil {
		prev, next, err = h.Store.AdjacentTraces(ctx, store.Cursor{StartedAt: trace.StartedAt, ID: trace.ID})
	}
	// Span filters carried over from the list: their spans in this trace are
	// stepped through, and without ?span= the first one is selected.
	filter, want := parseSpanFilter(r.URL.Query()), r.URL.Query().Get("span")
	hits, hitsErr := h.Store.MatchingSpans(ctx, filter, []string{trace.ID})
	if err = errors.Join(err, hitsErr); want == "" && len(hits) > 0 {
		want = hits[0].SpanID
	}
	roots := BuildSpanTree(spans)
	selected, selectedID := selectSpan(spans, roots, want)
	steps := stepNodes(roots)
	data := detailPageData{
		Trace:      trace,
		Roots:      roots,
		Prev:       prev,
		Next:       next,
		Totals:     computeTraceTotals(spans, roots),
		SelectedID: selectedID,
		TokenScale: maxSpanTokens(spans),
		Look:       worthALook(roots, steps),
		Asked:      askedOf(spans, trace.Name),
	}
	data.PrevSpanID, data.NextSpanID = adjacentSpanIDs(roots, selectedID)
	data.Trail, data.At, data.Of = spanTrail(roots, selectedID)
	if filter != (store.SpanFilter{}) {
		data.Search = buildTraceSearch(r.URL, filter, hits, selectedID)
	}
	wantRead := data.treeViews(r.URL, spans)
	if err == nil {
		data.Chart, data.Delta, err = h.contextViews(ctx, trace, spans, selectedID)
	}
	if err == nil && wantRead {
		data.Read, err = h.readView(ctx, trace, spans, data.Keep)
	}
	if data.Chart != nil && data.Read == nil {
		data.Chart.pick(data.Keep)
	}
	if err == nil && selected != nil {
		data.showSpan(selected)
		data.Explain, err = h.explainSpan(ctx, *selected, data.OutputMessages)
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	service := store.Deref(trace.Service)
	if data.ListURL = (shell{}).URL("traces", service); data.Search != nil {
		data.ListURL = "/?" + string(spanQuery(r.URL.Query())[1:])
	}
	page := &pageBuffer{ResponseWriter: w}
	h.renderPage(page, r, detailTmpl, service, &data)
	_, _ = io.WriteString(w, markPage(page.String(), filter.Text))
}

// pageBuffer holds a rendered page back so that markPage can mark it.
type pageBuffer struct {
	http.ResponseWriter
	strings.Builder
}

func (p *pageBuffer) Write(b []byte) (int, error) { return p.Builder.Write(b) }

// showSpan fills what the "span_detail" template reads for sp.
func (d *detailPageData) showSpan(sp *store.Span) {
	d.SelectedSpan, d.spanIO = sp, buildSpanIO(sp)
}

func sessionURL(sessionID string) string {
	return "/sessions?session=" + url.QueryEscape(sessionID)
}

// selectSpan resolves ?span= to a span of the trace. Absent, or naming no
// span here (a link carried over from another trace), it is the first
// failure, else the first root.
func selectSpan(spans []store.Span, roots []*SpanNode, wantID string) (*store.Span, string) {
	if wantID != "" {
		for i := range spans {
			if spans[i].ID == wantID {
				return &spans[i], wantID
			}
		}
	}
	if len(roots) == 0 {
		return nil, ""
	}
	if f := firstFailure(roots); f != nil {
		return &f.Span, f.Span.ID
	}
	return &roots[0].Span, roots[0].Span.ID
}

// adjacentSpanIDs finds selectedID's neighbours in the tree's display order,
// the arrow keys' targets. Either is "" at the first or last span, or when
// nothing is selected.
func adjacentSpanIDs(roots []*SpanNode, selectedID string) (prev, next string) {
	flat := flatten(roots)
	i := slices.IndexFunc(flat, func(n *SpanNode) bool { return n.Span.ID == selectedID })
	if i > 0 {
		prev = flat[i-1].Span.ID
	}
	if i >= 0 && i < len(flat)-1 {
		next = flat[i+1].Span.ID
	}
	return prev, next
}

// treeViews fills the folded tree and the view switch, and says whether
// ?view=read asks for the Read view. Their state is in the URL: ?fold=0
// shows every row.
func (d *detailPageData) treeViews(u *url.URL, spans []store.Span) (wantRead bool) {
	fold := len(spans) > foldMinSpans
	d.Keep = spanQuery(u.Query())
	proto := treeRow{TraceID: d.Trace.ID, SelectedID: d.SelectedID, Scale: d.TokenScale}
	d.FoldURL = withQuery(u, map[string]string{"fold": "0"})
	if u.Query().Get("fold") == "0" { // also opens a short trace's phase rows
		fold, d.Unfolded, proto.Unfolded, d.Keep, d.FoldURL = false, true, true, d.Keep+"&fold=0", withQuery(u, map[string]string{"fold": ""})
	}
	calls := 0
	for _, sp := range spans {
		if sp.Kind == store.SpanKindGeneration {
			calls++
		}
	}
	offered := calls >= readMinCalls
	wantRead = offered && u.Query().Get("view") == "read"
	if proto.Keep = d.Keep; d.Search != nil {
		proto.Hits = d.Search.ids
		stepKeep := d.Keep // the Read view's n and p stay in it
		if wantRead {
			stepKeep += "&view=read"
		}
		d.Search.steps(d.Trace.ID, stepKeep)
	}
	d.Tree, d.Fold = foldTree(spans, d.Roots, proto, fold)
	if offered {
		d.TreeURL, d.ReadURL = withQuery(u, map[string]string{"view": ""}), withQuery(u, map[string]string{"view": "read"})
	}
	return wantRead
}
