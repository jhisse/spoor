package sqlite

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/jhisse/spoor/internal/store"
)

// These tests read the REAL price table, a fresh database with every
// migration applied: a hand-made two-row table cannot show a model name
// falling to another model's row.

func catalogPrices(t *testing.T) []store.ModelPrice {
	t.Helper()
	prices, err := newTestStore(t).ListModelPrices(context.Background())
	if err != nil {
		t.Fatalf("ListModelPrices: %v", err)
	}
	if len(prices) < 150 {
		t.Fatalf("got %d price rows, want the full catalog", len(prices))
	}
	return prices
}

// An unanchored row is a substring match: it also prices every longer name
// that contains it. No catalog row may be one.
func TestCatalogPatternsCompileAndAreAnchored(t *testing.T) {
	for _, p := range catalogPrices(t) {
		if _, err := regexp.Compile(p.ModelPattern); err != nil {
			t.Errorf("catalog pattern does not compile: %v", err)
		}
		if !strings.Contains(p.ModelPattern, "^") || !strings.HasSuffix(p.ModelPattern, "$") {
			t.Errorf("catalog pattern %q is not anchored with ^…$", p.ModelPattern)
		}
	}
}

// Every model id resolves to its OWN row. row is a fragment only that row's
// pattern contains; inputPerM tells apart the pairs whose prices differ.
func TestCatalogModelsResolveToTheirOwnRow(t *testing.T) {
	prices := catalogPrices(t)
	cases := []struct {
		model, row string
		inputPerM  float64
	}{
		// Claude Code's 1M-context tag must not send Opus 5.5 to Opus 5's row.
		{"claude-opus-5-5[1m]", "claude-opus-5-5|", 4},
		{"claude-sonnet-5[1m]", "claude-sonnet-5|", 2},
		{"claude-opus-5[1m]", "claude-opus-5|", 5},

		// Each id that is a prefix of a newer one, and the newer one.
		{"claude-opus-5", "claude-opus-5|", 5},
		{"claude-opus-5-5", "claude-opus-5-5|", 4},
		{"claude-sonnet-5", "claude-sonnet-5|", 2},
		{"claude-sonnet-5-5", "claude-sonnet-5-5|", 2},
		{"claude-fable-5", "claude-fable-5|", 10},
		{"claude-fable-5-1", "claude-fable-5-1|", 10},
		{"claude-mythos-5", "claude-mythos-5$", 10},
		{"claude-mythos-5-1", "claude-mythos-5-1$", 10},
		{"claude-opus-4", "claude-opus-4(-20250514)?|", 15},
		{"claude-opus-4-1", "claude-opus-4-1(", 15},
		{"claude-opus-4-5", "claude-opus-4-5(", 5},
		{"claude-opus-4-8", "claude-opus-4-8|", 5},
		{"claude-sonnet-4", "claude-sonnet-4(-20250514)?|", 3},
		{"claude-sonnet-4-6", "claude-sonnet-4-6|", 3},
		{"claude-haiku-4-5", "claude-haiku-4-5(", 1},
		{"gpt-4o", "(gpt-4o)", 2.5},
		{"gpt-4o-mini", "(gpt-4o-mini)", 0.15},
		{"gpt-5", "(gpt-5)", 1.25},
		{"gpt-5-mini", "(gpt-5-mini)", 0.25},
		{"gpt-5-nano", "(gpt-5-nano)", 0.05},
		{"gpt-5-pro", "(gpt-5-pro)", 15},
		{"gpt-5.4", "(gpt-5.4)", 2.5},
		{"gpt-5.4-mini", "(gpt-5.4-mini)", 0.75},
		{"gpt-5.4-pro", "(gpt-5.4-pro)", 30},
		{"o3", "(o3)", 2},
		{"o3-mini", "(o3-mini)", 1.1},
		{"o3-pro", "(o3-pro)", 20},

		// Dated snapshots: their own row when there is one, else the model's.
		{"claude-haiku-4-5-20251001", "claude-haiku-4-5(", 1},
		{"claude-opus-5-5-20260915", "claude-opus-5-5|", 4},
		{"claude-sonnet-5-5-20260915", "claude-sonnet-5-5|", 2},
		{"claude-fable-5-1-20260920", "claude-fable-5-1|", 10},
		{"claude-opus-5-5-20260915[1m]", "claude-opus-5-5|", 4},
		{"gpt-4o-2024-05-13", "(gpt-4o-2024-05-13)", 5},
		{"gpt-4o-mini-2024-07-18", "(gpt-4o-mini-2024-07-18)", 0.15},
		{"gpt-5.4-mini-2026-03-17", "(gpt-5.4-mini-2026-03-17)", 0.75},

		// Provider prefixes: OpenRouter, Bedrock, Vertex.
		{"anthropic/claude-opus-5-5", "claude-opus-5-5|", 4},
		{"anthropic/claude-opus-5.5", "claude-opus-5-5|", 4},
		{"anthropic/claude-opus-5", "claude-opus-5|", 5},
		{"anthropic/claude-haiku-4-5", "claude-haiku-4-5(", 1},
		{"us.anthropic.claude-opus-5-5-v1:0", "claude-opus-5-5|", 4},
		{"global.anthropic.claude-opus-5-v1:0", "claude-opus-5|", 5},
		{"us.anthropic.claude-opus-5-5-20260915-v1:0", "claude-opus-5-5|", 4},
		{"anthropic.claude-sonnet-5-5", "claude-sonnet-5-5|", 2},
		{"claude-opus-5-5@20260915", "claude-opus-5-5|", 4},
		{"claude-opus-4-5@20251101", "claude-opus-4-5(", 5},
		{"openai/gpt-4o-mini", "(gpt-4o-mini)", 0.15},
		{"google/gemini-3.6-flash", "(gemini-3.6-flash)", 0.75},
	}
	for _, c := range cases {
		p, ok := store.MatchModelPrice(prices, c.model)
		if !ok {
			t.Errorf("%s: no price row, want the one containing %q", c.model, c.row)
			continue
		}
		if got := p.InputPricePerToken * 1e6; !strings.Contains(p.ModelPattern, c.row) || got < c.inputPerM*0.999999 || got > c.inputPerM*1.000001 {
			t.Errorf("%s: matched %q at $%v/M input, want the row containing %q at $%v/M", c.model, p.ModelPattern, got, c.row, c.inputPerM)
		}
	}

	// A name the table does not know has no price; it never borrows one.
	for _, model := range []string{"acme/in-house-model", "claude-opus-6", "claude-opus-5-5-preview", "my-claude-opus-5-finetune", "gpt-4o-mini-transcribe-x", "gpt-9"} {
		if p, ok := store.MatchModelPrice(prices, model); ok {
			t.Errorf("%s: matched %q, want no price", model, p.ModelPattern)
		}
	}
}

