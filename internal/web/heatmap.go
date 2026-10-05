package web

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"

	"github.com/jhisse/spoor/internal/store"
)

// heatScale cuts a measure into the heat map's rows, in 1-3-10 steps: two
// rows per decade, a log scale with round edges. Row y is [y-1, y). param
// names the measure's range filter in the URL: ?<param>_min= and ?<param>_max=.
type heatScale struct {
	bounds []float64
	param  string
	label  func(float64) string
}

var measures = []store.Measure{store.MeasureCost, store.MeasureDuration, store.MeasureTokens}

var heatScales = map[store.Measure]heatScale{
	store.MeasureCost:     {[]float64{0, 0.0001, 0.0003, 0.001, 0.003, 0.01, 0.03, 0.1, 0.3, 1, 3, 10, math.Inf(1)}, "cost", func(v float64) string { return fmt.Sprintf("$%.4g", v) }},
	store.MeasureDuration: {[]float64{0, 0.1, 0.3, 1, 3, 10, 30, 100, 300, 1000, 3000, math.Inf(1)}, "dur", func(v float64) string { return fmt.Sprintf("%.4gs", v) }},
	store.MeasureTokens:   {[]float64{0, 100, 300, 1e3, 3e3, 1e4, 3e4, 1e5, 3e5, 1e6, 3e6, math.Inf(1)}, "tok", func(v float64) string { return fmtCount(int64(v)) }},
}

func fmtBound(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

func (sc heatScale) rangeText(lo, hi float64) string {
	if hi <= 0 || math.IsInf(hi, 1) {
		return sc.label(lo) + " or more"
	}
	return sc.label(lo) + " – " + sc.label(hi)
}

type heatmap struct {
	Measure      store.Measure
	Switch       []heatCell // "off" and one link per measure; Single marks the current one
	Cols         int
	Labels       []string // each row's lower bound, top row first
	Cells        []heatCell
	Max, Missing int64 // the fullest cell; traces whose measure is unknown
}

type heatCell struct {
	X, Y       int
	URL, Title string
	Step       int  // 1 to 6 on the heat ramp (class ht-N); 1 is the outline of a single trace
	Single     bool // holds one trace: drawn as an outline, links to it
}

// overview is the list's chart: the histogram and, when ?y= asks for it,
// the heat map on the same buckets, so its columns sit under the bars. Nil
// under two buckets. total is how many traces q's filters match.
func (h *Handlers) overview(ctx context.Context, current *url.URL, q store.TraceQuery, m store.Measure) (hist *histogram, total int64, err error) {
	buckets, err := h.Store.TraceHistogram(ctx, q, histogramBars)
	for _, b := range buckets {
		total += b.OK + b.Error + b.Unset
	}
	if hist = buildHistogram(current, buckets); err != nil || hist == nil {
		return hist, total, err
	}
	var cells []store.HeatCell
	if m != "" {
		cells, err = h.Store.TraceHeatmap(ctx, q, m, buckets[0].Start, buckets[0].End.Sub(buckets[0].Start), heatScales[m].bounds)
	}
	hist.Heat, hist.Open = buildHeatmap(current, m, buckets, cells), q.From != nil || q.To != nil || m != ""
	return hist, total, err
}

// buildHeatmap trims the grid to the rows that hold a trace. A cell with
// one trace links to it, so a sparse map reads as a scatter; any other
// links to the list filtered to its period and range.
func buildHeatmap(current *url.URL, m store.Measure, buckets []store.TraceBucket, cells []store.HeatCell) *heatmap {
	sc := heatScales[m]
	hm := &heatmap{Measure: m, Cols: len(buckets)}
	for _, name := range append([]store.Measure{""}, measures...) {
		hm.Switch = append(hm.Switch, heatCell{Title: cmp.Or(string(name), "off"), Single: name == m, URL: withQuery(current, map[string]string{"y": string(name), "cursor": ""})})
	}
	if m == "" {
		return hm
	}
	for _, b := range buckets {
		hm.Missing += b.OK + b.Error + b.Unset
	}
	lo, hi := len(sc.bounds), 1
	for _, c := range cells {
		lo, hi, hm.Max = min(lo, max(c.Y, 1)), max(hi, c.Y), max(hm.Max, c.Count)
	}
	for y := hi; y >= lo; y-- {
		hm.Labels = append(hm.Labels, sc.label(sc.bounds[y-1]))
	}
	for _, c := range cells {
		if c.X < 0 || c.X >= len(buckets) || c.Y == 0 { // ingested after the histogram was read; a negative measure
			continue
		}
		hm.Missing -= c.Count
		b, from, to := buckets[c.X], sc.bounds[c.Y-1], sc.bounds[c.Y]
		cell := heatCell{
			X: c.X, Y: hi - c.Y, Step: 1 + int(math.Ceil(5*float64(c.Count)/float64(hm.Max))),
			URL:   withQuery(current, map[string]string{"from": b.Start.Format(dateTimeLocalLayout), "to": b.End.Format(dateTimeLocalLayout), sc.param + "_min": fmtBound(from), sc.param + "_max": fmtBound(to), "cursor": "", "live": ""}),
			Title: fmt.Sprintf("%s – %s UTC · %s %s · traces: %d", b.Start.Format("2006-01-02 15:04"), b.End.Format("2006-01-02 15:04"), m, sc.rangeText(from, to), c.Count),
		}
		if c.Count == 1 {
			cell.Single, cell.Step, cell.URL = true, 1, "/traces/"+c.TraceID
		}
		hm.Cells = append(hm.Cells, cell)
	}
	return hm
}
