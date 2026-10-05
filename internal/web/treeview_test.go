package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func i64Ptr(v int64) *int64     { return &v }
func f64Ptr(v float64) *float64 { return &v }
func findNode(roots []*SpanNode, id string) *SpanNode {
	for _, r := range roots {
		if r.Span.ID == id {
			return r
		}
		if n := findNode(r.Children, id); n != nil {
			return n
		}
	}
	return nil
}

// A framework's chain span repeats the usage of the model call under it:
// the agent above must see that usage once.
func TestBuildSpanTreeRollupCountsDeepestUsageOnce(t *testing.T) {
	now := time.Now()
	spans := []store.Span{
		{ID: "agent", Kind: store.SpanKindAgent, StartedAt: now},
		{ID: "chain", Kind: store.SpanKindChain, ParentSpanID: strPtr("agent"), StartedAt: now,
			InputTokens: i64Ptr(100), OutputTokens: i64Ptr(50), CostUSD: f64Ptr(0.02)},
		{ID: "gen", Kind: store.SpanKindGeneration, ParentSpanID: strPtr("chain"), StartedAt: now,
			InputTokens: i64Ptr(100), OutputTokens: i64Ptr(50), CostUSD: f64Ptr(0.02)},
		{ID: "gen2", Kind: store.SpanKindGeneration, ParentSpanID: strPtr("agent"), StartedAt: now.Add(time.Second),
			InputTokens: i64Ptr(10), OutputTokens: i64Ptr(5), CostUSD: f64Ptr(0.01)},
		{ID: "tool", Kind: store.SpanKindTool, ParentSpanID: strPtr("agent"), StartedAt: now.Add(2 * time.Second)},
	}
	roots := BuildSpanTree(spans)

	agent := findNode(roots, "agent")
	if got := agent.Tokens.Total(); got != 165 || !agent.TokensSum {
		t.Errorf("agent tokens = %d (sum=%v), want 165 as a Σ — chain and gen carry the same 150 and must count once", got, agent.TokensSum)
	}
	if agent.Cost == nil || fmt.Sprintf("%.4f", *agent.Cost) != "0.0300" || !agent.CostSum {
		t.Errorf("agent cost = %v (sum=%v), want 0.03 as a Σ", agent.Cost, agent.CostSum)
	}
	chain := findNode(roots, "chain")
	if got := chain.Tokens.Total(); got != 150 || !chain.TokensSum {
		t.Errorf("chain tokens = %d (sum=%v), want its descendant's 150 as a Σ, not 300", got, chain.TokensSum)
	}
	gen := findNode(roots, "gen")
	if gen.Tokens.Total() != 150 || gen.TokensSum || gen.CostSum {
		t.Errorf("gen = %+v, want its own 150 tokens, not marked as a sum", gen)
	}
	tool := findNode(roots, "tool")
	if tool.Tokens.Total() != 0 || tool.Cost != nil {
		t.Errorf("tool without usage must stay empty, got %+v", tool)
	}
}

func TestBuildSpanTreePlacesSpansInTraceDuration(t *testing.T) {
	now := time.Now()
	roots := BuildSpanTree([]store.Span{
		{ID: "root", StartedAt: now, EndedAt: now.Add(10 * time.Second)},
		{ID: "half", ParentSpanID: strPtr("root"), StartedAt: now.Add(5 * time.Second), EndedAt: now.Add(10 * time.Second)},
		{ID: "instant", ParentSpanID: strPtr("root"), StartedAt: now.Add(10 * time.Second), EndedAt: now.Add(10 * time.Second)},
	})
	if n := findNode(roots, "root"); n.Offset != 0 || n.Width != 1 {
		t.Errorf("root = (%v, %v), want (0, 1)", n.Offset, n.Width)
	}
	if n := findNode(roots, "half"); n.Offset != 0.5 || n.Width != 0.5 {
		t.Errorf("half = (%v, %v), want (0.5, 0.5)", n.Offset, n.Width)
	}
	if n := findNode(roots, "instant"); n.Width != minBarWidth || n.Offset+n.Width > 1 {
		t.Errorf("instant = (%v, %v), want the minimum width, kept inside the bar", n.Offset, n.Width)
	}

	// A trace whose only span has no duration has nothing to place.
	flat := BuildSpanTree([]store.Span{{ID: "only", StartedAt: now, EndedAt: now}})
	if flat[0].Width != 0 || strings.Contains(string(timeBar(flat[0])), "<svg") {
		t.Errorf("zero-duration trace must draw no duration bar, got width %v", flat[0].Width)
	}
}

func TestBuildSpanTreeMarksOrphan(t *testing.T) {
	now := time.Now()
	roots := BuildSpanTree([]store.Span{
		{ID: "root", StartedAt: now},
		{ID: "orphan", ParentSpanID: strPtr("never-arrived"), StartedAt: now.Add(time.Second)},
	})
	if roots[0].Orphan || !roots[1].Orphan {
		t.Errorf("Orphan = (%v, %v), want only the span with a dangling parent marked", roots[0].Orphan, roots[1].Orphan)
	}
}

