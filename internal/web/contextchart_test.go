package web

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestDetectCacheBreakArithmetic(t *testing.T) {
	prev := TokenMix{CacheRead: 98_000, CacheWrite: 2_000}
	cur := TokenMix{CacheWrite: 100_000}
	b, ok := detectCacheBreak(prev, cur)
	if !ok {
		t.Fatal("a step that reads nothing and rewrites everything after a 98% cached step is a break")
	}
	// actual: 100k written at 1.25×. cached: 98k read at 0.1× + 2k written at 1.25×.
	if !near(b.PrevRatio, 0.98) || b.Ratio != 0 || !near(b.ActualIn, 125_000) || !near(b.CachedIn, 9_800+2_500) {
		t.Errorf("break = %+v", b)
	}
	model := "m"
	got, extra := b.sentence(3, []store.ModelPrice{{ModelPattern: "^m$", InputPricePerToken: 0.000003}}, &model)
	for _, want := range []string{"Step 3", "0%", "98%", "$0.3750", "$0.0369", "(estimated)"} {
		if !strings.Contains(got, want) {
			t.Errorf("sentence %q missing %q", got, want)
		}
	}
	if !near(extra, 0.375-0.0369) {
		t.Errorf("extra = %v, want the difference of the two amounts, 0.3381", extra)
	}
	if got, extra := b.sentence(3, nil, &model); strings.Contains(got, "$") || !strings.Contains(got, "No price") || extra != 0 {
		t.Errorf("unpriced sentence must not state dollars: %q, extra %v", got, extra)
	}
}

func TestDetectCacheBreakNotABreak(t *testing.T) {
	cases := map[string][2]TokenMix{
		"steady cached loop":            {{CacheRead: 98_000, CacheWrite: 2_000}, {CacheRead: 100_000, CacheWrite: 1_500}},
		"predecessor had no cache":      {{Fresh: 5_000}, {CacheWrite: 6_000}},
		"no cache columns on either":    {{Fresh: 5_000, Output: 10}, {Fresh: 6_000, Output: 10}},
		"partial miss, still most read": {{CacheRead: 90_000, CacheWrite: 10_000}, {CacheRead: 60_000, CacheWrite: 50_000}},
		// Each of the next three fails exactly one condition.
		"hit rate fell by less than half": {{CacheRead: 90_000, CacheWrite: 10_000}, {CacheRead: 55_000, CacheWrite: 60_000}},
		"predecessor read under half":     {{CacheRead: 30_000, Fresh: 70_000}, {CacheWrite: 100_000}},
		"wrote less than it read":         {{CacheRead: 95_000, CacheWrite: 5_000}, {CacheRead: 40_000, CacheWrite: 30_000, Fresh: 30_000}},
	}
	for name, tc := range cases {
		if _, ok := detectCacheBreak(tc[0], tc[1]); ok {
			t.Errorf("%s: reported as a cache break", name)
		}
	}
}

func TestStackRectsGeometry(t *testing.T) {
	var b strings.Builder
	top := stackRects(&b, [4]float64{50, 25, 0, 25}, 200, 10, 20, 110, 100)
	got := b.String()
	if top != 60 {
		t.Errorf("top = %v, want 60 (half of a 100px plot above base 110)", top)
	}
	if strings.Count(got, "<rect") != 3 {
		t.Errorf("want one rect per non-zero bucket, got: %s", got)
	}
	// cache read sits at the base, output on top
	read := `y="85.0" width="20.0" height="25.0" fill="url(#hatch)"`
	out := `y="60.0" width="20.0" height="12.5" class="tk-out"`
	if !strings.Contains(got, read) || !strings.Contains(got, out) {
		t.Errorf("unexpected geometry: %s", got)
	}
}

func TestBarLayoutShrinksWithSteps(t *testing.T) {
	if p, gap := barLayout(2, chartRight); p-gap != 22 {
		t.Errorf("few steps: bar = %v, want the 22px cap", p-gap)
	}
	if p, _ := barLayout(1000, chartRight); p != minPitch {
		t.Errorf("many steps: pitch = %v, want the %vpx floor", p, minPitch)
	}
	// In between, the steps and what stands beside them fill chartFill exactly.
	if p, _ := barLayout(100, chartRight+pauseSlot); p*100+chartRight+pauseSlot != chartFill {
		t.Errorf("100 steps: pitch = %v, want the chart %v wide", p, chartFill)
	}
}

