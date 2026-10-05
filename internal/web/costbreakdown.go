package web

import (
	"fmt"
	"math"
	"regexp"

	"github.com/jhisse/spoor/internal/otlp"
	"github.com/jhisse/spoor/internal/store"
)

// costLine is one token bucket priced: Tokens × PerMillion = USD.
type costLine struct {
	Label           string
	Tokens          int64
	PerMillion, USD float64
	Estimated       bool // no cache rate in the price row; derived from its input price
}

// costBreakdown explains a generation's cost. Stored is what ingestion
// recorded, Computed is today's price table on the same tokens; when they
// differ both are shown, never reconciled.
type costBreakdown struct {
	Model, Pattern string // Pattern is "" when no price row matches
	PricedBy       string // the row recorded when Stored was calculated; "" if none was
	Stale          bool   // that row's fingerprint is not today's: the table changed since
	Lines          []costLine
	Computed       float64
	// Upper is Computed had every cache write been kept for 1 hour, at
	// Write1hPerMillion; zero when the span wrote no cache or the model has
	// no 1-hour rate. The trace does not say which lifetime was asked for,
	// so Computed (the 5-minute rate) to Upper is a range, shown as one.
	Upper, Write1hPerMillion float64
	Stored                   *float64
	Reported                 bool // the sender reported the cost; spoor did not calculate it
	// LongContext: the prompt is above longContextTokens on a model billed
	// more past that size; FastMode: the span was served in fast mode. The
	// table has one rate per model, so either makes the real cost higher.
	LongContext, FastMode bool
	CacheUnknown          bool // the sender reported no cache count: see store.Span.CacheUnknown
}

func (c costBreakdown) Differs() bool {
	return c.Pattern != "" && c.Stored != nil && (c.Stale || math.Abs(*c.Stored-c.Computed) > 1e-9)
}

func buildCostBreakdown(sp store.Span, prices []store.ModelPrice) *costBreakdown {
	if sp.Kind != store.SpanKindGeneration || sp.Model == nil {
		return nil
	}
	c := &costBreakdown{
		Model: *sp.Model, Stored: sp.CostUSD,
		Reported: otlp.CostReported(sp), FastMode: otlp.FastMode(sp), CacheUnknown: sp.CacheUnknown(),
		LongContext: store.Deref(sp.InputTokens) > longContextTokens && longContextTier.MatchString(*sp.Model),
	}
	p, ok := store.MatchModelPrice(prices, c.Model)
	if !ok {
		return c
	}
	c.Pattern, c.PricedBy = p.ModelPattern, store.Deref(sp.PricePattern)
	c.Stale = sp.PriceFingerprint != nil && *sp.PriceFingerprint != p.Fingerprint()
	m := sp.Mix()
	write := "cache write"
	if p.CacheWrite1hPricePerToken != nil {
		write = "cache write, 5-minute rate"
	}
	for _, l := range []costLine{ // the token bar's order
		{Label: "cache read", Tokens: m.CacheRead, PerMillion: p.CacheReadPrice(), Estimated: p.CacheReadPricePerToken == nil},
		{Label: "fresh input", Tokens: m.Fresh, PerMillion: p.InputPricePerToken},
		{Label: write, Tokens: m.CacheWrite, PerMillion: p.CacheWritePrice(), Estimated: p.CacheWritePricePerToken == nil},
		{Label: "output", Tokens: m.Output, PerMillion: p.OutputPricePerToken},
	} {
		if l.Tokens == 0 {
			continue
		}
		l.USD = float64(l.Tokens) * l.PerMillion
		l.PerMillion *= 1e6
		c.Lines = append(c.Lines, l)
		c.Computed += l.USD
	}
	if h := p.CacheWrite1hPricePerToken; h != nil && m.CacheWrite > 0 {
		c.Write1hPerMillion, c.Upper = *h*1e6, c.Computed+float64(m.CacheWrite)*(*h-p.CacheWritePrice())
	}
	return c
}

// longContextTokens is the prompt size above which Google bills its Gemini
// Pro models at a higher rate (https://ai.google.dev/gemini-api/docs/pricing).
// Claude 4.6 and later have no such tier.
const longContextTokens = 200_000

var longContextTier = regexp.MustCompile(`(?i)gemini.*-pro`)

// dollarsCaption is the sentence under a dollar chart of steps: it says what
// priced the bars, and names the 0.1x / 1.25x estimate only when a step's
// price row has no cache price, so the estimate was really used.
func dollarsCaption(steps []genStep, prices []store.ModelPrice) string {
	const caption = "The same steps in dollars, at list price from spoor's price table."
	for _, st := range steps {
		if p, ok := priceFor(prices, st.Span.Model); ok && (p.CacheReadPricePerToken == nil || p.CacheWritePricePerToken == nil) {
			return caption + " " + *st.Span.Model + " has no cache price there: its cache read is estimated at 0.1× and its cache write at 1.25× the input price."
		}
	}
	return caption
}

// usd has eight decimals, one token at $0.01 per million: at the list's
// four a small bucket reads $0.0000 and the lines do not visibly add up.
func usd(v float64) string { return fmt.Sprintf("$%.8f", v) }