// failingTrace is an agent run where a tool fails and the error propagates
// to every ancestor, as instrumentors usually report it; a later tool
// fails too.
func failingTrace(now time.Time) []store.Span {
	at := func(s int) time.Time { return now.Add(time.Duration(s) * time.Second) }
	return []store.Span{
		{ID: "agent", Name: "agent", Kind: store.SpanKindAgent, Status: store.StatusError, StartedAt: at(0), EndedAt: at(10)},
		{ID: "gen", Name: "llm.call", Kind: store.SpanKindGeneration, ParentSpanID: strPtr("agent"), Status: store.StatusOK, StartedAt: at(0), EndedAt: at(4),
			InputTokens: i64Ptr(100), CacheReadTokens: i64Ptr(80), OutputTokens: i64Ptr(50), CostUSD: f64Ptr(0.02)},
		{ID: "tool-fail", Name: "tool.Bash", Kind: store.SpanKindTool, ParentSpanID: strPtr("agent"), Status: store.StatusError, StartedAt: at(4), EndedAt: at(5)},
		{ID: "gen-big", Name: "llm.call", Kind: store.SpanKindGeneration, ParentSpanID: strPtr("agent"), Status: store.StatusOK, StartedAt: at(5), EndedAt: at(7),
			InputTokens: i64Ptr(900), OutputTokens: i64Ptr(10), CostUSD: f64Ptr(0.09)},
		{ID: "tool-fail-2", Name: "tool.Read", Kind: store.SpanKindTool, ParentSpanID: strPtr("agent"), Status: store.StatusError, StartedAt: at(7), EndedAt: at(10)},
	}
}

func TestFirstFailureIsTheOriginNotTheAncestor(t *testing.T) {
	roots := BuildSpanTree(failingTrace(time.Now()))
	if f := firstFailure(roots); f == nil || f.Span.ID != "tool-fail" {
		t.Errorf("firstFailure = %+v, want tool-fail (the agent only inherits the error)", f)
	}
	if f := firstFailure(BuildSpanTree([]store.Span{{ID: "ok", Status: store.StatusOK}})); f != nil {
		t.Errorf("firstFailure on a trace with no error = %+v, want nil", f)
	}
}

func TestSelectSpanDefaultsToFirstFailure(t *testing.T) {
	spans := failingTrace(time.Now())
	roots := BuildSpanTree(spans)
	if _, id := selectSpan(spans, roots, ""); id != "tool-fail" {
		t.Errorf("default selection = %q, want tool-fail", id)
	}
	if _, id := selectSpan(spans, roots, "gen"); id != "gen" {
		t.Errorf("explicit ?span=gen selected %q — an explicit choice always wins", id)
	}
}

func TestWorthALook(t *testing.T) {
	roots := BuildSpanTree(failingTrace(time.Now()))
	got := worthALook(roots, stepNodes(roots))
	want := []lookLink{
		{"first failure", "tool-fail", "tool.Bash", ""},
		{"slowest", "gen", "llm.call", "4.0 s"},
		{"most expensive · most tokens", "gen-big", "llm.call", "$0.0900 · 910 tokens"}, // one span, two reasons
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("worthALook = %v, want %v", got, want)
	}

	// One span that is the slowest, the dearest and the largest is one entry with the three
	// reasons; the first failure stays its own entry even when it is that span.
	now0 := time.Now()
	cost, in := 0.5, int64(9000)
	roots = BuildSpanTree([]store.Span{
		{ID: "big", Name: "big", Kind: store.SpanKindGeneration, StartedAt: now0, EndedAt: now0.Add(5 * time.Second), CostUSD: &cost, InputTokens: &in, Status: store.StatusError},
		{ID: "small", Name: "small", Kind: store.SpanKindTool, StartedAt: now0, EndedAt: now0.Add(time.Second)},
	})
	got = worthALook(roots, stepNodes(roots))
	if len(got) != 2 || got[0].Label != "first failure" || got[1].Label != "slowest · most expensive · most tokens" || got[1].Value != "5.0 s · $0.5000 · 9.0k tokens" {
		t.Errorf("worthALook for one dominant span = %v", got)
	}

	// Two tool calls, no error, no tokens, no price: only the slowest.
	now := time.Now()
	roots = BuildSpanTree([]store.Span{
		{ID: "a", Name: "a", Kind: store.SpanKindTool, StartedAt: now, EndedAt: now.Add(time.Second)},
		{ID: "b", Name: "b", Kind: store.SpanKindTool, StartedAt: now.Add(time.Second), EndedAt: now.Add(3 * time.Second)},
	})
	got = worthALook(roots, stepNodes(roots))
	if len(got) != 1 || got[0].SpanID != "b" || got[0].Label != "slowest" {
		t.Errorf("worthALook without errors, tokens or cost = %v, want only the slowest step (b)", got)
	}
}