// A chart is told apart by width: a short one keeps its natural size, a long
// one fills its container, and neither carries an inline style.
func TestChartSVGScalesWithItsContainer(t *testing.T) {
	spans := cachedLoop("t", time.Now())
	short := string(buildContextChart(genSteps(spans), sonnet, "").Tokens)
	if !strings.Contains(short, `<svg width="148" height="150"`) || strings.Contains(short, "fluid") {
		t.Errorf("3 steps: want a 148px plot (3×28 + 64): %s", short)
	}
	var many []store.Span
	for i := range 40 {
		sp := spans[i%3]
		sp.ID = fmt.Sprint("s", i)
		many = append(many, sp)
	}
	long := string(buildContextChart(genSteps(many), sonnet, "").Tokens)
	if !strings.Contains(long, `class="fluid" width="100%"`) || !strings.Contains(long, `viewBox="0 0 1160 150"`) {
		t.Errorf("40 steps: want a plot that stretches over a 1160-wide viewBox: %s", long[:300])
	}
	if strings.Contains(short+long, "style=") {
		t.Errorf("chart carries an inline style")
	}
	// The axis states the scale: 0, half and the tallest step (92,100 tokens).
	for _, want := range []string{">0</text>", ">46.0k</text>", ">92.1k</text>"} {
		if !strings.Contains(short, want) {
			t.Errorf("axis missing %s: %s", want, short)
		}
	}
}

// cachedLoop is an agent loop the way a caching SDK reports it: steady
// cache reads, then a context that shrinks and is rewritten in full.
func cachedLoop(traceID string, t0 time.Time) []store.Span {
	mk := func(i int, in, read, write, out int64, cost float64) store.Span {
		sp := gen(traceID, fmt.Sprintf("g%d", i), nil, t0.Add(time.Duration(i)*time.Second), nil)
		sp.Model = ptr("claude-sonnet-5")
		sp.InputTokens, sp.OutputTokens = &in, &out
		sp.CacheReadTokens, sp.CacheWriteTokens = &read, &write
		sp.CostUSD = &cost
		return sp
	}
	return []store.Span{
		mk(1, 90_000, 88_000, 2_000, 100, 0.01),
		mk(2, 92_000, 90_000, 2_000, 100, 0.02),
		mk(3, 40_000, 0, 40_000, 100, 0.15),
	}
}

var sonnet = []store.ModelPrice{{ModelPattern: "claude-sonnet-5", InputPricePerToken: 0.000003, OutputPricePerToken: 0.000015}}

func TestBuildContextChartWithCacheAndCost(t *testing.T) {
	steps := genSteps(cachedLoop("t", time.Now()))
	c := buildContextChart(steps, sonnet, "g2")
	tokens := string(c.Tokens)
	if strings.Count(tokens, "<a href=") != 3 {
		t.Errorf("want one linked bar per generation: %s", tokens)
	}
	if !strings.Contains(tokens, ">C<") || strings.Contains(tokens, "Q<") {
		t.Errorf("step 3 is a smaller, rewritten context: want the shrink marker and no cache break")
	}
	if !strings.Contains(tokens, "<polyline") || !strings.Contains(tokens, "$0.1800") {
		t.Errorf("want the cumulative cost line with its final value labelled: %s", tokens)
	}
	if c.Dollars == "" || len(c.Notes) != 0 {
		t.Errorf("priced, costed, cached steps: want the dollar band and no notes, got notes %v", c.Notes)
	}
	// In dollars the rewritten step is the tallest: 40k × $3/M × 1.25.
	if !strings.Contains(string(c.Dollars), ">$0.1515</text>") {
		t.Errorf("dollar band scale: %s", c.Dollars)
	}
	if len(c.Breaks) != 0 {
		t.Errorf("a new, smaller context has no cache to lose: Breaks = %v", c.Breaks)
	}
}

