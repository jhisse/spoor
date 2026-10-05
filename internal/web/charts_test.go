package web

import (
	"html"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func TestScalePx(t *testing.T) {
	for _, c := range []struct {
		v, top float64
		want   int
	}{
		{100, 100, 64},
		{25, 100, 16},
		{0, 100, 0},
		{0.001, 100, 2}, // tiny but real: still visible
		{5, 0, 0},       // nothing on the page has a value
	} {
		if got := scalePx(c.v, c.top, 64); got != c.want {
			t.Errorf("scalePx(%v, %v, 64) = %d, want %d", c.v, c.top, got, c.want)
		}
	}
}

func TestFmtCoarse(t *testing.T) {
	for d, want := range map[time.Duration]string{
		1500 * time.Millisecond:         "1.5 s",
		2 * time.Minute:                 "2 min",
		12*time.Minute + 40*time.Second: "13 min",
		time.Hour:                       "1 h 00 min",
		26*time.Hour + 49*time.Minute:   "26 h 49 min",
	} {
		if got := fmtCoarse(d); got != want {
			t.Errorf("fmtCoarse(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestTokenLegendNamesOnlyPresentBuckets(t *testing.T) {
	got := string(legend(TokenMix{Fresh: 10, Output: 5}, false))
	if !strings.Contains(got, "fresh input") || !strings.Contains(got, "output") || strings.Contains(got, "cache") {
		t.Errorf("legend for an input/output-only mix = %s", got)
	}
}

func turnAt(start time.Time, d time.Duration, cost *float64, in, out int64) store.TraceSummary {
	return store.TraceSummary{
		Trace:            store.Trace{StartedAt: start, EndedAt: start.Add(d), Status: store.StatusOK},
		TotalCostUSD:     cost,
		TotalInputTokens: in, TotalOutputTokens: out,
	}
}

func TestBuildReplayGapsCumulativeAndWorkTime(t *testing.T) {
	base := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	c1, c2, c3 := 0.01, 0.04, 0.02
	traces := []store.TraceSummary{
		turnAt(base, 10*time.Second, &c1, 100, 10),
		turnAt(base.Add(20*time.Second), 30*time.Second, &c2, 400, 40),          // 10s after turn 1: no pause
		turnAt(base.Add(13*time.Minute), 20*time.Second, &c3, 200, 20),          // 12m10s after turn 2
		turnAt(base.Add(13*time.Minute+80*time.Second), time.Second, &c1, 1, 1), // exactly 1min: not over the threshold
	}
	traces[2].Status = store.StatusError
	summary := store.SessionSummary{StartedAt: base, EndedAt: traces[3].EndedAt}

	rp := buildReplay(summary, traces)
	if rp.Work != "1 min" || rp.Wall != "14 min" {
		t.Errorf("work/wall = %q/%q, want 1 min/14 min", rp.Work, rp.Wall)
	}
	if rp.StripScale != "cost (max $0.0400)" {
		t.Errorf("StripScale = %q, want cost scale", rp.StripScale)
	}
	wantIdle := []string{"", "", "12 min", ""}
	wantCum := []float64{0.01, 0.05, 0.07, 0.08}
	for i, turn := range rp.Turns {
		if turn.Idle != wantIdle[i] {
			t.Errorf("turn %d idle = %q, want %q", turn.N, turn.Idle, wantIdle[i])
		}
		if turn.Cumulative == nil || *turn.Cumulative < wantCum[i]-1e-9 || *turn.Cumulative > wantCum[i]+1e-9 {
			t.Errorf("turn %d cumulative = %v, want %v", turn.N, turn.Cumulative, wantCum[i])
		}
	}
	strip := string(rp.Strip)
	if strings.Count(strip, `href="#i-err"`) != 1 || strings.Count(strip, `href="#i-ok"`) != 3 || !strings.Contains(strip, "⋯ 12 min ⋯") {
		t.Errorf("want a cross for the failed turn, a disc for the other three and the pause written out: %s", strip)
	}
	// The most expensive turn fills the strip's 96px; a quarter of its cost is a
	// quarter of the height. 10/11 of each column is its fresh input.
	if !strings.Contains(strip, `height="87.3" class="tk-fresh"`) || !strings.Contains(strip, `height="21.8" class="tk-fresh"`) {
		t.Errorf("want columns 24px and 96px tall, split by token mix: %s", strip)
	}
}

func TestBuildReplayScaleFallsBack(t *testing.T) {
	base := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	cost := 0.01

	// One turn has no cost (unpriced model): the whole strip uses tokens.
	rp := buildReplay(store.SessionSummary{}, []store.TraceSummary{
		turnAt(base, time.Second, &cost, 100, 0),
		turnAt(base.Add(time.Second), time.Second, nil, 300, 0),
	})
	if !strings.HasPrefix(rp.StripScale, "tokens (max 300)") {
		t.Errorf("StripScale = %q, want tokens scale", rp.StripScale)
	}
	if rp.Turns[1].Cumulative == nil || *rp.Turns[1].Cumulative != cost {
		t.Errorf("cumulative after an unpriced turn = %v, want the known %v", rp.Turns[1].Cumulative, cost)
	}

	// A turn with neither cost nor tokens (no generation span): duration,
	// and its column is a plain block rather than a token mix.
	rp = buildReplay(store.SessionSummary{}, []store.TraceSummary{
		turnAt(base, 2*time.Second, nil, 100, 0),
		turnAt(base.Add(2*time.Second), 4*time.Second, nil, 0, 0),
	})
	if !strings.HasPrefix(rp.StripScale, "duration (max 4.0 s)") {
		t.Errorf("StripScale = %q, want duration scale", rp.StripScale)
	}
	if !strings.Contains(string(rp.Strip), `height="96" class="f-rail"`) || rp.Turns[0].Cumulative != nil {
		t.Errorf("tokenless turn column = %s, cumulative = %v", rp.Strip, rp.Turns[0].Cumulative)
	}

	// A single turn has nothing to compare against: no strip.
	if rp = buildReplay(store.SessionSummary{}, []store.TraceSummary{turnAt(base, time.Second, &cost, 1, 1)}); rp.StripScale != "" {
		t.Errorf("single-turn StripScale = %q, want none", rp.StripScale)
	}
}

func TestBuildHistogram(t *testing.T) {
	base := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	current, _ := url.Parse("/?service=s1&status=error&cursor=abc&live=1")
	buckets := []store.TraceBucket{
		{Start: base, End: base.Add(5 * time.Minute), OK: 3, Error: 1},
		{Start: base.Add(5 * time.Minute), End: base.Add(10 * time.Minute), Unset: 2},
	}
	h := buildHistogram(current, buckets)
	if h == nil || len(h.Bars) != 2 || h.Max != 4 || h.Width != "5 min" {
		t.Fatalf("histogram = %+v, want 2 bars, max 4, width 5 min", h)
	}
	u, err := url.Parse(h.Bars[1].URL)
	if err != nil {
		t.Fatalf("bar URL: %v", err)
	}
	q := u.Query()
	if q.Encode() != "from=2026-10-03T04%3A05&service=s1&status=error&to=2026-10-03T04%3A10" {
		t.Errorf("bar URL = %s, want from/to set, other filters kept, cursor and live dropped", h.Bars[1].URL)
	}
	if h.Bars[0].OKPx != 42 || h.Bars[0].ErrorPx != 14 || h.Bars[1].UnsetPx != 28 {
		t.Errorf("bar heights = %+v, want 42/14 and 28 of 56", h.Bars)
	}
}

// seedSizedTrace inserts a trace of the given duration with one generation
// span carrying the given token counts (cache read is part of input).
func seedSizedTrace(t *testing.T, s store.Store, id string, startedAt time.Time, d time.Duration, in, cacheRead, out int64) {
	t.Helper()
	tr := store.Trace{ID: id, Name: id, StartedAt: startedAt, EndedAt: startedAt.Add(d), Status: store.StatusOK, CreatedAt: startedAt}
	if err := s.MergeTrace(t.Context(), tr, true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}
	sp := store.Span{
		TraceID: id, ID: id + "-span", Kind: store.SpanKindGeneration, Name: "chat",
		StartedAt: startedAt, EndedAt: startedAt.Add(d), Status: store.StatusOK, CreatedAt: startedAt,
		InputTokens: &in, OutputTokens: &out,
	}
	if cacheRead > 0 {
		sp.CacheReadTokens = &cacheRead
	}
	if err := s.InsertSpan(t.Context(), sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}
}

func TestListRowsShareOneScale(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now().UTC()
	seedSizedTrace(t, s, "small", now.Add(-time.Minute), time.Second, 150, 0, 50)
	seedSizedTrace(t, s, "big", now, 4*time.Second, 700, 400, 100)

	body := doList(t, h, "").Body.String()
	for _, want := range []string{
		"bar 0–800", // token scale: the larger trace's total
		`<title>cache read: 400</title>`,
		`<title>fresh input: 300</title>`,
		`<rect x="0.00" width="11.00" height="6" class="tk-fresh"><title>fresh input: 150</title>`, // 150 of 800 on 64px
		"cache read</span>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, "cache write") {
		t.Error("legend names cache write though no trace on the page has it")
	}
	// One model call each and no session: no context column, no sessions band.
	if strings.Contains(body, "ctxcol") || strings.Contains(body, "Latest sessions") {
		t.Error("single-call traces without a session must not get a context drawing or a sessions band")
	}
}

func TestListHistogramBarFiltersToItsBucket(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	base := time.Date(2026, 10, 3, 4, 0, 30, 0, time.UTC)
	seedTrace(t, s, "early-trace", base)
	seedTrace(t, s, "late-trace", base.Add(30*time.Minute))

	body := doList(t, h, "").Body.String()
	if !strings.Contains(body, "1 bar = 1 min") {
		t.Fatalf("body missing the histogram's stated bucket width, got: %s", body)
	}
	// The first bar's link: the first minute.
	want := "/?from=2026-10-03T04%3A00&amp;to=2026-10-03T04%3A01"
	if !strings.Contains(body, `href="`+want+`"`) {
		t.Fatalf("body missing first bar link %q, got: %s", want, body)
	}
	u, _ := url.Parse(html.UnescapeString(want))
	filtered := doList(t, h, u.RawQuery).Body.String()
	if !strings.Contains(filtered, "early-trace") || strings.Contains(filtered, "late-trace") {
		t.Errorf("following the first bar should list only early-trace, got: %s", filtered)
	}
	if strings.Contains(filtered, "1 bar =") {
		t.Error("a single-bucket range should not render a histogram")
	}
}

func TestSessionsReplayTurnStrip(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	base := time.Now().UTC().Add(-time.Hour)
	seedSessionTurn(t, s, "turn-a", "conv-1", base)
	seedSessionTurn(t, s, "turn-b", "conv-1", base.Add(10*time.Minute+time.Second)) // 10min after turn-a ended

	body := doSessions(t, h, "session=conv-1").Body.String()
	for _, want := range []string{
		`href="#turn-1"`, `href="#turn-2"`, `id="turn-1"`, `id="turn-2"`,
		"⋯ 10 min ⋯", "⋯ idle 10 min ⋯", // the pause, labelled in the strip and between the panels
		"height = duration (max 1.0 s)",              // seedSessionTurn has no cost and no tokens
		"<dt>Work (sum of turns)</dt><dd>2.0 s</dd>", // work time: two 1s turns
		"<dt>Wall clock</dt><dd>10 min</dd>",         // against the wall clock
		`href="#i-ok"/></svg> ok`,                    // status is a shape, named in the legend
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, "cumulative") {
		t.Error("no turn has a cost: there is no cumulative cost to show")
	}
}

func TestSessionsReplaySingleTurnHasNoStrip(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	seedSessionTurn(t, s, "only", "conv-1", time.Now().UTC())

	body := doSessions(t, h, "session=conv-1").Body.String()
	if strings.Contains(body, "one column per turn") || !strings.Contains(body, `id="turn-1"`) {
		t.Errorf("single-turn session should render its card without a strip, got: %s", body)
	}
}

func TestSessionsListShowsTokenBarAndCumulativeCost(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now().UTC()
	sess := "conv-1"
	cost := 0.02
	for i, id := range []string{"t1", "t2"} {
		at := now.Add(time.Duration(i) * time.Second)
		tr := store.Trace{ID: id, Name: id, SessionID: &sess, StartedAt: at, EndedAt: at.Add(time.Second), Status: store.StatusOK, CreatedAt: at}
		if err := s.MergeTrace(t.Context(), tr, true); err != nil {
			t.Fatalf("MergeTrace: %v", err)
		}
		in, out := int64(90), int64(10)
		sp := store.Span{TraceID: id, ID: id + "-s", Kind: store.SpanKindGeneration, Name: "chat", StartedAt: at, EndedAt: at.Add(time.Second), Status: store.StatusOK, CreatedAt: at, InputTokens: &in, OutputTokens: &out, CostUSD: &cost}
		if err := s.InsertSpan(t.Context(), sp); err != nil {
			t.Fatalf("InsertSpan: %v", err)
		}
	}

	list := doSessions(t, h, "").Body.String()
	for _, want := range []string{"bar 0–200", `aria-label="200 tokens"`, `<title>output: 20</title>`} {
		if !strings.Contains(list, want) {
			t.Errorf("session list missing %q, got: %s", want, list)
		}
	}
	replay := doSessions(t, h, "session=conv-1").Body.String()
	for _, want := range []string{"height = cost (max $0.0200)", "cumulative $0.0200", "cumulative $0.0400"} {
		if !strings.Contains(replay, want) {
			t.Errorf("replay missing %q, got: %s", want, replay)
		}
	}
}
