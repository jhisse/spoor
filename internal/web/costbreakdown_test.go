package web

import (
	"encoding/json"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

var (
	pricedBoth  = store.ModelPrice{ModelPattern: "^full-model$", InputPricePerToken: 3e-6, OutputPricePerToken: 15e-6, CacheReadPricePerToken: ptr(0.3e-6), CacheWritePricePerToken: ptr(3.75e-6)}
	pricedPlain = store.ModelPrice{ModelPattern: "^plain-model$", InputPricePerToken: 1e-6, OutputPricePerToken: 2e-6}
	testPrices  = []store.ModelPrice{pricedBoth, pricedPlain}
)

// genSpan is a generation whose prompt of 1000 tokens includes 600 read
// from cache and 100 written to it, plus 50 output tokens.
func genSpan(model string, cost *float64) store.Span {
	sp := gen("t", "g", nil, time.Now(), nil)
	sp.Model, sp.CostUSD = &model, cost
	sp.InputTokens, sp.CacheReadTokens, sp.CacheWriteTokens, sp.OutputTokens = ptr(int64(1000)), ptr(int64(600)), ptr(int64(100)), ptr(int64(50))
	return sp
}

func TestCostBreakdownClosesOnStoredCost(t *testing.T) {
	// 300 fresh × $3/M + 600 read × $0.30/M + 100 write × $3.75/M + 50 out × $15/M
	const want = 0.0009 + 0.00018 + 0.000375 + 0.00075
	c := buildCostBreakdown(genSpan("full-model", ptr(want)), testPrices)
	if c.Pattern != "^full-model$" || c.Reported || c.Differs() {
		t.Fatalf("breakdown = %+v, want the full-model row, computed, closing", c)
	}
	wantLines := []costLine{
		{"cache read", 600, 0.3, 0.00018, false},
		{"fresh input", 300, 3, 0.0009, false},
		{"cache write", 100, 3.75, 0.000375, false},
		{"output", 50, 15, 0.00075, false},
	}
	if len(c.Lines) != len(wantLines) {
		t.Fatalf("lines = %+v, want 4", c.Lines)
	}
	var sum float64
	for i, l := range c.Lines {
		w := wantLines[i]
		if l.Label != w.Label || l.Tokens != w.Tokens || l.Estimated || math.Abs(l.PerMillion-w.PerMillion) > 1e-9 || math.Abs(l.USD-w.USD) > 1e-12 {
			t.Errorf("line %d = %+v, want %+v", i, l, w)
		}
		sum += l.USD
	}
	if sum != c.Computed || math.Abs(c.Computed-want) > 1e-12 {
		t.Errorf("computed = %v, lines sum to %v, want %v", c.Computed, sum, want)
	}
}

func TestCostBreakdownMarksEstimatedCachePrices(t *testing.T) {
	c := buildCostBreakdown(genSpan("plain-model", nil), testPrices)
	for _, l := range c.Lines {
		cache := strings.HasPrefix(l.Label, "cache")
		if l.Estimated != cache {
			t.Errorf("%s: estimated = %v, want %v (the row has no cache rate)", l.Label, l.Estimated, cache)
		}
	}
	if got, want := c.Lines[0].PerMillion, pricedPlain.CacheReadPrice()*1e6; got != want {
		t.Errorf("cache read price = %v, want the store helper's %v", got, want)
	}
	if c.Stored != nil || c.Differs() || c.Computed == 0 {
		t.Errorf("never-stored cost: %+v, want a computed figure and no mismatch claim", c)
	}
}

func TestCostBreakdownReportedStaleAndUnpriced(t *testing.T) {
	reported := genSpan("full-model", ptr(0.5))
	reported.Metadata = json.RawMessage(`{"spoor.cost_usd": 0.5}`)
	if c := buildCostBreakdown(reported, testPrices); !c.Reported || !c.Differs() || c.Computed == 0 {
		t.Errorf("reported cost: %+v, want reported, differing, with the computed figure beside it", c)
	}
	openInference := genSpan("full-model", ptr(0.5))
	openInference.Metadata = json.RawMessage(`{"llm.cost.total": 0.5}`)
	if c := buildCostBreakdown(openInference, testPrices); !c.Reported {
		t.Error("llm.cost.total in metadata must count as a reported cost")
	}
	if c := buildCostBreakdown(genSpan("full-model", ptr(0.001)), testPrices); c.Reported || !c.Differs() {
		t.Errorf("stale price: %+v, want a mismatch that is not attributed to the SDK", c)
	}
	c := buildCostBreakdown(genSpan("no-such-model", nil), testPrices)
	if c.Pattern != "" || c.Lines != nil || c.Differs() {
		t.Errorf("unpriced model: %+v, want no lines and a blind-spots link", c)
	}
	tool := genSpan("full-model", nil)
	tool.Kind = store.SpanKindTool
	noModel := genSpan("full-model", nil)
	noModel.Model = nil
	if buildCostBreakdown(tool, testPrices) != nil || buildCostBreakdown(noModel, testPrices) != nil {
		t.Error("a non-generation or a span without a model has no breakdown")
	}
}

// Every capture that got a cost at ingestion must close: the UI's breakdown
// and internal/otlp's calculation are two readers of one rule.
func TestCostBreakdownClosesOnEveryRealCapture(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/*.pb")
	closed := 0
	for _, f := range files {
		s := newTestStore(t)
		ingestCapture(t, s, filepath.Base(f))
		prices, err := s.ListModelPrices(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		_, spans := onlyTrace(t, s)
		for _, sp := range spans {
			c := buildCostBreakdown(sp, prices)
			if sp.CostUSD == nil || c == nil {
				continue
			}
			closed++
			// LiteLLM reports its own cost and no cache split: the two differ.
			if reported := strings.HasPrefix(filepath.Base(f), "litellm"); c.Differs() != reported || c.Reported != reported {
				t.Errorf("%s %s: stored %v, breakdown %+v", filepath.Base(f), sp.Name, *sp.CostUSD, c)
			}
		}
	}
	if closed < 10 {
		t.Errorf("only %d priced generations across the captures; the test is not exercising them", closed)
	}
}

func TestDetailCostBreakdownStates(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())
	prices, err := s.ListModelPrices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	const model = "claude-haiku-4-5-20251001"
	closing := buildCostBreakdown(genSpan(model, nil), prices).Computed

	cases := []struct {
		id, model string
		cost      *float64
		metadata  string
		want      []string
		notWant   string
	}{
		{"closes", model, &closing, "", []string{"fresh input", "cache read", "cache write", "output", "sum", "= " + usd(closing), "The sum matches the cost stored"}, "differs"},
		{"stale", model, ptr(closing * 2), "", []string{"differs from today's calculation", "Priced with an older table: stored " + usd(closing*2) + ", today's table gives " + usd(closing), "<code>spoor reprice</code>", "= " + usd(closing)}, "The stored cost came from the row"},
		// Same dollars, but the recorded row and fingerprint are not today's: still said.
		{"old-row", model, &closing, "", []string{"Priced with an older table", "The stored cost came from the row <code>claude-haiku-4-5</code>"}, "The sum matches"},
		{"reported", model, ptr(0.5), `{"spoor.cost_usd":0.5}`, []string{"reported by the SDK", usd(0.5), "by the table it would be " + usd(closing)}, "The sum matches"},
		{"fast", model, &closing, `{"speed":"fast"}`, []string{"Fast mode. Priced at the standard rate"}, "above 200k"},
		{"never-stored", model, nil, "", []string{"No cost was stored at ingestion", "= " + usd(closing)}, "The sum matches"},
		{"unpriced", "nobody/unknown-model", nil, "", []string{"<b>nobody/unknown-model</b> has no price in the table", `href="/blind-spots"`}, ">sum<"},
	}
	for _, tc := range cases {
		sp := genSpan(tc.model, tc.cost)
		sp.TraceID, sp.ID, sp.Name = tr.ID, tc.id, tc.id
		if tc.metadata != "" {
			sp.Metadata = json.RawMessage(tc.metadata)
		}
		if tc.id == "old-row" {
			sp.PricePattern, sp.PriceFingerprint = ptr("claude-haiku-4-5"), ptr("0badf00d")
		}
		insertSpans(t, s, sp)
		body := doDetailQuery(t, h, tr.ID, "span="+tc.id).Body.String()
		panel := body[strings.Index(body, `id="span-detail"`):]
		for _, want := range append(tc.want, "<details", "the arithmetic") {
			if !strings.Contains(panel, want) {
				t.Errorf("%s: panel missing %q", tc.id, want)
			}
		}
		if strings.Contains(panel, tc.notWant) {
			t.Errorf("%s: panel must not say %q", tc.id, tc.notWant)
		}
	}
}

// A cost spoor computed from a captured payload closes on the page, and the
// cache lines of a model whose price row has no cache rate say "estimated".
func TestDetailCostBreakdownOnRealCapture(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "openllmetry-anthropic-simple.pb")
	tr, spans := onlyTrace(t, s)
	body := doDetail(t, h, tr.ID).Body.String()
	for _, want := range []string{"The sum matches the cost stored", "= " + usd(*spans[0].CostUSD), "from the table row"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// A cost spoor calculated with no cache count from the sender is marked where
// it is shown, and explained in the span's cost breakdown; a span whose
// sender did count the cache is not.
func TestCostWithoutCacheCountsIsMarked(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "openllmetry-openai-openrouter-simple.pb")
	ingestCapture(t, s, "claude-code-simple.pb")
	list := doList(t, h, "").Body.String()
	if n := strings.Count(list, `>~</span>`); n != 1 {
		t.Errorf("list marks %d costs, want 1 (the capture without cache counts)", n)
	}
	traces, _, err := s.QueryTraces(t.Context(), store.TraceQuery{})
	if err != nil {
		t.Fatal(err)
	}
	for _, tr := range traces {
		page := doDetail(t, h, tr.ID).Body.String()
		unknown := tr.CacheUnknown > 0
		if got := strings.Contains(page, `>~</span>`); got != unknown {
			t.Errorf("%s: header marked = %v, want %v", tr.Name, got, unknown)
		}
		_, spans, _ := s.GetTraceByID(t.Context(), tr.ID)
		for _, sp := range spans {
			if sp.Kind != store.SpanKindGeneration {
				continue
			}
			panel := doDetailQuery(t, h, tr.ID, "span="+sp.ID).Body.String()
			if got := strings.Contains(panel, "No cache counts."); got != sp.CacheUnknown() {
				t.Errorf("%s span %s: explained = %v, want %v", tr.Name, sp.Name, got, sp.CacheUnknown())
			}
		}
	}
}
