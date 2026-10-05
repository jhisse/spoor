package web

import (
	"fmt"
	"html/template"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// Sizes of the list and session charts, in px. Every bar on a page shares
// one scale (its largest value), stated next to the chart.
const (
	histogramHeight = 56 // of the 64 its viewBox is high: the rest is room for the error cap
	histogramBars   = 48
	stripHeight     = 96               // the tallest turn's column
	stripBase       = 17 + stripHeight // its foot; the status shape sits in the 17px above the plot
	idleSlot        = 96.0             // room for "⋯ 1 h 12 min ⋯"
	// A fixed threshold: a pause is "long" relative to model time,
	// which is seconds; make it relative to the session's own gaps if
	// machine-driven sessions with minute-long turns show up.
	idleThreshold = time.Minute
)

// scalePx is v's length on a bar that is px long at top. A non-zero value
// never rounds down to an invisible bar.
func scalePx(v, top float64, px int) int {
	if v <= 0 || top <= 0 {
		return 0
	}
	return max(2, int(math.Round(v/top*float64(px))))
}

// summaryMix splits aggregate totals the way Span.Mix splits one span.
func summaryMix(input, output, cacheRead, cacheWrite int64) TokenMix {
	return TokenMix{CacheRead: cacheRead, Fresh: max(input-cacheRead-cacheWrite, 0), CacheWrite: cacheWrite, Output: output}
}

func traceMix(t store.TraceSummary) TokenMix {
	return summaryMix(t.TotalInputTokens, t.TotalOutputTokens, t.TotalCacheReadTokens, t.TotalCacheWriteTokens)
}

// fmtCoarse is fmtDuration for spans of human time: from a minute up, whole
// minutes ("12 min", "1 h 05 min").
func fmtCoarse(d time.Duration) string {
	if d < time.Minute {
		return fmtDuration(d)
	}
	return fmtDuration(d.Round(time.Minute))
}

type listRow struct {
	store.TraceSummary
	Mix   TokenMix
	Ctx   *contextSpark  // its context per model call; nil under two calls or without token counts
	URL   string         // the trace, with the search that listed it
	Hit   *store.SpanHit // the first span the span filters describe; nil without one
	Hits  int            // how many they describe in this trace
	Why   string         // under ?sort=odd, how far from the typical trace of its name
	Asked string         // what the person asked, cut short; "" when no span recorded it or it is the name
}

// MoreModels is how many models the row's model cell does not name.
func (r listRow) MoreModels() int { return max(len(r.Models)-1, 0) }

// MoreHits is how many matching spans the row's line does not show.
func (r listRow) MoreHits() int { return max(r.Hits-1, 0) }

// listScale is what the list's bars are measured against: the page's
// largest token total and, for the context drawings, its largest model
// call. Total is the page's whole mix, for the legend; Marks the letters
// the drawings carry.
type listScale struct {
	Tokens, Context int64
	Total           TokenMix
	Marks           template.HTML
}

// listRows pairs each trace with its hits (grouped by trace) and its model
// calls (steps, by trace), and gives its link the search parameters keep,
// so the trace page opens on a match.
func listRows(traces []store.TraceSummary, hits []store.SpanHit, steps map[string][]genStep, keep template.URL, why, asked map[string]string) ([]listRow, listScale) {
	var sc listScale
	var seen string
	for _, calls := range steps {
		for _, st := range calls {
			sc.Context, seen = max(sc.Context, st.Span.Mix().Total()), seen+st.Mark
		}
	}
	sc.Marks = chartKey(seen, false)
	first, count := map[string]*store.SpanHit{}, map[string]int{}
	for i := range hits {
		if count[hits[i].TraceID]++; count[hits[i].TraceID] == 1 {
			first[hits[i].TraceID] = &hits[i]
		}
	}
	rows := make([]listRow, 0, len(traces))
	for _, t := range traces {
		mix := traceMix(t)
		sc.Tokens = max(sc.Tokens, mix.Total())
		sc.Total = sc.Total.Add(mix)
		row := listRow{TraceSummary: t, Mix: mix, URL: "/traces/" + url.PathEscape(t.ID), Hit: first[t.ID], Hits: count[t.ID], Why: why[t.ID]}
		if row.Asked = asked[t.ID]; row.Asked == t.Name {
			row.Asked = ""
		}
		if spark := buildContextSpark(steps[t.ID], 4, 16); spark.SVG != "" {
			row.Ctx = &spark
		}
		if keep != "" {
			row.URL += "?" + string(keep[1:])
		}
		rows = append(rows, row)
	}
	return rows, sc
}

type histogram struct {
	Bars  []histBar
	Width string
	Max   int64
	Total string    // traces in every bar
	Ticks [5]string // start times at 0, 25, 50, 75 and 100% of the axis
	Heat  *heatmap
	Open  bool // a period or the heat map is set
}

// histBar is one stacked bar, bottom-up ok, unset, error; each Y is the top
// edge of its segment in the 64-high viewBox.
type histBar struct {
	URL, Title                    string
	N                             int64 // traces in the bar
	OKPx, UnsetPx, ErrorPx        int
	OKY, UnsetY, ErrorY, ErrorCap int
}

// buildHistogram turns store buckets into bars whose links set from/to on
// the current URL. Nil when there is nothing to navigate: under two buckets.
func buildHistogram(current *url.URL, buckets []store.TraceBucket) *histogram {
	if len(buckets) < 2 {
		return nil
	}
	h := &histogram{Width: fmtCoarse(buckets[0].End.Sub(buckets[0].Start))}
	var total int64
	for _, b := range buckets {
		h.Max, total = max(h.Max, b.OK+b.Error+b.Unset), total+b.OK+b.Error+b.Unset
	}
	h.Total = commas(total)
	start, day := buckets[0].Start, ""
	for i := range h.Ticks { // the date is printed on the first tick and where it changes
		at, layout := start.Add(buckets[len(buckets)-1].End.Sub(start)*time.Duration(i)/4), "15:04"
		if d := at.Format("Jan 2"); d != day {
			day, layout = d, "Jan 2, 15:04"
		}
		h.Ticks[i] = at.Format(layout)
	}
	for _, b := range buckets {
		bar := histBar{
			URL:     withQuery(current, map[string]string{"from": b.Start.Format(dateTimeLocalLayout), "to": b.End.Format(dateTimeLocalLayout), "cursor": "", "live": ""}),
			N:       b.OK + b.Error + b.Unset,
			Title:   fmt.Sprintf("%s – %s UTC · %d ok · %d error · %d unset", b.Start.Format("2006-01-02 15:04"), b.End.Format("2006-01-02 15:04"), b.OK, b.Error, b.Unset),
			OKPx:    scalePx(float64(b.OK), float64(h.Max), histogramHeight),
			ErrorPx: scalePx(float64(b.Error), float64(h.Max), histogramHeight),
			UnsetPx: scalePx(float64(b.Unset), float64(h.Max), histogramHeight),
		}
		bar.OKY = 64 - bar.OKPx
		bar.UnsetY = bar.OKY - bar.UnsetPx
		bar.ErrorY = bar.UnsetY - bar.ErrorPx
		bar.ErrorCap = bar.ErrorY - 5
		h.Bars = append(h.Bars, bar)
	}
	return h
}

type sessionTurn struct {
	N          int
	Trace      store.TraceSummary
	Input      string
	Output     string
	Mix        TokenMix
	Cumulative *float64 // cost of this turn and every one before it; nil until a turn has a cost
	Idle       string   // the pause before this turn, when it is a long one
}

// turnScale picks one height scale for the whole strip: cost when every
// turn has one (an unknown cost is not a zero-height column), else tokens,
// else duration, which every trace has. label states the choice.
func turnScale(traces []store.TraceSummary) (value func(store.TraceSummary) float64, label func(top float64) string) {
	allCost, allTokens := true, true
	for _, t := range traces {
		allCost = allCost && t.TotalCostUSD != nil
		allTokens = allTokens && traceMix(t).Total() > 0
	}
	switch {
	case allCost:
		return func(t store.TraceSummary) float64 { return *t.TotalCostUSD },
			func(top float64) string { return "cost (max " + usd4(top) + ")" }
	case allTokens:
		return func(t store.TraceSummary) float64 { return float64(traceMix(t).Total()) },
			func(top float64) string { return "tokens (max " + fmtCount(int64(top)) + "), since a turn has no cost" }
	default:
		return func(t store.TraceSummary) float64 { return float64(t.EndedAt.Sub(t.StartedAt)) },
			func(top float64) string {
				return "duration (max " + fmtDuration(time.Duration(top)) + "), since a turn has neither cost nor tokens"
			}
	}
}

// stripColumn draws one turn of the strip, px tall at x: its status shape
// from layout.html's sprite, its token mix (a plain block when it reports
// no tokens) and, when numbered, its number. The whole column links to the
// turn's panel.
func stripColumn(b *strings.Builder, turn sessionTurn, x, width float64, px int, numbered bool) {
	t := turn.Trace
	title := fmt.Sprintf("Turn %d · %s · %s · %s · %s tokens", turn.N, t.Name, t.Status, fmtDuration(t.EndedAt.Sub(t.StartedAt)), fmtCount(turn.Mix.Total()))
	if t.TotalCostUSD != nil {
		title += " · " + usd4(*t.TotalCostUSD)
	}
	if turn.Cumulative != nil {
		title += " · cumulative " + usd4(*turn.Cumulative)
	}
	shape := string(t.Status)
	if t.Status == store.StatusError {
		shape = "err"
	}
	fmt.Fprintf(b, `<a href="#turn-%d"><title>%s</title><use href="#i-%s" x="%.0f" y="2" width="12" height="12" class="st %s"/>`, turn.N, template.HTMLEscapeString(title), shape, x+width/2-6, shape)
	if stackRects(b, floats(turn.Mix.Parts()), float64(turn.Mix.Total()), x, width, stripBase, float64(px)) == stripBase {
		fmt.Fprintf(b, `<rect x="%.1f" y="%d" width="%.1f" height="%d" class="f-rail"/>`, x, stripBase-px, width, px)
	}
	if numbered {
		fmt.Fprintf(b, `<text x="%.0f" y="%d" text-anchor="middle">%d</text>`, x+width/2, stripBase+15, turn.N)
	}
	b.WriteString(`</a>`)
}

// buildReplay lays out a session's traces (chronological) as turns: the
// strip column, the labelled pause before it, and the running cost. Work
// is the sum of trace durations, as opposed to the session's wall clock.
func buildReplay(summary store.SessionSummary, traces []store.TraceSummary) *sessionReplay {
	value, label := turnScale(traces)
	var top, running float64
	for _, t := range traces {
		top = max(top, value(t))
	}
	rp := &sessionReplay{Summary: summary, Wall: fmtCoarse(summary.EndedAt.Sub(summary.StartedAt))}
	var work time.Duration
	var total TokenMix
	var cumulative *float64
	var prevEnd time.Time
	// 28px columns while the session is short enough;
	// past that, narrow ones with every fifth numbered.
	pitch, width, every := 36.0, 28.0, 1
	if len(traces) > 30 {
		pitch, width, every = 16, 12, 5
	}
	var strip strings.Builder
	x := 8.0
	for i, t := range traces {
		turn := sessionTurn{N: i + 1, Trace: t, Mix: traceMix(t)}
		work += t.EndedAt.Sub(t.StartedAt)
		rp.MaxTokens = max(rp.MaxTokens, turn.Mix.Total())
		total = total.Add(turn.Mix)
		if t.TotalCostUSD != nil {
			running += *t.TotalCostUSD
			c := running
			cumulative = &c
		}
		turn.Cumulative = cumulative
		if gap := t.StartedAt.Sub(prevEnd); i > 0 && gap > idleThreshold {
			turn.Idle = fmtCoarse(gap)
			fmt.Fprintf(&strip, `<text x="%.0f" y="%d" text-anchor="middle" class="mute">⋯ %s ⋯</text>`, x+idleSlot/2-4, stripBase-stripHeight/2+4, turn.Idle)
			x += idleSlot
		}
		prevEnd = t.EndedAt
		stripColumn(&strip, turn, x, width, scalePx(value(t), top, stripHeight), turn.N%every == 0)
		x += pitch
		rp.Turns = append(rp.Turns, turn)
	}
	rp.Work = fmtCoarse(work)
	rp.Legend = legend(total, false)
	if len(traces) > 1 {
		rp.StripScale = label(top)
		rp.Strip = template.HTML(fmt.Sprintf(`<svg width="%.0f" height="%d" role="img" aria-label="one column per turn, height = %s">%s<line x1="0" x2="%.0f" y1="%d" y2="%d" class="grid-l"/></svg>`,
			x, stripBase+19, template.HTMLEscapeString(rp.StripScale), strip.String(), x, stripBase+1, stripBase+1)) // #nosec G203 -- numbers, fixed strings, and escaped titles
	}
	return rp
}
