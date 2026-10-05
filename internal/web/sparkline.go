package web

import (
	"fmt"
	"html/template"
	"strings"
)

// contextSpark is a run of model calls as a word-sized drawing, one bar per
// call, with the counts written beside it: the closed "Context per step" row
// of the trace page, a row of the trace list, a card of its latest sessions.
// It counts the steps' marks; it detects nothing itself.
type contextSpark struct {
	Calls                    int
	Peak                     int64         // the largest call, in tokens
	Shrank, Breaks, Expiries int           // steps marked C, B and E
	Extra                    float64       // what the breaks and expiries with a price are estimated to have added
	Why                      string        // their sentences
	SVG                      template.HTML // empty when no call reports tokens
}

// sparkBars is the most bars drawn; a longer run shows the largest step
// of each group of consecutive ones.
const sparkBars, sparkHeight = 24, 20.0

func (s contextSpark) ExtraUSD() string { return usd4(s.Extra) }

// Summary is the trace page's line.
func (s contextSpark) Summary() string {
	if s.Peak == 0 {
		return fmt.Sprintf("%d generations · no token counts reported", s.Calls)
	}
	t := fmt.Sprintf("%d generations · peak %s tokens", s.Calls, fmtCount(s.Peak))
	if s.Shrank > 0 {
		t += fmt.Sprintf(" · %d smaller context (C)", s.Shrank)
	}
	if s.Breaks > 0 {
		t += fmt.Sprintf(" · %d cache break (B)", s.Breaks)
	}
	return t
}

// buildContextSpark draws steps pitch px apart on the scale of their own
// peak. A bar that lost its cache is underlined.
func buildContextSpark(steps []genStep, pitch int, height float64) contextSpark {
	s := contextSpark{Calls: len(steps)}
	for _, st := range steps {
		s.Peak = max(s.Peak, st.Span.Mix().Total())
		s.Shrank += strings.Count(st.Mark, "C")
		s.Breaks += strings.Count(st.Mark, "B")
		s.Expiries += strings.Count(st.Mark, "E")
		s.Extra += st.Extra
		if st.Break != "" {
			s.Why += " " + st.Break
		}
	}
	if s.Peak == 0 {
		return s
	}
	every := (len(steps) + sparkBars - 1) / sparkBars
	var b strings.Builder
	bars, width := 0, float64(pitch-1-pitch/6)
	for i := 0; i < len(steps); i += every {
		var mix TokenMix
		lost := false
		for _, st := range steps[i:min(i+every, len(steps))] {
			if m := st.Span.Mix(); m.Total() > mix.Total() {
				mix = m
			}
			lost = lost || strings.ContainsAny(st.Mark, "BE")
		}
		x := float64(bars * pitch)
		if mix.Total() > 0 { // never an invisible bar
			stackRects(&b, floats(mix.Parts()), float64(mix.Total()), x, width, height, max(float64(mix.Total())/float64(s.Peak)*height, 2))
		}
		if lost {
			fmt.Fprintf(&b, `<rect x="%.0f" y="%.0f" width="%.0f" height="2" class="f-warn"/>`, x, height+1, width)
		}
		bars++
	}
	s.SVG = template.HTML(fmt.Sprintf(`<svg width="%.0f" height="%.0f" aria-hidden="true">%s</svg>`, float64((bars-1)*pitch)+width, height+3, b.String())) // #nosec G203 -- numbers and fixed strings only
	return s
}
