package store

import "testing"

func TestMatchModelPriceLongestPatternWins(t *testing.T) {
	prices := []ModelPrice{
		{ModelPattern: "gpt-4o", InputPricePerToken: 0.0000025, OutputPricePerToken: 0.00001},
		{ModelPattern: "gpt-4o-mini", InputPricePerToken: 0.00000015, OutputPricePerToken: 0.0000006},
	}

	got, ok := MatchModelPrice(prices, "openai/gpt-4o-mini")
	if !ok {
		t.Fatal("no match, want gpt-4o-mini (longer, more specific pattern)")
	}
	if got.ModelPattern != "gpt-4o-mini" {
		t.Errorf("ModelPattern = %q, want gpt-4o-mini", got.ModelPattern)
	}
}

func TestMatchModelPriceAlphabeticalTiebreak(t *testing.T) {
	// Same length patterns, both match: alphabetically first wins,
	// deterministically, regardless of table row order.
	for _, prices := range [][]ModelPrice{
		{{ModelPattern: "model-zz"}, {ModelPattern: "model-aa"}},
		{{ModelPattern: "model-aa"}, {ModelPattern: "model-zz"}},
	} {
		got, ok := MatchModelPrice(prices, "model-aa-and-model-zz-both-match-this")
		if !ok {
			t.Fatal("no match")
		}
		if got.ModelPattern != "model-aa" {
			t.Errorf("ModelPattern = %q, want model-aa (alphabetically first)", got.ModelPattern)
		}
	}
}

func TestMatchModelPriceSkipsMalformedRegex(t *testing.T) {
	prices := []ModelPrice{
		{ModelPattern: "(unterminated", InputPricePerToken: 1},
		{ModelPattern: "claude-haiku-4-5", InputPricePerToken: 0.000001, OutputPricePerToken: 0.000005},
	}

	got, ok := MatchModelPrice(prices, "claude-haiku-4-5")
	if !ok {
		t.Fatal("no match, want the valid pattern despite a malformed sibling")
	}
	if got.ModelPattern != "claude-haiku-4-5" {
		t.Errorf("ModelPattern = %q, want claude-haiku-4-5", got.ModelPattern)
	}
}

func TestMatchModelPriceNoMatch(t *testing.T) {
	prices := []ModelPrice{{ModelPattern: "claude-haiku-4-5"}}
	if got, ok := MatchModelPrice(prices, "some-unpriced-model"); ok {
		t.Errorf("matched %q, want no match", got.ModelPattern)
	}
}

// Claude Code names the 1M-context variant "claude-opus-5-5[1m]"; the tag
// must not send it to another model's row.
func TestMatchModelPriceIgnoresVariantTag(t *testing.T) {
	prices := []ModelPrice{
		{ModelPattern: "claude-opus-5", InputPricePerToken: 5e-6},
		{ModelPattern: `(?i)^(claude-opus-5-5)$`, InputPricePerToken: 4e-6},
		{ModelPattern: `(?i)^(claude-opus-5-5\[1m\])$`, InputPricePerToken: 8e-6},
	}
	for model, want := range map[string]float64{
		"claude-opus-5-5[1m]": 8e-6, // a row written for the variant still wins: it is the longest
		"claude-opus-5-5":     4e-6,
		"claude-opus-5[1m]":   5e-6,
	} {
		if got, ok := MatchModelPrice(prices, model); !ok || got.InputPricePerToken != want {
			t.Errorf("%s: matched %q, want the row priced %v", model, got.ModelPattern, want)
		}
	}
	if got, _ := MatchModelPrice(prices[:2], "claude-opus-5-5[1m]"); got.InputPricePerToken != 4e-6 {
		t.Errorf("without a variant row, matched %q, want the base model's anchored row", got.ModelPattern)
	}
}

