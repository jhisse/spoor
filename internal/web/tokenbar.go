package web

import (
	"fmt"
	"html/template"
	"strconv"
	"strings"

	"github.com/jhisse/spoor/internal/store"
)

// TokenMix is the four-bucket split every token bar in the UI draws. One
// type and one renderer on purpose: the same bar appears in the span panel,
// the tree row, the session turn and the trace list, and must mean the same
// thing everywhere.
type TokenMix = store.TokenMix

// tokenBarSegments name and paint the buckets, in TokenMix.Parts order.
// attr paints an SVG shape (spoor.css's classes; cache read is the #hatch
// pattern of layout.html's sprite), sw is the legend swatch's class.
var tokenBarSegments = [4]struct{ label, attr, sw string }{
	{"cache read", `fill="url(#hatch)"`, "read"},
	{"fresh input", `class="tk-fresh"`, "fresh"},
	{"cache write", `class="tk-write"`, "write"},
	{"output", `class="tk-out"`, "out"},
}

// tokenBar renders m as a horizontal stacked bar, width px wide at
// scaleMax tokens — pass the largest total on the page so bars compare
// across rows, or 0 to fill the width. Empty string when there are no
// tokens, so a template can drop it in unconditionally. Segments keep a
// 1px gap, so each is judged against the surface, not its neighbour.
func tokenBar(m TokenMix, scaleMax int64, width, height int) template.HTML {
	total := m.Total()
	if total == 0 {
		return ""
	}
	scaleMax = max(scaleMax, total)
	var b strings.Builder
	// The viewBox lets a stylesheet stretch the bar (.tok-l); alone it changes nothing.
	fmt.Fprintf(&b, `<svg width="%d" height="%d" viewBox="0 0 %d %d" preserveAspectRatio="none" role="img" aria-label="%s tokens">`, width, height, width, height, commas(total))
	x := 0.0
	for i, v := range m.Parts() {
		if v == 0 {
			continue
		}
		seg, w := tokenBarSegments[i], max(float64(v)/float64(scaleMax)*float64(width), 1.5)
		fmt.Fprintf(&b, `<rect x="%.2f" width="%.2f" height="%d" %s><title>%s: %s</title></rect>`, x, max(w-1, 1), height, seg.attr, seg.label, commas(v))
		x += w
	}
	b.WriteString(`</svg>`)
	return template.HTML(b.String()) // #nosec G203 -- built from integers and fixed strings only
}

// legend names the buckets m actually has, as the children of a .legend
// element: a page whose data only carries input and output explains two
// swatches, not four. counts adds each bucket's exact count: the four
// numbers under the span panel's bar.
func legend(m TokenMix, counts bool) template.HTML {
	var b strings.Builder
	for i, v := range m.Parts() {
		if v == 0 {
			continue
		}
		seg := tokenBarSegments[i]
		fmt.Fprintf(&b, `<span><i class="sw %s"></i>%s`, seg.sw, seg.label)
		if counts {
			fmt.Fprintf(&b, ` <b>%s</b>`, commas(v))
		}
		b.WriteString(`</span>`)
	}
	return template.HTML(b.String()) // #nosec G203 -- integers and fixed strings only
}

// commas writes n with thousands separators: 12,100.
func commas(n int64) string {
	s := strconv.FormatInt(n, 10)
	for i := len(s) - 3; i > 0 && s[i-1] != '-'; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
