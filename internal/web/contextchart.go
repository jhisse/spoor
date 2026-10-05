package web

import (
	"fmt"
	"html/template"
	"math"
	"regexp"
	"strings"

	"github.com/jhisse/spoor/internal/store"
)

// contextChart is one vertical stacked bar per generation, in start order,
// drawn with tokenbar.go's buckets.
type contextChart struct {
	Steps       int
	Tokens      template.HTML
	Dollars     template.HTML // empty when some generation's model has no price
	DollarsNote string        // what priced the dollar bars
	Legend      template.HTML
	Breaks      []string // one sentence per cache break
	Notes       []string // what was left out, and why
	Spark       contextSpark
}

type chartStep struct {
	genStep
	Tokens   [4]float64
	Dollars  [4]float64
	Marker   string // "C" context shrank, "B" cache break
	Selected bool
}

// markSteps says what each step of a trace did to its context, against the
// previous call of its scope. It is the one definition the trace page's
// chart and the list's rows share.
func markSteps(steps []genStep, prices []store.ModelPrice) {
	for i := range steps {
		st := &steps[i]
		if st.Prev == nil {
			continue
		}
		st.Mark = ""
		if st.Span.InputTokens != nil && st.Prev.InputTokens != nil && *st.Span.InputTokens < *st.Prev.InputTokens {
			st.Mark = "C"
		}
		if b, isBreak := detectCacheBreak(st.Prev.Mix(), st.Span.Mix()); isBreak {
			st.Mark += "B"
			st.Break, st.Extra = b.sentence(i+1, prices, st.Span.Model)
		}
	}
}

func buildContextChart(steps []genStep, prices []store.ModelPrice, selectedID string) *contextChart {
	markSteps(steps, prices)
	c := &contextChart{Steps: len(steps), Spark: buildContextSpark(steps, 7, sparkHeight)}
	cs := make([]chartStep, len(steps))
	cum := make([]float64, len(steps))
	var total float64
	var unpriced, uncosted int
	hasCache := false
	var sum TokenMix
	for i, st := range steps {
		mix := st.Span.Mix()
		sum = sum.Add(mix)
		s := chartStep{genStep: st, Tokens: floats(mix.Parts()), Marker: st.Mark, Selected: st.Span.ID == selectedID}
		hasCache = hasCache || st.Span.CacheReadTokens != nil || st.Span.CacheWriteTokens != nil
		price, ok := priceFor(prices, st.Span.Model)
		if ok {
			s.Dollars = price.Cost(mix)
		} else {
			unpriced++
		}
		if st.Span.CostUSD != nil {
			total += *st.Span.CostUSD
		} else {
			uncosted++
		}
		cum[i] = total
		if st.Break != "" {
			c.Breaks = append(c.Breaks, st.Break)
		}
		cs[i] = s
	}
	if uncosted > 0 {
		cum = nil
		c.Notes = append(c.Notes, fmt.Sprintf("%d of %d generations have no stored cost: cumulative cost line omitted.", uncosted, len(steps)))
	}
	var crowded int
	c.Tokens, crowded = chartSVG(cs, func(s chartStep) [4]float64 { return s.Tokens }, chartTop+104+chartBottom, cum, "tokens")
	if unpriced == 0 {
		c.Dollars, _ = chartSVG(cs, func(s chartStep) [4]float64 { return s.Dollars }, chartTop+48+chartBottom, nil, "US$")
		c.DollarsNote = dollarsCaption(steps, prices)
	} else {
		c.Notes = append(c.Notes, fmt.Sprintf("%d of %d generations have no known price for the model: dollar band omitted.", unpriced, len(steps)))
	}
	if !hasCache {
		c.Notes = append(c.Notes, "These spans report no cache tokens: two categories only, input and output.")
	}
	if crowded > 0 {
		c.Notes = append(c.Notes, fmt.Sprintf("%d marks that would sit on top of the mark before them are not drawn; each is in its bar's tooltip.", crowded))
	}
	c.Legend = legend(sum, false) + chartKey(drawn(cs), cum != nil)
	return c
}

// drawn is every marker letter in cs, plus "T" for a turn label and "⋯" for
// a pause: what the legend has to explain.
func drawn(cs []chartStep) (seen string) {
	for _, s := range cs {
		seen += s.Marker
		if s.Label != "" {
			seen += "T"
		}
		if s.Pause != "" {
			seen += "⋯"
		}
	}
	return seen
}