// Published list prices, USD per million tokens, copied by hand from the
// providers' own pages on 2026-10-03:
//
//	Anthropic https://platform.claude.com/docs/en/about-claude/pricing
//	OpenAI    https://developers.openai.com/api/docs/pricing
//	Google    https://ai.google.dev/gemini-api/docs/pricing
//
// When a provider changes a price, this table and
// model_prices.json change in the same commit. write and write1h are the 5-minute and 1-hour cache
// writes: 0 for OpenAI and Google, whose pages list no such charge for these
// models (write is then not pinned, and write1h must be absent).
func TestCatalogPricesMatchPublishedPrices(t *testing.T) {
	prices := catalogPrices(t)
	cases := []struct {
		model                               string
		input, write, write1h, read, output float64
	}{
		{"claude-fable-5-1", 10, 12.50, 20, 0.25, 50},
		{"claude-mythos-5-1", 10, 12.50, 20, 0.25, 50},
		{"claude-fable-5", 10, 12.50, 20, 1, 50},
		{"claude-mythos-5", 10, 12.50, 20, 1, 50},
		{"claude-opus-5-5", 4, 5, 8, 0.20, 20},
		{"claude-opus-5", 5, 6.25, 10, 0.50, 25},
		{"claude-opus-4-8", 5, 6.25, 10, 0.50, 25},
		{"claude-opus-4-7", 5, 6.25, 10, 0.50, 25},
		{"claude-opus-4-6", 5, 6.25, 10, 0.50, 25},
		{"claude-opus-4-5", 5, 6.25, 10, 0.50, 25},
		{"claude-opus-4-1", 15, 18.75, 30, 1.50, 75},
		{"claude-opus-4", 15, 18.75, 30, 1.50, 75},
		{"claude-sonnet-5-5", 2, 2.50, 4, 0.20, 10},
		{"claude-sonnet-5", 2, 2.50, 4, 0.20, 10},
		{"claude-sonnet-4-6", 3, 3.75, 6, 0.30, 15},
		{"claude-sonnet-4-5", 3, 3.75, 6, 0.30, 15},
		{"claude-sonnet-4", 3, 3.75, 6, 0.30, 15},
		{"claude-haiku-4-5", 1, 1.25, 2, 0.10, 5},
		{"claude-3-5-haiku-20241022", 0.80, 1, 1.60, 0.08, 4},
		{"gpt-4o-mini", 0.15, 0, 0, 0.075, 0.60},
		{"gpt-4o", 2.50, 0, 0, 1.25, 10},
		{"gpt-5", 1.25, 0, 0, 0.125, 10},
		{"gpt-5-mini", 0.25, 0, 0, 0.025, 2},
		{"gpt-5.4", 2.50, 0, 0, 0.25, 15},
		{"gpt-5.4-mini", 0.75, 0, 0, 0.075, 4.50},
		{"o3", 2, 0, 0, 0.50, 8},
		{"google/gemini-3.6-flash", 0.75, 0, 0, 0.075, 3.75},
	}
	perM := func(v *float64) float64 {
		if v == nil {
			return -1 // no explicit price: never equal to a published one
		}
		return *v * 1e6
	}
	same := func(got, want float64) bool { return got > want*0.999999 && got < want*1.000001 }
	for _, c := range cases {
		p, ok := store.MatchModelPrice(prices, c.model)
		if !ok {
			t.Errorf("%s: no price row", c.model)
			continue
		}
		got := [5]float64{p.InputPricePerToken * 1e6, perM(p.CacheWritePricePerToken), perM(p.CacheWrite1hPricePerToken), perM(p.CacheReadPricePerToken), p.OutputPricePerToken * 1e6}
		want := [5]float64{c.input, c.write, c.write1h, c.read, c.output}
		for i, name := range []string{"input", "5-minute cache write", "1-hour cache write", "cache read", "output"} {
			if want[i] != 0 && !same(got[i], want[i]) {
				t.Errorf("%s: %s = $%v/M, published $%v/M (row %q)", c.model, name, got[i], want[i], p.ModelPattern)
			}
		}
		if c.write1h == 0 && p.CacheWrite1hPricePerToken != nil {
			t.Errorf("%s: has a 1-hour cache write price, but its provider publishes none", c.model)
		}
	}
}

