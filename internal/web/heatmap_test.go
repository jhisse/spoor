package web

import (
	"html"
	"math"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// A bound has to survive the trip through a link unchanged, or a cell's
// link would list a different set than the cell counted.
func TestHeatScaleBoundsRoundTripAndAscend(t *testing.T) {
	for m, sc := range heatScales {
		if sc.bounds[0] != 0 || !math.IsInf(sc.bounds[len(sc.bounds)-1], 1) {
			t.Errorf("%s bounds should run from 0 to +Inf, got %v", m, sc.bounds)
		}
		for i, b := range sc.bounds {
			r := parseListParams(url.Values{sc.param + "_min": {fmtBound(b)}, sc.param + "_max": {fmtBound(b)}}).Ranges[m]
			if b > 0 && (r.Min != b || r.Max != b) {
				t.Errorf("%s bound %v came back from a link as %+v", m, b, r)
			}
			if i > 0 && b <= sc.bounds[i-1] {
				t.Errorf("%s bounds not ascending at %v", m, b)
			}
		}
	}
	if got := fmtBound(0.0003); got != "0.0003" {
		t.Errorf("fmtBound(0.0003) = %q", got)
	}
}

func TestHeatScaleRangeText(t *testing.T) {
	for _, c := range []struct {
		m      store.Measure
		lo, hi float64
		want   string
	}{
		{store.MeasureCost, 0.03, 0.1, "$0.03 – $0.1"},
		{store.MeasureCost, 0, 0.0001, "$0 – $0.0001"},
		{store.MeasureCost, 10, math.Inf(1), "$10 or more"},
		{store.MeasureDuration, 300, 0, "300s or more"}, // a hand-written ?min= without ?max=
		{store.MeasureDuration, 0.1, 0.3, "0.1s – 0.3s"},
		{store.MeasureTokens, 3000, 10000, "3.0k – 10.0k"},
	} {
		if got := heatScales[c.m].rangeText(c.lo, c.hi); got != c.want {
			t.Errorf("rangeText(%s, %v, %v) = %q, want %q", c.m, c.lo, c.hi, got, c.want)
		}
	}
}

func TestParseHeatMeasureIsLenient(t *testing.T) {
	if p := parseListParams(url.Values{"y": {"duration"}, "dur_min": {"3"}, "dur_max": {"abc"}}); p.Heat != store.MeasureDuration || p.Ranges[store.MeasureDuration] != (store.Range{Min: 3}) {
		t.Errorf("parseListParams = %+v, want the duration heat map and a range from 3, no max", p)
	}
	if p := parseListParams(url.Values{"y": {"weight"}}); p.Heat != "" || len(p.Ranges) != 0 {
		t.Errorf("an unknown measure draws no heat map, got %+v", p)
	}
}

func TestBuildHeatmap(t *testing.T) {
	base := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	current, _ := url.Parse("/?service=s1&cursor=abc&live=1&y=duration&cost_min=9")
	buckets := []store.TraceBucket{
		{Start: base, End: base.Add(5 * time.Minute), OK: 5, Error: 1},
		{Start: base.Add(5 * time.Minute), End: base.Add(10 * time.Minute), Unset: 2},
	}
	// Duration bounds: 0, 0.1, 0.3, 1, 3, 10, ... so Y=5 is 3s–10s and Y=11 is 3000s or more.
	hm := buildHeatmap(current, store.MeasureDuration, buckets, []store.HeatCell{
		{X: 0, Y: 5, Count: 4, TraceID: "t1"},
		{X: 0, Y: 11, Count: 1, TraceID: "slow"},
		{X: 1, Y: 6, Count: 2, TraceID: "t2"},
		{X: 7, Y: 6, Count: 1, TraceID: "late"}, // ingested after the histogram was read
	})
	// 8 traces in the histogram, 7 in a kept cell: 1 missing. Rows run from 3000s down to 3s.
	if got, want := []int64{int64(hm.Cols), hm.Max, hm.Missing}, []int64{2, 4, 1}; !reflect.DeepEqual(got, want) {
		t.Errorf("cols, max, missing = %v, want %v", got, want)
	}
	if want := []string{"3000s", "1000s", "300s", "100s", "30s", "10s", "3s"}; !reflect.DeepEqual(hm.Labels, want) {
		t.Errorf("Labels = %v, want %v", hm.Labels, want)
	}
	// A filter link keeps the other filters and drops cursor and live; the
	// single-trace cell links to its trace and is step 1; the rest are 1 + ⌈5 × count/max⌉.
	wantCells := []heatCell{
		{X: 0, Y: 6, Step: 6, URL: "/?cost_min=9&dur_max=10&dur_min=3&from=2026-10-03T04%3A00&service=s1&to=2026-10-03T04%3A05&y=duration",
			Title: "2026-10-03 04:00 – 2026-10-03 04:05 UTC · duration 3s – 10s · traces: 4"},
		{X: 0, Y: 0, Step: 1, Single: true, URL: "/traces/slow",
			Title: "2026-10-03 04:00 – 2026-10-03 04:05 UTC · duration 3000s or more · traces: 1"},
		{X: 1, Y: 5, Step: 4, URL: "/?cost_min=9&dur_max=30&dur_min=10&from=2026-10-03T04%3A05&service=s1&to=2026-10-03T04%3A10&y=duration",
			Title: "2026-10-03 04:05 – 2026-10-03 04:10 UTC · duration 10s – 30s · traces: 2"},
	}
	if !reflect.DeepEqual(hm.Cells, wantCells) {
		t.Errorf("Cells = %+v, want %+v", hm.Cells, wantCells)
	}
	wantSwitch := []heatCell{{Title: "off", URL: "/?cost_min=9&live=1&service=s1"}, {Title: "cost", URL: "/?cost_min=9&live=1&service=s1&y=cost"},
		{Title: "duration", URL: "/?cost_min=9&live=1&service=s1&y=duration", Single: true}, {Title: "tokens", URL: "/?cost_min=9&live=1&service=s1&y=tokens"}}
	if !reflect.DeepEqual(hm.Switch, wantSwitch) {
		t.Errorf("Switch = %+v, want %+v (a range is a filter and stays)", hm.Switch, wantSwitch)
	}
	if off := buildHeatmap(current, "", buckets, nil); len(off.Switch) != 4 || !off.Switch[0].Single || off.Cells != nil || off.Missing != 0 {
		t.Errorf("heat map switched off = %+v, want only the switch", off)
	}

	empty := buildHeatmap(current, store.MeasureCost, buckets, nil)
	if empty.Missing != 8 || len(empty.Cells) != 0 || len(empty.Labels) != 0 {
		t.Errorf("heat map without cells = %+v, want every trace counted as missing and no rows", empty)
	}
}

func TestListHeatmapCellFiltersToPeriodAndRange(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	base := time.Date(2026, 10, 3, 4, 0, 30, 0, time.UTC)
	seedSizedTrace(t, s, "quick-one", base, 4*time.Second, 100, 0, 10)
	seedSizedTrace(t, s, "quick-two", base.Add(5*time.Second), 5*time.Second, 100, 0, 10)
	seedSizedTrace(t, s, "slow-early", base.Add(10*time.Second), 4000*time.Second, 100, 0, 10)
	seedSizedTrace(t, s, "quick-late", base.Add(30*time.Minute), 4*time.Second, 100, 0, 10)

	body := doList(t, h, "y=duration").Body.String()
	for _, want := range []string{
		`aria-current="true">duration</a>`,
		`viewBox="0 0 31 7"`, // 31 one-minute buckets; rows from 3s up to 3000s
		`<div>3000s</div><div>1000s</div><div>300s</div><div>100s</div><div>30s</div><div>10s</div><div>3s</div>`,
		`more traces, up to 2`,
		`<a href="/traces/slow-early" tabindex="-1"><rect x="0.04" y="0.06" width=".92" height=".88" class="ht-1"`,
		`duration 3s – 10s · traces: 2</title>`,
		`<input type="hidden" name="y" value="duration">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, "not shown") || strings.Contains(body, `class="chip x"`) {
		t.Error("every trace has a duration and no range is set: nothing to report")
	}

	follow := func(link string) string {
		t.Helper()
		if !strings.Contains(body, `<a href="`+link+`" tabindex="-1"><rect`) {
			t.Fatalf("body missing a cell linking to %q, got: %s", link, body)
		}
		u, _ := url.Parse(html.UnescapeString(link))
		return doList(t, h, u.RawQuery).Body.String()
	}
	filtered := follow("/?dur_max=10&amp;dur_min=3&amp;from=2026-10-03T04%3A00&amp;to=2026-10-03T04%3A01&amp;y=duration")
	for _, want := range []string{
		"quick-one", "quick-two",
		`hx-get="/?from=2026-10-03T04%3A00&amp;to=2026-10-03T04%3A01&amp;y=duration" title="Remove this filter">duration 3s – 10s<`, // the chip keeps the rest
		`name="dur_min" min="0" step="any" placeholder="from" aria-label="Duration (s), from" value="3">`,
		`<details class="disc hint" open>`, // a filter of the group is set: it is shown
	} {
		if !strings.Contains(filtered, want) {
			t.Errorf("filtered body missing %q, got: %s", want, filtered)
		}
	}
	if strings.Contains(filtered, "slow-early") || strings.Contains(filtered, "quick-late") {
		t.Errorf("following the cell should list only its two traces, got: %s", filtered)
	}

	// The top row has no upper bound: its link carries +Inf and still lists the trace.
	top := doList(t, h, "y=duration&dur_min=3000&dur_max=%2BInf").Body.String()
	if !strings.Contains(top, "slow-early") || strings.Contains(top, "quick-one") || !strings.Contains(top, "duration 3000s or more<") {
		t.Errorf("min=3000&max=+Inf should list only slow-early, got: %s", top)
	}
	if none := doList(t, h, "y=duration&dur_min=9000").Body.String(); !strings.Contains(none, "No trace matches this filter") || !strings.Contains(none, "<b>4</b> traces without") {
		t.Errorf("a range that matches nothing should say so, not show onboarding, got: %s", none)
	}
}

// A trace without a priced span has no cost, and the page says how many
// were left out instead of drawing them at zero. Without ?y= no heat map is
// drawn at all: only its switch.
func TestListHeatmapCountsUnknownCost(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	base := time.Date(2026, 10, 3, 4, 0, 30, 0, time.UTC)
	seedSizedTrace(t, s, "unpriced-one", base, time.Second, 100, 0, 10)
	seedSizedTrace(t, s, "unpriced-two", base.Add(30*time.Minute), time.Second, 100, 0, 10)

	body := doList(t, h, "y=cost").Body.String()
	if !strings.Contains(body, `aria-current="true">cost</a>`) || !strings.Contains(body, "2 with unknown cost not shown") {
		t.Errorf("body should name cost and the 2 traces left out, got: %s", body)
	}
	if strings.Contains(body, `class="heat"`) {
		t.Error("no trace has a cost: there is no grid to draw")
	}
	if off := doList(t, h, "").Body.String(); !strings.Contains(off, `aria-current="true">off</a>`) || strings.Contains(off, "not shown") {
		t.Errorf("without ?y= the heat map is off and reports nothing, got: %s", off)
	}
	tokens := doList(t, h, "y=tokens").Body.String()
	if !strings.Contains(tokens, `<div>100</div>`) || !strings.Contains(tokens, "tokens 100 – 300 · traces: 1") {
		t.Errorf("the tokens heat map should put both 110-token traces in the 100 row, got: %s", tokens)
	}
}

// A trace stored after the histogram was read can fall outside its columns,
// on either side: such a cell is left out, not indexed.
func TestBuildHeatmapSkipsCellsOutsideTheBuckets(t *testing.T) {
	start := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	buckets := []store.TraceBucket{{Start: start, End: start.Add(time.Minute), OK: 1}, {Start: start.Add(time.Minute), End: start.Add(2 * time.Minute)}}
	u, _ := url.Parse("/?y=cost")
	hm := buildHeatmap(u, store.MeasureCost, buckets, []store.HeatCell{{X: -1, Y: 3, Count: 1}, {X: 0, Y: 3, Count: 1}, {X: 2, Y: 3, Count: 1}})
	if len(hm.Cells) != 1 || hm.Cells[0].X != 0 {
		t.Errorf("cells = %+v, want only the one inside the buckets", hm.Cells)
	}
}

// Bars and cells are narrow and out of the tab order, so "As a list" holds
// the same links as lines of their own: only the periods that have traces,
// and every cell.
func TestListChartsHaveATextAlternative(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	base := time.Date(2026, 10, 3, 4, 0, 30, 0, time.UTC)
	seedSizedTrace(t, s, "quick-one", base, 4*time.Second, 100, 0, 10)
	seedSizedTrace(t, s, "quick-two", base.Add(5*time.Second), 5*time.Second, 100, 0, 10)
	seedSizedTrace(t, s, "quick-late", base.Add(30*time.Minute), 4*time.Second, 100, 0, 10)

	body := doList(t, h, "y=duration").Body.String()
	for _, want := range []string{
		`<h3>As a list</h3>`,
		`<li><a class="link" href="/?from=2026-10-03T04%3A00&amp;to=2026-10-03T04%3A01&amp;y=duration">2026-10-03 04:00 – 2026-10-03 04:01 UTC`,
		`<li><a class="link" href="/?dur_max=10&amp;dur_min=3&amp;from=2026-10-03T04%3A00&amp;to=2026-10-03T04%3A01&amp;y=duration">`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if n := strings.Count(body, `<li><a class="link" href="/?from=`); n != 2 {
		t.Errorf("%d periods listed, want the 2 that have traces (not the 29 empty ones)", n)
	}
}