// chartKey explains only what this chart drew: a legend entry for a mark
// that is nowhere on the chart sends the reader looking for it.
func chartKey(seen string, cumulative bool) template.HTML {
	var b strings.Builder
	if cumulative {
		b.WriteString(`<span><i class="sw line"></i>cumulative cost</span>`)
	}
	for _, k := range [][2]string{{"C", "smaller context"}, {"B", "cache break"}, {"E", "cache expiry"}} {
		if strings.Contains(seen, k[0]) {
			fmt.Fprintf(&b, `<span><b class="warn-t">%s</b> %s</span>`, k[0], k[1])
		}
	}
	if strings.Contains(seen, "T") {
		b.WriteString(`<span class="muted">T<i>n</i> = turn <i>n</i> starts</span>`)
	}
	if strings.Contains(seen, "⋯") {
		b.WriteString(`<span class="muted">⋯ 12 min ⋯ = no model call for that long</span>`)
	}
	return template.HTML(b.String()) // #nosec G203 -- fixed strings only
}

var chartLink = regexp.MustCompile(`<a href="([^"]+)"`)

// pick makes every bar select its span in place, the way a tree row does:
// #ctx-body in treeviews.html carries the rest of the htmx attributes.
// keep is "&fold=0" or "".
func (c *contextChart) pick(keep template.URL) {
	to := `<a href="${1}` + template.HTMLEscapeString(string(keep)) + `" hx-get="${1}` + template.HTMLEscapeString(string(keep)) + `"`
	c.Tokens = template.HTML(chartLink.ReplaceAllString(string(c.Tokens), to))   // #nosec G203 -- chartSVG's own output and an escaped constant
	c.Dollars = template.HTML(chartLink.ReplaceAllString(string(c.Dollars), to)) // #nosec G203 -- as above
}

func floats(v [4]int64) [4]float64 {
	return [4]float64{float64(v[0]), float64(v[1]), float64(v[2]), float64(v[3])}
}

func priceFor(prices []store.ModelPrice, model *string) (store.ModelPrice, bool) {
	if model == nil {
		return store.ModelPrice{}, false
	}
	return store.MatchModelPrice(prices, *model)
}

// cacheBreak is a generation that lost the prompt cache its predecessor
// had. ActualIn and CachedIn are the input side of the step in
// fresh-input-token equivalents: what it was, and what it would have been
// had the same input been read from cache at the predecessor's ratio.
type cacheBreak struct {
	PrevRatio, Ratio   float64
	ActualIn, CachedIn float64
}

// detectCacheBreak's thresholds are spoor's own choice, not a standard:
// the predecessor read at least half its input from cache, this step read
// at most half that share, and wrote at least as much as it read. A
// smaller prompt than the predecessor's is a different context (a
// subagent's first call, or a compaction), which has no cache to lose —
// calling that a break would put a wrong dollar figure on the page.
func detectCacheBreak(prev, cur TokenMix) (cacheBreak, bool) {
	prevIn := float64(prev.CacheRead + prev.Fresh + prev.CacheWrite)
	in := float64(cur.CacheRead + cur.Fresh + cur.CacheWrite)
	if prevIn == 0 || in < prevIn || cur.CacheWrite == 0 || cur.CacheWrite < cur.CacheRead {
		return cacheBreak{}, false
	}
	b := cacheBreak{PrevRatio: float64(prev.CacheRead) / prevIn, Ratio: float64(cur.CacheRead) / in}
	if b.PrevRatio < 0.5 || b.Ratio > b.PrevRatio/2 {
		return cacheBreak{}, false
	}
	read := b.PrevRatio * in
	rest := in - read
	notRead := float64(cur.Fresh + cur.CacheWrite)
	fresh, write := rest*float64(cur.Fresh)/notRead, rest*float64(cur.CacheWrite)/notRead
	b.ActualIn = float64(cur.Fresh) + float64(cur.CacheRead)*store.CacheReadMultiple + float64(cur.CacheWrite)*store.CacheWriteMultiple
	b.CachedIn = fresh + read*store.CacheReadMultiple + write*store.CacheWriteMultiple
	return b, true
}

// sentence describes the break and returns what it is estimated to have
// added to the input's cost, 0 when the model has no price.
func (b cacheBreak) sentence(step int, prices []store.ModelPrice, model *string) (string, float64) {
	s := fmt.Sprintf("Step %d: cache break — %.0f%% of input read from cache (previous step: %.0f%%).", step, b.Ratio*100, b.PrevRatio*100)
	price, priced := priceFor(prices, model)
	if !priced {
		return s + " No price for the model; cost not estimated.", 0
	}
	in := price.InputPricePerToken
	return s + fmt.Sprintf(" The input cost %s; at the previous step's cache rate it would cost %s (estimated).",
		usd4(b.ActualIn*in), usd4(b.CachedIn*in)), (b.ActualIn - b.CachedIn) * in
}

