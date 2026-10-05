package web

import (
	"strings"
	"testing"

	"github.com/jhisse/spoor/internal/store"
)

func TestContextSparkSummarisesAndCapsItsBars(t *testing.T) {
	steps := make([]genStep, 50)
	var prev *store.Span
	for i := range steps {
		steps[i].Span = store.Span{InputTokens: i64Ptr(int64(100 + i)), OutputTokens: i64Ptr(1)}
		steps[i].Prev, prev = prev, &steps[i].Span
	}
	steps[10].Span.InputTokens = i64Ptr(5) // a smaller context than step 9's

	markSteps(steps, nil)
	steps[30].Mark, steps[30].Extra = "E", 0.25 // as a session's steps carry it

	got := buildContextSpark(steps, 7, 20)
	if want := "50 generations · peak 150 tokens · 1 smaller context (C)"; got.Summary() != want || got.Expiries != 1 || got.Extra != 0.25 {
		t.Errorf("summary = %q, want %q; %+v", got.Summary(), want, got)
	}
	// 50 steps in groups of 3: 17 bars, 7px apart; the tallest fills the
	// height; the group holding the expiry is underlined.
	svg := string(got.SVG)
	if !strings.HasPrefix(svg, `<svg width="117" height="23"`) || strings.Count(svg, `class="tk-out"`) != 17 || !strings.Contains(svg, `y="0.0"`) ||
		strings.Count(svg, `class="f-warn"`) != 1 || !strings.Contains(svg, `<rect x="70" y="21" width="5" height="2" class="f-warn"/>`) {
		t.Errorf("sparkline = %s", svg)
	}
	if none := buildContextSpark(make([]genStep, 2), 7, 20); none.Summary() != "2 generations · no token counts reported" || none.SVG != "" {
		t.Errorf("without token counts: %+v", none)
	}
}
