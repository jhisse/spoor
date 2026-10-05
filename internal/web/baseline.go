package web

import (
	"context"
	"fmt"
	"html/template"
	"math"
	"strings"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

const (
	baselineBins   = 12
	baselineMin    = 20 // fewer comparable spans than this is no basis
	baselineWindow = 30 * 24 * time.Hour
	baselineLimit  = 2000
)

// baseline is where one number of the span sits among comparable spans.
type baseline struct {
	Label, Value, Range string // Value is this span's own number
	Percentile, N       int    // Percentile: share of the N at or below this span
	SVG                 template.HTML
}

// buildBaseline bins values on a log scale between their extremes and
// marks x's bin. Values under 1 count as 1. Nil under baselineMin values.
func buildBaseline(label string, values []float64, x float64, format func(float64) string) *baseline {
	if len(values) < baselineMin {
		return nil
	}
	lo, hi, atOrBelow := math.Inf(1), 1.0, 0
	for _, v := range values {
		lo, hi = min(lo, max(v, 1)), max(hi, v)
		if v <= x {
			atOrBelow++
		}
	}
	bin := func(v float64) int {
		if hi <= lo {
			return 0
		}
		return max(min(int(math.Log(max(v, lo)/lo)/math.Log(hi/lo)*baselineBins), baselineBins-1), 0)
	}
	var counts [baselineBins]int
	peak := 0
	for _, v := range values {
		counts[bin(v)]++
		peak = max(peak, counts[bin(v)])
	}
	const barW, barH = 8, 26
	var b strings.Builder
	fmt.Fprintf(&b, `<svg width="%d" height="%d" role="img" aria-label="histogram of comparable spans, log scale">`, baselineBins*barW, barH+8)
	for i, c := range counts {
		h, class := (c*barH+peak-1)/peak, "f-dur"
		if i == bin(x) {
			class = "f-accent"
			fmt.Fprintf(&b, `<path d="M%d %d l3.5 6 h-7 z" class="f-ink"/>`, i*barW+3, barH+2)
		}
		fmt.Fprintf(&b, `<rect x="%d" y="%d" width="%d" height="%d" class="%s"><title>%d spans</title></rect>`, i*barW, barH-h, barW-1, h, class, c)
	}
	b.WriteString(`</svg>`)
	return &baseline{
		Label: label, Value: format(x), Range: format(lo) + " to " + format(hi), Percentile: atOrBelow * 100 / len(values), N: len(values),
		SVG: template.HTML(b.String()), // #nosec G203 -- built from integers and fixed strings only
	}
}

// spanExplain is what the span panel adds next to the selected span's numbers.
type spanExplain struct {
	Cost      *costBreakdown
	Health    []healthBadge
	Baselines []*baseline
}

func (h *Handlers) explainSpan(ctx context.Context, sp store.Span, out []chatMessage) (spanExplain, error) {
	e := spanExplain{Health: genHealth(sp, out)}
	prices, err := h.Store.ListModelPrices(ctx)
	if err != nil {
		return e, err
	}
	e.Cost = buildCostBreakdown(sp, prices)
	durations, inputTokens, err := h.Store.ComparableSpans(ctx, sp, time.Now().Add(-baselineWindow), baselineLimit)
	if err != nil {
		return e, err
	}
	var micros, tokens []float64
	for _, d := range durations {
		micros = append(micros, float64(d.Microseconds()))
	}
	for i := 0; sp.InputTokens != nil && i < len(inputTokens); i++ {
		tokens = append(tokens, float64(inputTokens[i]))
	}
	for _, b := range []*baseline{
		buildBaseline("Duration", micros, float64(sp.EndedAt.Sub(sp.StartedAt).Microseconds()),
			func(v float64) string { return fmtDuration(time.Duration(v) * time.Microsecond) }),
		buildBaseline("Input tokens", tokens, float64(store.Deref(sp.InputTokens)),
			func(v float64) string { return commas(int64(v)) }),
	} {
		if b != nil {
			e.Baselines = append(e.Baselines, b)
		}
	}
	return e, nil
}