// An Anthropic or OpenAI row never leans on the estimated cache multiple
// (0.1x read, 1.25x write) by accident: Opus 5.5 reads at 0.05x, Fable 5.1 at
// 0.025x. The rows allowed to have no cache price are listed, with the reason.
func TestCatalogAnthropicAndOpenAIRowsHaveExplicitCachePrices(t *testing.T) {
	noCachePrice := []string{
		// Models that predate prompt caching: no cache tokens, no cache price.
		"claude-1.", "claude-2.", "claude-instant", "(gpt-)(35|3.5)", "(gpt-4)", "(gpt-4-", "(gpt-4(-",
		// OpenAI publishes no cached-input price for these.
		"-pro", "(gpt-4o-2024-05-13)",
		// Retired and gone from Anthropic's page: their cache prices could not
		// be checked on 2026-10-03, so they stay estimated (and labelled so).
		"claude-3-haiku-20240307", "claude-3-opus-20240229",
	}
	for _, p := range catalogPrices(t) {
		if !strings.Contains(p.ModelPattern, "claude") && !strings.Contains(p.ModelPattern, "openai/") {
			continue
		}
		if p.CacheReadPricePerToken != nil && p.CacheWritePricePerToken != nil {
			continue
		}
		if !slices.ContainsFunc(noCachePrice, func(s string) bool { return strings.Contains(p.ModelPattern, s) }) {
			t.Errorf("row %q has no explicit cache price: its cache cost would be estimated", p.ModelPattern)
		}
	}
}