// Chart geometry, in px before any stretching. chartTop holds the turn
// labels and the room a marker needs above the tallest bar; chartBottom the
// step numbers; chartRight the cost line's last value. A chart too long for
// 22px bars is laid out chartFill wide and stretches with its container.
const (
	chartTop, chartBottom, chartRight = 28.0, 18.0, 64.0
	chartAxis, pauseSlot, chartFill   = 56.0, 96.0, 1160.0
	maxPitch, minPitch                = 28.0, 2.5 // 400 steps, the session chart's limit, still fit in chartFill
)

// barLayout is the room one step takes and the gap inside it: 22px bars
// while they fit in chartFill beside extra, shrinking to 2px ones.
func barLayout(n int, extra float64) (pitch, gap float64) {
	pitch = min(max((chartFill-extra)/float64(n), minPitch), maxPitch)
	return pitch, min(pitch/4, 6)
}

// stackRects draws one bar bottom-up in TokenMix.Parts order — cache
// read at the base, output on top — and returns the bar's top edge.
func stackRects(b *strings.Builder, vals [4]float64, scale, x, width, base, height float64) float64 {
	y := base
	for i, v := range vals {
		if v == 0 {
			continue
		}
		h := v / scale * height
		y -= h
		if h < 0.05 { // it would be written with a height of 0.0
			continue
		}
		fmt.Fprintf(b, `<rect x="%.1f" y="%.1f" width="%.1f" height="%.1f" %s/>`, x, max(y, 0), width, h, tokenBarSegments[i].attr) // max: rounding can leave the top at -1e-15, which prints as "-0.0"
	}
	return y
}

// chartExtent is the tallest step, which is the scale, and the room the
// chart needs beside its bars: the cost label and the labelled pauses.
func chartExtent(steps []chartStep, vals func(chartStep) [4]float64) (scale, extra float64) {
	extra = chartRight
	for _, s := range steps {
		v := vals(s)
		scale = max(scale, v[0]+v[1]+v[2]+v[3])
		if s.Pause != "" {
			extra += pauseSlot
		}
	}
	return scale, extra
}

// numberEvery is 1 when every step can carry its number, else 2, 5, ...:
// the bars are narrower than a number.
func numberEvery(pitch float64) int {
	for _, k := range []int{1, 2, 5, 10, 20, 50} {
		if float64(k)*pitch >= 24 {
			return k
		}
	}
	return 100
}

// plotWidth is the plot's own width while its bars are 22px or at the 2px
// floor (it then scrolls in a narrower container); between the two it was
// laid out to fill chartFill, and stretches with its container.
func plotWidth(pitch, total float64) string {
	if pitch < maxPitch && pitch > minPitch {
		return `class="fluid" width="100%"`
	}
	return fmt.Sprintf(`width="%.0f"`, total)
}