// The normalised forms of a decorated name, in the order they are tried.
func TestModelNameForms(t *testing.T) {
	for model, want := range map[string][4]string{
		"claude-opus-5-5[1m]":                        {"claude-opus-5-5[1m]", "claude-opus-5-5", "claude-opus-5-5", "claude-opus-5-5"},
		"claude-opus-5-5-20260915[1m]":               {"claude-opus-5-5-20260915[1m]", "claude-opus-5-5-20260915", "claude-opus-5-5", "claude-opus-5-5"},
		"us.anthropic.claude-opus-5-5-20260915-v1:0": {"us.anthropic.claude-opus-5-5-20260915-v1:0", "us.anthropic.claude-opus-5-5-20260915-v1:0", "us.anthropic.claude-opus-5-5-v1:0", "us.anthropic.claude-opus-5-5-v1:0"},
		"claude-opus-5-5@20260915":                   {"claude-opus-5-5@20260915", "claude-opus-5-5@20260915", "claude-opus-5-5", "claude-opus-5-5"},
		"gpt-5.4-2026-03-05":                         {"gpt-5.4-2026-03-05", "gpt-5.4-2026-03-05", "gpt-5.4", "gpt-5-4"},
		"anthropic/claude-opus-5.5":                  {"anthropic/claude-opus-5.5", "anthropic/claude-opus-5.5", "anthropic/claude-opus-5.5", "anthropic/claude-opus-5-5"},
	} {
		if got := modelNameForms(model); got != want {
			t.Errorf("modelNameForms(%q) = %q, want %q", model, got, want)
		}
	}
}

// A span's pricing is stale when another row now matches its model, when its
// row's prices changed, or when no row matches any more; never otherwise.
func TestStalePricings(t *testing.T) {
	read := 2e-7
	opus55 := ModelPrice{ModelPattern: `^claude-opus-5-5$`, InputPricePerToken: 4e-6, OutputPricePerToken: 2e-5, CacheReadPricePerToken: &read}
	opus5 := ModelPrice{ModelPattern: `claude-opus-5`, InputPricePerToken: 5e-6, OutputPricePerToken: 2.5e-5}
	cheaper := opus55
	cheaper.OutputPricePerToken = 1e-5
	if opus55.Fingerprint() == cheaper.Fingerprint() || opus55.Fingerprint() == opus5.Fingerprint() {
		t.Fatal("a fingerprint must change with the row's prices and with its pattern")
	}
	pricings := []SpanPricing{
		{Model: "claude-opus-5-5[1m]", Pattern: opus55.ModelPattern, Fingerprint: opus55.Fingerprint(), Spans: 7},
		{Model: "claude-opus-5-5[1m]", Pattern: opus5.ModelPattern, Fingerprint: opus5.Fingerprint(), Spans: 31}, // tagged name, priced by another model's row
		{Model: "gone-model", Pattern: "gone", Fingerprint: "00000000", Spans: 2},
	}
	if got := StalePricings([]ModelPrice{opus55, opus5}, pricings); got != 33 {
		t.Errorf("stale = %d, want 33: the 31 priced by the wrong row and the 2 whose row is gone", got)
	}
	if got := StalePricings([]ModelPrice{cheaper, opus5}, pricings[:1]); got != 7 {
		t.Errorf("stale after a price change = %d, want 7", got)
	}
}

// A row's own cache prices are used as they are; only a missing one is
// estimated from the input price. The explicit values here are deliberately
// not the estimate's multiples.
func TestCachePricesExplicitWinOverEstimate(t *testing.T) {
	in, read, write := 1e-6, 3e-7, 2e-6
	explicit := ModelPrice{InputPricePerToken: in, CacheReadPricePerToken: &read, CacheWritePricePerToken: &write}
	if explicit.CacheReadPrice() != read || explicit.CacheWritePrice() != write {
		t.Errorf("explicit row: read %v, write %v; want %v, %v", explicit.CacheReadPrice(), explicit.CacheWritePrice(), read, write)
	}
	estimated := ModelPrice{InputPricePerToken: in}
	if estimated.CacheReadPrice() != in*CacheReadMultiple || estimated.CacheWritePrice() != in*CacheWriteMultiple {
		t.Errorf("row without cache prices: read %v, write %v; want 0.1x and 1.25x of input", estimated.CacheReadPrice(), estimated.CacheWritePrice())
	}
	if got := explicit.Cost(TokenMix{CacheRead: 1, Fresh: 1, CacheWrite: 1}); got != [4]float64{read, in, write, 0} {
		t.Errorf("Cost = %v, want each bucket at its own price", got)
	}
}
