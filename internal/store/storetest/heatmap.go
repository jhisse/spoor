package storetest

import (
	"context"
	"math"
	"reflect"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// testTraceHeatmap covers TraceHeatmap and the Ranges filter together:
// a cell's count has to be what filtering to its range lists.
func testTraceHeatmap(t *testing.T, s store.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	seed := func(service, id string, at time.Time, d time.Duration, status store.Status, costs ...float64) {
		tr := newTrace(id, at)
		tr.EndedAt, tr.Status, tr.Service = at.Add(d), status, &service
		if err := s.MergeTrace(ctx, tr, true); err != nil {
			t.Fatalf("MergeTrace(%s): %v", id, err)
		}
		if err := s.InsertSpan(ctx, newSpan(id, "unpriced", at)); err != nil {
			t.Fatalf("InsertSpan(%s): %v", id, err)
		}
		for i := range costs {
			sp := newSpan(id, string(rune('a'+i)), at)
			sp.CostUSD = &costs[i]
			if err := s.InsertSpan(ctx, sp); err != nil {
				t.Fatalf("InsertSpan(%s): %v", id, err)
			}
		}
	}
	seed("main", "a", base.Add(10*time.Second), 2*time.Second, store.StatusOK, 0.015, 0.02)    // two spans: 0.035
	seed("main", "b", base.Add(20*time.Second), 2500*time.Millisecond, store.StatusError, 0.1) // exactly on a bound
	seed("main", "c", base.Add(5*time.Minute+30*time.Second), 40*time.Second, store.StatusOK)  // no cost: unknown
	seed("main", "d", base.Add(5*time.Minute+40*time.Second), 50*time.Millisecond, store.StatusOK, 0.02)
	seed("main", "free", base.Add(5*time.Minute+50*time.Second), 50*time.Millisecond, store.StatusOK, 0) // a cost of zero is known
	seed("other", "e", base.Add(30*time.Second), 2*time.Second, store.StatusOK, 0.02)

	heat := func(name string, m store.Measure, q store.TraceQuery, bounds []float64, want []store.HeatCell) {
		t.Helper()
		q.Service = "main"
		got, err := s.TraceHeatmap(ctx, q, m, base, 2*time.Minute, bounds)
		if err != nil {
			t.Fatalf("TraceHeatmap(%s): %v", name, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("TraceHeatmap(%s) = %+v, want %+v", name, got, want)
		}
	}
	// The bounds the UI sends start at 0 and end at +Inf.
	heat("duration", store.MeasureDuration, store.TraceQuery{}, []float64{0, 1, 10, math.Inf(1)},
		[]store.HeatCell{{X: 0, Y: 2, Count: 2, TraceID: "a"}, {X: 2, Y: 1, Count: 2, TraceID: "d"}, {X: 2, Y: 3, Count: 1, TraceID: "c"}})
	// The zero Measure is cost. b sits on the 0.1 bound and belongs to the
	// row that starts there; c has no cost and is in no cell.
	heat("cost", "", store.TraceQuery{}, []float64{0, 0.01, 0.1, math.Inf(1)},
		[]store.HeatCell{{X: 0, Y: 2, Count: 1, TraceID: "a"}, {X: 0, Y: 3, Count: 1, TraceID: "b"}, {X: 2, Y: 1, Count: 1, TraceID: "free"}, {X: 2, Y: 2, Count: 1, TraceID: "d"}})
	errStatus := store.StatusError
	heat("status filter", store.MeasureDuration, store.TraceQuery{Status: &errStatus}, []float64{0, 1, 10},
		[]store.HeatCell{{X: 0, Y: 2, Count: 1, TraceID: "b"}})
	heat("no match", store.MeasureCost, store.TraceQuery{Span: store.SpanFilter{Name: "no-such-span"}}, []float64{0, 1}, nil)

	list := func(name string, q store.TraceQuery, want ...string) {
		t.Helper()
		q.Service = "main"
		got, _, err := s.QueryTraces(ctx, q)
		if err != nil {
			t.Fatalf("QueryTraces(%s): %v", name, err)
		}
		ids := []string{}
		for _, tr := range got { // newest first
			ids = append(ids, tr.ID)
		}
		if !reflect.DeepEqual(ids, want) {
			t.Errorf("QueryTraces(%s) = %v, want %v", name, ids, want)
		}
		buckets, err := s.TraceHistogram(ctx, q, 3)
		if err != nil {
			t.Fatalf("TraceHistogram(%s): %v", name, err)
		}
		var n int64
		for _, b := range buckets {
			n += b.OK + b.Error + b.Unset
		}
		if n != int64(len(want)) {
			t.Errorf("TraceHistogram(%s) counts %d traces, want %d", name, n, len(want))
		}
	}
	list("no range: unknown cost is listed", store.TraceQuery{}, "free", "d", "c", "b", "a")
	rng := func(m store.Measure, lo, hi float64) store.TraceQuery {
		return store.TraceQuery{Ranges: map[store.Measure]store.Range{m: {Min: lo, Max: hi}}}
	}
	list("duration 1s to 10s", rng(store.MeasureDuration, 1, 10), "b", "a")
	list("duration under 1s", rng(store.MeasureDuration, 0, 1), "free", "d")
	list("duration from 10s, no upper bound", rng(store.MeasureDuration, 10, math.Inf(1)), "c")
	list("cost from 0.1: min is inclusive", rng(store.MeasureCost, 0.1, 0), "b")
	list("cost under 0.1: max is exclusive, unknown excluded, zero included", rng(store.MeasureCost, 0, 0.1), "free", "d", "a")
	list("two ranges at once", store.TraceQuery{Ranges: map[store.Measure]store.Range{store.MeasureCost: {Max: 0.1}, store.MeasureDuration: {Min: 1}}}, "a")
	// Tokens: a reports input only, d input and output; the others report none and are unknown.
	in, out := int64(900), int64(200)
	for id, sp := range map[string]store.Span{"a": {InputTokens: &in}, "d": {InputTokens: &in, OutputTokens: &out}} {
		sp.TraceID, sp.ID, sp.Kind, sp.Name, sp.Status, sp.StartedAt, sp.EndedAt, sp.CreatedAt = id, "gen", store.SpanKindGeneration, "gen", store.StatusOK, base, base, base
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatalf("InsertSpan(%s): %v", id, err)
		}
	}
	list("tokens from 1000", rng(store.MeasureTokens, 1000, 0), "d")
	list("tokens under 1000: unknown excluded", rng(store.MeasureTokens, 0, 1000), "a")
}
