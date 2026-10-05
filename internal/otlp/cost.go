package otlp

import "github.com/jhisse/spoor/internal/store"

// priceSpan prices a span against model_prices, each bucket of its
// store.TokenMix at that bucket's price, and records the row that priced it
// and that row's fingerprint. A count the SDK didn't report adds nothing.
// No model, no token count at all, or no matching row leaves s untouched:
// cost_usd stays null, never a default.
func priceSpan(prices []store.ModelPrice, s *store.Span) {
	if s.Model == nil || (s.InputTokens == nil && s.OutputTokens == nil) {
		return
	}
	p, ok := store.MatchModelPrice(prices, *s.Model)
	if !ok {
		return
	}
	// One expression, not a sum over p.Cost(m): the compiler may fuse it
	// (arm64 does), and a rearranged sum would differ from costs already
	// stored by one ulp, which `spoor reprice` would report as a change.
	m := s.Mix()
	cost := float64(m.Fresh)*p.InputPricePerToken +
		float64(m.CacheRead)*p.CacheReadPrice() +
		float64(m.CacheWrite)*p.CacheWritePrice() +
		float64(m.Output)*p.OutputPricePerToken
	fingerprint := p.Fingerprint()
	s.CostUSD, s.PricePattern, s.PriceFingerprint = &cost, &p.ModelPattern, &fingerprint
}