// A subagent's first call (26k, all cache write) right after the parent's
// 92k call is a new context, not a lost cache. The same rewrite on a prompt
// that kept growing is a break.
func TestDetectCacheBreakNeedsAContinuingContext(t *testing.T) {
	prev := TokenMix{CacheRead: 89_505, CacheWrite: 2_331, Fresh: 2}
	if _, ok := detectCacheBreak(prev, TokenMix{CacheWrite: 26_217, Fresh: 2}); ok {
		t.Errorf("a smaller prompt must not be reported as a cache break")
	}
	b, ok := detectCacheBreak(prev, TokenMix{CacheWrite: 95_000, Fresh: 2})
	if !ok {
		t.Fatalf("a growing prompt that reads nothing from cache is a break")
	}
	if got, extra := b.sentence(3, sonnet, ptr("claude-sonnet-5")); !strings.Contains(got, "Step 3") || !strings.Contains(got, "(estimated)") || extra <= 0 {
		t.Errorf("sentence = %q, extra %v", got, extra)
	}
}

func TestBuildContextChartDegradesWithoutCacheCostOrPrice(t *testing.T) {
	spans := cachedLoop("t", time.Now())
	for i := range spans {
		spans[i].CacheReadTokens, spans[i].CacheWriteTokens, spans[i].CostUSD = nil, nil, nil
		spans[i].Model = ptr("unknown-model")
	}
	c := buildContextChart(genSteps(spans), sonnet, "")
	tokens := string(c.Tokens)
	if strings.Contains(tokens, "<polyline") || c.Dollars != "" || len(c.Breaks) != 0 {
		t.Errorf("no cost, no price, no cache columns: line, dollar band and breaks must be absent")
	}
	if strings.Contains(tokens, "url(#hatch)") || strings.Contains(tokens, "tk-write") {
		t.Errorf("nil cache columns must draw only input and output buckets: %s", tokens)
	}
	if !strings.Contains(tokens, ">C<") {
		t.Errorf("the input drop at step 3 must still be marked")
	}
	notes := strings.Join(c.Notes, "\n")
	for _, want := range []string{"3 of 3 generations have no stored cost", "3 of 3 generations have no known price", "two categories"} {
		if !strings.Contains(notes, want) {
			t.Errorf("notes missing %q: %s", want, notes)
		}
	}
}

func TestDetailRendersContextChart(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	t0 := time.Now()
	tr := seedTrace(t, s, "trace-1", t0)
	insertSpans(t, s, cachedLoop(tr.ID, t0)...)

	body := doDetail(t, h, tr.ID).Body.String()
	for _, want := range []string{"Context per step</h3>", "3 generations · peak ", `<div id="ctx-body" hx-target="#span-detail"`, `href="/traces/trace-1?span=g3" hx-get="/traces/trace-1?span=g3"`, "The same steps in dollars", `href="/traces/trace-1?span=g3"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

func TestDetailHidesContextChartBelowTwoGenerations(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	t0 := time.Now()
	tr := seedTrace(t, s, "trace-1", t0)
	insertSpans(t, s, cachedLoop(tr.ID, t0)[:1]...)

	if body := doDetail(t, h, tr.ID).Body.String(); strings.Contains(body, "Context per step") {
		t.Errorf("a single generation has no step-to-step context to chart")
	}
}

// The legend names a mark only when the chart drew it.
func TestChartKeyListsOnlyWhatWasDrawn(t *testing.T) {
	if got := string(chartKey("", false)); got != "" {
		t.Errorf("nothing drawn: key = %q, want empty", got)
	}
	got := string(chartKey("CBT", true))
	for _, want := range []string{"cumulative cost", "smaller context", "cache break", "turn <i>n</i> starts"} {
		if !strings.Contains(got, want) {
			t.Errorf("key %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "cache expiry") || strings.Contains(got, "no model call") {
		t.Errorf("key %q names marks that were not drawn", got)
	}
}

// usd4 writes "<$0.0001": in hand-built SVG that "<" must be escaped.
func TestDollarAxisEscapesSmallAmounts(t *testing.T) {
	if svg := chartAxisSVG(0.00004, 100, "US$"); strings.Contains(svg, "<$") || !strings.Contains(svg, "&lt;$0.0001") {
		t.Errorf("axis = %s", svg)
	}
}