func TestStepNodesKeepsOnlyStepsInStartOrder(t *testing.T) {
	roots := BuildSpanTree(failingTrace(time.Now()))
	var ids []string
	for _, n := range stepNodes(roots) {
		ids = append(ids, n.Span.ID)
	}
	if got, want := strings.Join(ids, ","), "gen,tool-fail,gen-big,tool-fail-2"; got != want {
		t.Errorf("steps = %s, want %s (the agent span is structure, not a step)", got, want)
	}
}

func seedSpans(t *testing.T, s store.Store, traceID string, spans []store.Span) {
	t.Helper()
	for _, sp := range spans {
		sp.TraceID, sp.CreatedAt = traceID, sp.StartedAt
		if err := s.InsertSpan(t.Context(), sp); err != nil {
			t.Fatalf("InsertSpan(%s): %v", sp.ID, err)
		}
	}
}

// On a failed trace, first failure and most expensive step are named at the
// top and the failure is already open, without touching the tree.
func TestDetailFailedTraceReadsFast(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now()
	tr := seedTrace(t, s, "trace-1", now)
	spans := failingTrace(now)
	spans = append(spans, store.Span{ID: "late", Name: "late", Kind: store.SpanKindGeneric, ParentSpanID: strPtr("never-arrived"), Status: store.StatusOK, StartedAt: now.Add(9 * time.Second), EndedAt: now.Add(10 * time.Second)})
	seedSpans(t, s, tr.ID, spans)

	w := doDetail(t, h, tr.ID)
	if w.Code != 200 {
		t.Fatalf("status = %d, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	link := func(id string) string {
		return `href="/traces/trace-1?span=` + id + `" hx-get="/traces/trace-1?span=` + id + `" hx-target="#span-detail" hx-select="#span-detail" hx-select-oob="#span-tree:innerHTML,#ctx-body,#find" hx-push-url="true" hx-swap="outerHTML show:#span-top:top"`
	}
	for _, want := range []string{
		// worth a look line
		"Worth a look",
		// first-failure pointer
		`<a class="chip look" ` + link("tool-fail") + ` title="tool.Bash">`,
		// most-expensive pointer
		`<a class="chip" ` + link("gen-big") + ` title="llm.call">most expensive`,
		// its cost
		"$0.0900",
		// largest-token pointer
		"most tokens",
		// own-error glyph in the tree, on the selected row
		`<a class="trow" id="sel" aria-current="true" ` + link("tool-fail") + `>
<span class="t-name"><svg class="st err" role="img" aria-label="error">`,
		// orphan marker
		`title="parent span never-arrived never arrived; shown as a root">no parent</span>`,
		// Σ token rollup on the agent
		`title="Σ of descendants: 1060 tokens"><svg`,
		// Σ cost rollup on the agent
		"Σ $0.1100",
		// four token buckets when cache
		"<title>cache read: 80</title>",
		// fresh bucket excludes cache
		"<title>fresh input: 20</title>",
		// duration bar placed in time
		`<rect x="22.4" y="1" width="5.6" height="6" rx="1" class="k k-tool"/>`,
		// failing span opened by default
		`<h2>tool.Bash</h2>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "ZgotmplZ") {
		t.Error("html/template rejected an inline style value (ZgotmplZ)")
	}
}

// One model call, no error, no cache columns: no pointers, two
// token buckets, own usage with no Σ.
func TestDetailQuietTraceShowsNoOverview(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now()
	tr := seedTrace(t, s, "trace-1", now)
	seedSpans(t, s, tr.ID, []store.Span{{ID: "gen", Name: "llm.call", Kind: store.SpanKindGeneration, Status: store.StatusOK,
		StartedAt: now, EndedAt: now.Add(time.Second), InputTokens: i64Ptr(100), OutputTokens: i64Ptr(50)}})

	body := doDetail(t, h, tr.ID).Body.String()
	for _, absent := range []string{"Worth a look", "Σ", "cache read", "no parent"} {
		if strings.Contains(body, absent) {
			t.Errorf("body must not contain %q for a single ok step", absent)
		}
	}
	for _, want := range []string{"<title>fresh input: 100</title>", "<title>output: 50</title>"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// Four decimals under a dollar, then one fewer per digit of dollars.
func TestRowCost(t *testing.T) {
	for cost, want := range map[float64]string{0.0123: "$0.0123", 0.99996: "$1.0000", 1: "$1.000", 2.5: "$2.500", 10: "$10.00", 12.345: "$12.35"} {
		if got := rowCost(&cost); got != want {
			t.Errorf("rowCost(%v) = %q, want %q", cost, got, want)
		}
	}
}
