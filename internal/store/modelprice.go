package store

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"slices"
	"sync"
)

// compiledPatterns caches each model_pattern's compiled regex (nil for a
// malformed one): the price table has a few hundred rows and every span
// with a model is matched against all of them.
var compiledPatterns sync.Map

func compilePattern(pattern string) *regexp.Regexp {
	if re, ok := compiledPatterns.Load(pattern); ok {
		return re.(*regexp.Regexp)
	}
	re, _ := regexp.Compile(pattern)
	compiledPatterns.Store(pattern, re)
	return re
}

// The price table names base models, anchored (^…$). Senders decorate them,
// and a decorated name must reach its own model's row, never a neighbour's:
//   - variantTag: a bracketed suffix (Claude Code's "claude-opus-5-5[1m]");
//   - dateStamp: a snapshot date ("-20260915", "@20260915", "-2026-03-05"),
//     priced like the model it is a snapshot of unless it has its own row;
//   - versionDot: "5.5" for "5-5" (OpenRouter's "anthropic/claude-opus-5.5").
var (
	variantTag = regexp.MustCompile(`\[[^\]]*\]$`)
	dateStamp  = regexp.MustCompile(`[-@](\d{8}|\d{4}-\d{2}-\d{2})`)
	versionDot = regexp.MustCompile(`(\d)\.(\d)`)
)

// modelNameForms is the one place a model name is normalised for pricing:
// the name as sent, then with each decoration above removed in turn.
func modelNameForms(model string) [4]string {
	bare := variantTag.ReplaceAllString(model, "")
	undated := dateStamp.ReplaceAllString(bare, "")
	return [4]string{model, bare, undated, versionDot.ReplaceAllString(undated, "$1-$2")}
}

// MatchModelPrice picks the price row for a model: a row matches when its
// pattern matches any of modelNameForms; the longest matching pattern wins
// (a row written for a dated or tagged name beats the base model's), ties
// broken alphabetically so the result doesn't depend on row order. A
// malformed regex is skipped: it can only come from an operator's own
// INSERT, not from a payload.
func MatchModelPrice(prices []ModelPrice, model string) (ModelPrice, bool) {
	var (
		best  ModelPrice
		found bool
	)
	forms := modelNameForms(model)
	for _, p := range prices {
		re := compilePattern(p.ModelPattern)
		if re == nil || !slices.ContainsFunc(forms[:], re.MatchString) {
			continue
		}
		longer := len(p.ModelPattern) > len(best.ModelPattern)
		tie := len(p.ModelPattern) == len(best.ModelPattern) && p.ModelPattern < best.ModelPattern
		if !found || longer || tie {
			best, found = p, true
		}
	}
	return best, found
}

// matchVersion is part of every fingerprint. Bump it when MatchModelPrice or
// modelNameForms changes which row a name gets: spans priced under the
// earlier rule then show as needing a reprice, though no price changed.
const matchVersion = 1

// Fingerprint identifies p's prices and the matching rule: it is stored with
// each span p prices, and differs from today's once either has changed.
func (p ModelPrice) Fingerprint() string {
	h := fnv.New32a()
	_, _ = fmt.Fprintln(h, matchVersion, p.ModelPattern, p.InputPricePerToken, p.OutputPricePerToken, p.CacheReadPrice(), p.CacheWritePrice())
	return fmt.Sprintf("%08x", h.Sum32())
}

// SpanPricing is Spans spans of one model, priced by one row at one fingerprint.
type SpanPricing struct {
	Model, Pattern, Fingerprint string
	Spans                       int64
}

// StalePricings counts the spans today's table would not price the same way:
// another row now matches their model, or their row's prices changed.
func StalePricings(prices []ModelPrice, pricings []SpanPricing) (stale int64) {
	for _, sp := range pricings {
		if p, ok := MatchModelPrice(prices, sp.Model); !ok || p.Fingerprint() != sp.Fingerprint {
			stale += sp.Spans
		}
	}
	return stale
}

// A price row with no cache rate gets one estimated from its input price,
// at the multiples Anthropic publishes (read 0.1x, 5-minute write 1.25x).
// The blind-spots page tells the operator this is an estimate.
const (
	CacheReadMultiple  = 0.1
	CacheWriteMultiple = 1.25
)

func (p ModelPrice) CacheReadPrice() float64 {
	if p.CacheReadPricePerToken != nil {
		return *p.CacheReadPricePerToken
	}
	return p.InputPricePerToken * CacheReadMultiple
}

func (p ModelPrice) CacheWritePrice() float64 {
	if p.CacheWritePricePerToken != nil {
		return *p.CacheWritePricePerToken
	}
	return p.InputPricePerToken * CacheWriteMultiple
}

// TokenMix is a span's tokens in the four buckets that are priced, and
// drawn, separately. Reasoning tokens are part of Output.
type TokenMix struct {
	CacheRead, Fresh, CacheWrite, Output int64
}

// Parts lists the buckets in their one display order.
func (m TokenMix) Parts() [4]int64 { return [4]int64{m.CacheRead, m.Fresh, m.CacheWrite, m.Output} }

func (m TokenMix) Total() int64 { return m.CacheRead + m.Fresh + m.CacheWrite + m.Output }

func (m TokenMix) Add(o TokenMix) TokenMix {
	return TokenMix{m.CacheRead + o.CacheRead, m.Fresh + o.Fresh, m.CacheWrite + o.CacheWrite, m.Output + o.Output}
}

// Mix splits s's tokens. InputTokens is the whole prompt, cache included,
// so fresh input is what is left after the two cache buckets — floored at
// zero for an SDK that reports them disjoint. An unreported count is zero.
func (s Span) Mix() TokenMix {
	m := TokenMix{CacheRead: Deref(s.CacheReadTokens), CacheWrite: Deref(s.CacheWriteTokens), Output: Deref(s.OutputTokens)}
	m.Fresh = max(Deref(s.InputTokens)-m.CacheRead-m.CacheWrite, 0)
	return m
}

// CacheUnknown says s's cost was calculated by spoor with the whole prompt as
// fresh input, because the sender reported no cache count at all: had part
// of the prompt been read from the provider's cache the real cost is lower,
// had part been written to it, higher. The trace cannot say which.
func (s Span) CacheUnknown() bool {
	return s.PricePattern != nil && s.CacheReadTokens == nil && s.CacheWriteTokens == nil && s.InputTokens != nil && *s.InputTokens > 0
}

// Cost prices each bucket of m, in Parts order.
func (p ModelPrice) Cost(m TokenMix) [4]float64 {
	return [4]float64{
		float64(m.CacheRead) * p.CacheReadPrice(),
		float64(m.Fresh) * p.InputPricePerToken,
		float64(m.CacheWrite) * p.CacheWritePrice(),
		float64(m.Output) * p.OutputPricePerToken,
	}
}
