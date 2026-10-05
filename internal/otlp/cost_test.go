package otlp

import (
	"math"
	"reflect"
	"testing"

	"github.com/jhisse/spoor/internal/store"
)

func ptr[T any](v T) *T { return &v }

func calculateCost(prices []store.ModelPrice, s store.Span) *float64 {
	priceSpan(prices, &s)
	return s.CostUSD
}

func approx(t *testing.T, got *float64, want float64) {
	t.Helper()
	if got == nil {
		t.Fatalf("cost = nil, want %v", want)
	}
	if math.Abs(*got-want) > 1e-12 {
		t.Errorf("cost = %v, want %v", *got, want)
	}
}

// A real llm.call span (prompt caching on, no completion count) at
// claude-sonnet-5's catalog prices. Priced by hand:
//
//	fresh       2 * 0.000002   = 0.000004
//	cache read  87486 * 2e-7   = 0.0174972
//	cache write 1264 * 2.5e-6  = 0.00316
func TestCalculateCostPricesEachUsageType(t *testing.T) {
	prices := []store.ModelPrice{{
		ModelPattern: "claude-sonnet-5", InputPricePerToken: 0.000002, OutputPricePerToken: 0.00001,
		CacheReadPricePerToken: ptr(0.0000002), CacheWritePricePerToken: ptr(0.0000025),
	}}
	s := store.Span{
		Model:       ptr("claude-sonnet-5"),
		InputTokens: ptr(int64(88752)), CacheReadTokens: ptr(int64(87486)), CacheWriteTokens: ptr(int64(1264)),
	}
	approx(t, calculateCost(prices, s), 0.0206612)

	// Same span with the turn's 633 output tokens: + 633 * 0.00001.
	s.OutputTokens = ptr(int64(633))
	approx(t, calculateCost(prices, s), 0.0269912)

	// The whole prompt at the input rate would be 0.177504 for the input side alone.
	if got := *calculateCost(prices, s); got > 0.03 {
		t.Errorf("cost = %v, cache tokens are still priced as fresh input", got)
	}
}

func TestCalculateCostEstimatesMissingCachePrices(t *testing.T) {
	prices := []store.ModelPrice{{ModelPattern: "some-model", InputPricePerToken: 0.000001, OutputPricePerToken: 0.000005}}
	s := store.Span{
		Model:       ptr("some-model"),
		InputTokens: ptr(int64(1000)), OutputTokens: ptr(int64(50)),
		CacheReadTokens: ptr(int64(600)), CacheWriteTokens: ptr(int64(300)),
	}
	// 100 fresh + 600 read at 0.1x + 300 written at 1.25x + 50 output.
	want := 100*0.000001 + 600*0.0000001 + 300*0.00000125 + 50*0.000005
	approx(t, calculateCost(prices, s), want)
}

func TestCalculateCostWithoutCacheIsInputPlusOutput(t *testing.T) {
	prices := []store.ModelPrice{{ModelPattern: "claude-haiku-4-5", InputPricePerToken: 0.000001, OutputPricePerToken: 0.000005}}
	s := store.Span{Model: ptr("claude-haiku-4-5-20251001"), InputTokens: ptr(int64(100)), OutputTokens: ptr(int64(50))}
	approx(t, calculateCost(prices, s), 100*0.000001+50*0.000005)
}

func TestCalculateCostUnknownStaysUnknown(t *testing.T) {
	prices := []store.ModelPrice{{ModelPattern: "claude-haiku-4-5", InputPricePerToken: 0.000001, OutputPricePerToken: 0.000005}}
	cases := map[string]store.Span{
		"unpriced model":  {Model: ptr("some-unpriced-model"), InputTokens: ptr(int64(100)), OutputTokens: ptr(int64(50))},
		"no model":        {InputTokens: ptr(int64(100)), OutputTokens: ptr(int64(50))},
		"no token counts": {Model: ptr("claude-haiku-4-5")},
	}
	for name, s := range cases {
		if got := calculateCost(prices, s); got != nil {
			t.Errorf("%s: cost = %v, want nil", name, *got)
		}
	}
}

var repricePrices = []store.ModelPrice{{ModelPattern: "^claude-sonnet-5$", InputPricePerToken: 3e-6, OutputPricePerToken: 15e-6, CacheReadPricePerToken: ptr(3e-7), CacheWritePricePerToken: ptr(3.75e-6)}}

// A stored span is priced again with the table as it is now, by usage
// type, and records the row that priced it; doing it twice changes nothing.
func TestRepriceRecalculatesFromTheTable(t *testing.T) {
	stored := store.Span{
		Model: ptr("claude-sonnet-5"), InputTokens: ptr(int64(88752)), OutputTokens: ptr(int64(100)),
		CacheReadTokens: ptr(int64(87486)), CacheWriteTokens: ptr(int64(1264)), CostUSD: ptr(0.267756),
	}
	got := Reprice(repricePrices, stored)
	want := 2*3e-6 + 87486*3e-7 + 1264*3.75e-6 + 100*15e-6
	if got.CostUSD == nil || math.Abs(*got.CostUSD-want) > 1e-12 || got.PricePattern == nil || *got.PricePattern != "^claude-sonnet-5$" {
		t.Errorf("CostUSD = %v by row %v, want %v by the sonnet row", got.CostUSD, got.PricePattern, want)
	}
	if again := Reprice(repricePrices, got); !reflect.DeepEqual(again, got) {
		t.Errorf("second Reprice changed the span: %+v, want it idempotent", again)
	}
}

func TestRepriceLeavesReportedAndIncalculableCostsAlone(t *testing.T) {
	for name, sp := range map[string]store.Span{
		"llm.cost.total reported": {Model: ptr("claude-sonnet-5"), InputTokens: ptr(int64(10)), CostUSD: ptr(0.5), Metadata: []byte(`{"llm.cost.total": 0.5}`)},
		"spoor.cost_usd reported": {Model: ptr("claude-sonnet-5"), InputTokens: ptr(int64(10)), CostUSD: ptr(0.5), Metadata: []byte(`{"spoor.cost_usd": 0.5}`)},
		"no price for the model":  {Model: ptr("unknown-model"), InputTokens: ptr(int64(10)), CostUSD: ptr(0.5)},
		"no model, no metadata":   {CostUSD: ptr(0.5)},
	} {
		if got := Reprice(repricePrices, sp); got.CostUSD == nil || *got.CostUSD != 0.5 {
			t.Errorf("%s: CostUSD = %v, want 0.5 untouched", name, got.CostUSD)
		}
	}
	if got := Reprice(repricePrices, store.Span{Model: ptr("unknown-model"), InputTokens: ptr(int64(10))}); got.CostUSD != nil {
		t.Errorf("unpriced model: CostUSD = %v, want nil (unknown stays unknown)", *got.CostUSD)
	}
}

// What ingestion stores is what Reprice later reads: a reported cost must
// survive in metadata, or a reprice would overwrite it.
func TestReportedCostSurvivesIngestThenReprice(t *testing.T) {
	s := translateAttrs(map[string]string{"spoor.cost_usd": "0.5", "gen_ai.request.model": "claude-sonnet-5", "gen_ai.usage.input_tokens": "10"})
	if got := Reprice(repricePrices, s); got.CostUSD == nil || *got.CostUSD != 0.5 {
		t.Errorf("CostUSD after reprice = %v, want the reported 0.5", got.CostUSD)
	}
}
