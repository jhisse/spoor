package web

import (
	"context"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

type listPageData struct {
	shell
	Traces    []listRow
	Scale     listScale
	Legend    template.HTML
	Latest    []latestSession // the band over an unfiltered first page
	Histogram *histogram
	Total     string     // traces the filters match, on every page
	Form      url.Values // the filter form's values, as submitted
	More      bool       // a filter of the "More filters" group is set: it is shown open
	Models    []string   // the model menu: every model a generation span named
	Tools     []string   // the tool menu: every tool span's name
	Chips     []chip
	NoTerm    bool // the search text has nothing to look for
	NextURL   string
	Odd       bool   // the furthest-from-typical order is on
	OddURL    string // flips Odd's current state
	oddView
	Meter     *costMeter // under Live, the spending rate of the last meterWindow
	Live      bool
	ToggleURL string // flips Live's current state; the toggle button's href
	PollURL   string // same live view, cursor-less; hx-trigger re-fetches this so a swapped-in #trace-list keeps polling
	IngestURL string // empty state: where to POST traces
	UIURL     string // empty state: this server, which also serves /sample.pb
}

// spanKinds is the kind menu, in the order of the legend.
var spanKinds = []store.SpanKind{store.SpanKindGeneration, store.SpanKindTool, store.SpanKindAgent, store.SpanKindChain,
	store.SpanKindRetriever, store.SpanKindEmbedding, store.SpanKindReranker, store.SpanKindGeneric}

func (listPageData) Kinds() []store.SpanKind  { return spanKinds }
func (listPageData) Statuses() []store.Status { return statuses }

var statuses = []store.Status{store.StatusOK, store.StatusError, store.StatusUnset}

// Carried are the parameters the filter form passes on unchanged.
func (listPageData) Carried() []string { return []string{"service", "live", "y", "sort"} }

// RangeFields are the form's range filters, one per measure.
func (listPageData) RangeFields() (fields []struct{ Label, Param string }) {
	for i, label := range []string{"Cost (USD)", "Duration (s)", "Tokens, in + out"} {
		fields = append(fields, struct{ Label, Param string }{label, heatScales[measures[i]].param})
	}
	return fields
}

// List renders GET /: the trace list, narrowed by the filters of
// parseListParams, with keyset pagination.
func (h *Handlers) List(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	form := r.URL.Query()
	p := parseListParams(form)
	query := p.query()
	data := listPageData{
		Form:      form,
		More:      p.more(),
		Chips:     p.chips(r.URL),
		NoTerm:    p.Span.Text != "" && !hasTerm(p.Span.Text),
		Live:      p.Live,
		Odd:       p.Odd,
		OddURL:    toggleURL(r.URL, "sort", "odd", !p.Odd),
		ToggleURL: toggleURL(r.URL, "live", "1", !p.Live),
		PollURL:   toggleURL(r.URL, "live", "1", true),
		IngestURL: h.ingestURL(r),
		UIURL:     "http://" + r.Host,
	}

	summaries, next, err := h.Store.QueryTraces(ctx, query)
	summaries, next, data.oddView = p.page(summaries, next)
	if err == nil && p.Live {
		data.Meter, err = h.meter(ctx, query)
	}
	if err == nil { // the histogram covers the whole filtered set, not just this page
		var total int64
		data.Histogram, total, err = h.overview(ctx, r.URL, query, p.Heat)
		data.Total = commas(total)
	}
	poll := r.Header.Get("HX-Target") == "trace-results" // the live poll keeps only the results, not the form
	if err == nil && !poll {
		data.Models, data.Tools, err = h.menus(ctx)
	}
	ids := make([]string, len(summaries))
	for i, t := range summaries {
		ids[i] = t.ID
	}
	var hits []store.SpanHit
	var asked map[string]string
	if err == nil {
		hits, asked, err = h.rowText(ctx, p.Span, ids)
	}
	var steps map[string][]genStep
	switch {
	case err != nil:
	case len(summaries) == 0:
		err = h.countWithout(ctx, data.Chips)
	default:
		// The band is the first page's with no filter but the service: "lately" is not a search result.
		steps, data.Latest, err = h.contexts(ctx, summaries, ids, p.Cursor == nil && !p.Odd && len(data.Chips) == min(1, len(p.Service)))
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	data.Traces, data.Scale = listRows(summaries, hits, steps, spanQuery(form), data.Why, asked)
	data.Legend = legend(data.Scale.Total, false)
	data.NextURL = buildNextURL(r.URL, next)
	if r.Header.Get("HX-Target") == "trace-list" { // a submitted form names every field, set or not
		w.Header().Set("HX-Push-Url", withQuery(r.URL, nil))
	}
	h.renderPage(w, r, listTmpl, p.Service, &data)
}

func (h *Handlers) menus(ctx context.Context) (models, tools []string, err error) {
	if models, err = h.Store.ListGenerationModels(ctx); err != nil {
		return nil, nil, err
	}
	tools, err = h.Store.ListSpanNames(ctx, store.SpanKindTool)
	return models, tools, err
}

// costMeter is the live list's taximeter: what the traces that started in
// the last meterWindow cost, as an hourly rate. Uncosted traces are named,
// not priced.
type costMeter struct {
	Rate             string // "$0.4100/h"; "" with no trace in the window
	Traces, Uncosted int64
}

const meterWindow = 10 * time.Minute

func (h *Handlers) meter(ctx context.Context, q store.TraceQuery) (*costMeter, error) {
	w, err := h.Store.CostSince(ctx, q, time.Now().Add(-meterWindow))
	if err != nil {
		return nil, err
	}
	m := &costMeter{Traces: w.Traces, Uncosted: w.Uncosted}
	if w.Traces > w.Uncosted {
		m.Rate = usd4(w.USD*float64(time.Hour/meterWindow)) + "/h"
	}
	return m, nil
}

// rowText reads the text beside the rows: the spans the span filters
// describe, and what each trace was asked.
func (h *Handlers) rowText(ctx context.Context, f store.SpanFilter, ids []string) ([]store.SpanHit, map[string]string, error) {
	hits, err := h.Store.MatchingSpans(ctx, f, ids)
	if err != nil {
		return nil, nil, err
	}
	asked, err := h.subjects(ctx, ids, askedText)
	return hits, asked, err
}

// latestSession is one card of the list's band: a session's totals and its
// context per model call.
type latestSession struct {
	sessionRow
	Ctx contextSpark
}

const latestSessions = 3

// contexts reads what the list draws beside its totals: each listed trace's
// model calls as marked steps (traces with fewer than two have none), read
// the way the trace page reads them, and, when latest is set, the sessions
// the page's newest traces belong to. The spans come from an index, without
// bodies, so the cost follows the page and those sessions, not the database.
func (h *Handlers) contexts(ctx context.Context, traces []store.TraceSummary, ids []string, latest bool) (map[string][]genStep, []latestSession, error) {
	prices, err := h.Store.ListModelPrices(ctx)
	if err != nil {
		return nil, nil, err
	}
	spans, err := h.Store.QuerySpans(ctx, store.SpanQuery{TraceIDs: ids, Usage: true})
	if err != nil {
		return nil, nil, err
	}
	byTrace := map[string][]store.Span{}
	for _, sp := range spans {
		byTrace[sp.TraceID] = append(byTrace[sp.TraceID], sp)
	}
	steps := map[string][]genStep{}
	for id, sps := range byTrace {
		if st := genSteps(sps); len(st) >= 2 {
			markSteps(st, prices)
			steps[id] = st
		}
	}
	var sessions []latestSession
	seen := map[string]bool{}
	for _, t := range traces {
		if !latest || len(sessions) == latestSessions {
			break
		}
		if t.SessionID == nil || *t.SessionID == "" || seen[*t.SessionID] {
			continue
		}
		seen[*t.SessionID] = true
		card, err := h.latestCard(ctx, *t.SessionID, prices)
		if err != nil {
			return nil, nil, err
		}
		if card != nil {
			sessions = append(sessions, *card)
		}
	}
	return steps, sessions, nil
}

// latestCard is one session of the band; nil when retention removed the
// session since the page was read.
func (h *Handlers) latestCard(ctx context.Context, sessionID string, prices []store.ModelPrice) (*latestSession, error) {
	s, _, err := h.Store.GetSession(ctx, sessionID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	gens, err := h.Store.QuerySpans(ctx, store.SpanQuery{SessionID: s.ID, Kind: store.SpanKindGeneration, Usage: true})
	if err != nil {
		return nil, err
	}
	subject, err := h.subjects(ctx, []string{s.FirstTraceID}, subjectText)
	if err != nil {
		return nil, err
	}
	calls, _ := sessionSteps(nil, gens, prices)
	return &latestSession{sessionRow{s, summaryMix(s.TotalInputTokens, s.TotalOutputTokens, s.TotalCacheReadTokens, s.TotalCacheWriteTokens), subject[s.FirstTraceID]},
		buildContextSpark(calls, 7, sparkHeight)}, nil
}

// countWithout fills each chip's Without: what dropping that one filter
// would list. It runs only when nothing matched, to say which filter to drop.
func (h *Handlers) countWithout(ctx context.Context, chips []chip) error {
	for i := range chips {
		u, err := url.Parse(chips[i].URL)
		if err != nil {
			return err
		}
		buckets, err := h.Store.TraceHistogram(ctx, parseListParams(u.Query()).query(), 1)
		if err != nil {
			return err
		}
		var n int64
		for _, b := range buckets {
			n += b.OK + b.Error + b.Unset
		}
		chips[i].Without = commas(n)
	}
	return nil
}
