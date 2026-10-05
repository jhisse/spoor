package web

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBuildBaseline(t *testing.T) {
	format := func(v float64) string { return fmt.Sprintf("%.0f", v) }
	if buildBaseline("d", make([]float64, baselineMin-1), 1, format) != nil {
		t.Error("fewer than baselineMin values must give no baseline")
	}

	var values []float64
	for range 10 {
		values = append(values, 1, 1000)
	}
	low := buildBaseline("d", values, 1, format)
	if low.Percentile != 50 || low.N != 20 || low.Range != "1 to 1000" {
		t.Errorf("low = %+v, want percentile 50 of 20 over 1 to 1000", low)
	}
	// The marker sits under the first bin for 1 and under the last for 1000.
	if !strings.Contains(string(low.SVG), `<path d="M3 28`) {
		t.Errorf("low: marker not under bin 0: %s", low.SVG)
	}
	high := buildBaseline("d", values, 1000, format)
	if high.Percentile != 100 || !strings.Contains(string(high.SVG), `<path d="M91 28`) {
		t.Errorf("high = %d, %s; want percentile 100 and the marker under bin 11", high.Percentile, high.SVG)
	}
	for _, b := range []*baseline{low, high} {
		svg := string(b.SVG)
		if strings.Count(svg, "f-accent") != 1 || strings.Count(svg, "<path") != 1 || strings.Count(svg, "<rect") != baselineBins {
			t.Errorf("want %d bins with exactly one highlighted and marked: %s", baselineBins, svg)
		}
	}

}

func TestBuildBaselineLogScaleAndEdges(t *testing.T) {
	format := func(v float64) string { return fmt.Sprintf("%.0f", v) }
	var values []float64
	for range 10 {
		values = append(values, 1, 1000)
	}
	// Log scale: 10 lands a third of the way from 1 to 1000, in bin 4 of 12.
	if mid := buildBaseline("d", values, 10, format); !strings.Contains(string(mid.SVG), `<path d="M35 28`) {
		t.Errorf("mid: 10 between 1 and 1000 should mark bin 4: %s", mid.SVG)
	}
	// Degenerate inputs: all equal, zeros, and a value outside the range.
	same := buildBaseline("d", make([]float64, 30), 0, format)
	if same.Percentile != 100 || !strings.Contains(string(same.SVG), `<path d="M3 28`) {
		t.Errorf("all-zero values = %+v", same)
	}
	if out := buildBaseline("d", values, 5000, format); out.Percentile != 100 || !strings.Contains(string(out.SVG), `<path d="M91 28`) {
		t.Errorf("value above the range should clamp to the last bin: %+v", out)
	}
}

func TestDetailBaselineNeedsEnoughComparableSpans(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now()
	tr := seedTrace(t, s, "trace-1", now)
	add := func(i int) {
		sp := genSpan("full-model", nil)
		sp.TraceID, sp.ID, sp.Name = tr.ID, fmt.Sprintf("g%02d", i), "chat"
		sp.EndedAt = sp.StartedAt.Add(time.Duration(i+1) * 100 * time.Millisecond)
		sp.InputTokens = ptr(int64(1000 * (i + 1)))
		insertSpans(t, s, sp)
	}
	for i := range baselineMin - 1 {
		add(i)
	}
	few := doDetailQuery(t, h, tr.ID, "span=g00").Body.String()
	if !strings.Contains(few, "No basis for comparison: fewer than 20 spans") || strings.Contains(few, "percentile") {
		t.Error("19 comparable spans: want the no-basis sentence and no percentile")
	}

	add(baselineMin - 1)
	body := doDetailQuery(t, h, tr.ID, "span=g19").Body.String()
	for _, want := range []string{
		"Is this normal?", `aria-label="histogram of comparable spans, log scale"`,
		`<b>Duration: percentile 100</b><br>2.0 s, among 20 spans from 100 ms to 2.0 s (log scale)`,
		`<b>Input tokens: percentile 100</b><br>20,000, among 20 spans from 1,000 to 20,000 (log scale)`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("slowest of 20: body missing %q", want)
		}
	}
	if first := doDetailQuery(t, h, tr.ID, "span=g00").Body.String(); !strings.Contains(first, `<b>Duration: percentile 5</b>`) {
		t.Error("fastest of 20 should sit at percentile 5")
	}
}