// chartAxisSVG is the left axis, a fixed-width sibling of the plot so its
// labels keep their size and place when the plot stretches.
func chartAxisSVG(scale, height float64, unit string) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<svg width="%.0f" height="%.0f" aria-hidden="true">`, chartAxis, height)
	for i, v := range []float64{0, scale / 2, scale} {
		fmt.Fprintf(&b, `<text x="%.0f" y="%.0f" text-anchor="end">%s</text>`, chartAxis-6, height-chartBottom-float64(i)*(height-chartBottom-chartTop)/2+4, template.HTMLEscapeString(fmtAmount(v, unit)))
	}
	return b.String() + `</svg>`
}

// markRow places the letters above the bars. One that would sit on top of
// the one before it is counted instead of drawn; the bar's tooltip still
// names it.
type markRow struct {
	freeX, lastY float64
	crowded      int
}

func (m *markRow) draw(w *strings.Builder, s chartStep, x, mid, y float64) {
	if s.Marker == "" {
		return
	}
	if x < m.freeX && math.Abs(y-m.lastY) < 10 {
		m.crowded++
		return
	}
	fmt.Fprintf(w, `<text x="%.2f%%" y="%.1f" text-anchor="middle" class="mark">%s</text>`, mid, y, s.Marker)
	m.freeX, m.lastY = x+8*float64(len(s.Marker)), y
}

// chartSVG renders one band: the axis, then a plot whose text and hairlines
// are placed in percent, so they keep their size, over a nested svg whose
// viewBox stretches the bars. Only the token band names turns, pauses and
// markers: the dollar band under it has the same columns. cum, when non-nil,
// is the cumulative cost after each step, drawn as a line on its own scale.
// It also returns how many marks were left out for lack of room.
func chartSVG(steps []chartStep, vals func(chartStep) [4]float64, height float64, cum []float64, unit string) (template.HTML, int) {
	scale, extra := chartExtent(steps, vals)
	if scale == 0 {
		return "", 0
	}
	var mr markRow
	pitch, gap := barLayout(len(steps), extra)
	total := float64(len(steps))*pitch + extra
	every := numberEvery(pitch)
	base, plot := height-chartBottom, height-chartBottom-chartTop
	pct := func(x float64) float64 { return x / total * 100 }
	var marks, bars strings.Builder // marks: text and hairlines, in percent; bars: the stretched geometry
	names := namesOf(unit, &marks)
	var line []string
	var x, labelFree float64
	for i, s := range steps {
		if s.Pause != "" {
			fmt.Fprintf(names, `<text x="%.2f%%" y="%.0f" text-anchor="middle" class="mute">⋯ %s ⋯</text>`, pct(x+pauseSlot/2), chartTop+plot/2, s.Pause)
			x += pauseSlot
		}
		sepTop := 16.0                       // under the label row, unless this separator carries a label
		if s.Label != "" && x >= labelFree { // a label that would run into the one before it is left out
			fmt.Fprintf(names, `<text x="%.2f%%" dx="3" y="11">%s</text>`, pct(x), template.HTMLEscapeString(s.Label))
			labelFree, sepTop = x+26, 2
		}
		if i > 0 && s.Scope != steps[i-1].Scope {
			fmt.Fprintf(&marks, `<line x1="%.2f%%" x2="%.2f%%" y1="%.0f" y2="%.0f" class="grid-l"/>`, pct(x), pct(x), sepTop, base)
		}
		v, mid := vals(s), pct(x+pitch/2)
		fmt.Fprintf(&bars, `<a href="%s" tabindex="-1"><title>%s</title>`, template.HTMLEscapeString(spanURL(s.Span)), template.HTMLEscapeString(stepTitle(i+1, s, v, unit)))
		top := stackRects(&bars, v, scale, x+gap/2, pitch-gap, base, plot)
		if s.Selected {
			fmt.Fprintf(&bars, `<rect x="%.1f" y="%.1f" width="%.1f" height="2" class="f-ink"/>`, x+gap/2, base+1, pitch-gap)
		}
		bars.WriteString(`</a>`)
		mr.draw(names, s, x, mid, top-3)
		if (i+1)%every == 0 {
			fmt.Fprintf(&marks, `<text x="%.2f%%" y="%.0f" text-anchor="middle">%d</text>`, mid, height-4, i+1)
		}
		if cum != nil && cum[len(cum)-1] > 0 {
			line = append(line, fmt.Sprintf("%.1f,%.1f", x+pitch/2, base-cum[i]/cum[len(cum)-1]*plot))
		}
		x += pitch
	}
	if line != nil {
		fmt.Fprintf(&bars, `<polyline points="%s" class="cum" vector-effect="non-scaling-stroke"/>`, strings.Join(line, " "))
		fmt.Fprintf(&marks, `<text x="%.2f%%" dx="4" y="%.0f" class="f-ink">%s</text>`, pct(x), chartTop+4, template.HTMLEscapeString(usd4(cum[len(cum)-1])))
	}
	var grid strings.Builder
	for _, y := range []float64{base, base - plot/2, chartTop} {
		fmt.Fprintf(&grid, `<line x1="0" x2="%.2f%%" y1="%.1f" y2="%.1f" class="grid-l"/>`, pct(x), y, y)
	}
	return template.HTML(fmt.Sprintf(`<div class="ctx">%s<svg %s height="%.0f" role="img" aria-label="context per step, %s, 0 to %s">%s<svg width="100%%" height="100%%" viewBox="0 0 %.0f %.0f" preserveAspectRatio="none">%s</svg>%s</svg></div>`,
		chartAxisSVG(scale, height, unit), plotWidth(pitch, total), height, unit, template.HTMLEscapeString(fmtAmount(scale, unit)), grid.String(), total, height, bars.String(), marks.String())), mr.crowded // #nosec G203 -- numbers, fixed strings, and an escaped href and title
}

// namesOf is where a band writes its turn labels, pause labels and markers:
// among its marks for the token band, nowhere for any other.
func namesOf(unit string, marks *strings.Builder) *strings.Builder {
	if unit == "tokens" {
		return marks
	}
	return &strings.Builder{}
}

func fmtAmount(v float64, unit string) string {
	if unit == "US$" {
		return usd4(v)
	}
	return fmtCount(int64(v))
}

func stepTitle(n int, s chartStep, vals [4]float64, unit string) string {
	t := fmt.Sprintf("step %d · %s", n, s.Span.Name)
	if s.ScopeName != "" {
		t += " · " + s.ScopeName
	}
	if s.Marker != "" {
		t += " · marked " + s.Marker
	}
	for i, seg := range tokenBarSegments {
		if vals[i] > 0 {
			t += fmt.Sprintf(" · %s %s", seg.label, fmtAmount(vals[i], unit))
		}
	}
	return t
}
