// Package storetest is a Store conformance suite written only against the
// store.Store interface: any adapter must pass it.
package storetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// Run exercises every Store method against a fresh instance per subtest.
// newStore returns a migrated Store and registers its own cleanup.
func Run(t *testing.T, newStore func(t *testing.T) store.Store) {
	t.Helper()
	t.Run("Ping", func(t *testing.T) { testPing(t, newStore(t)) })
	t.Run("InsertSpanIdempotentFirstWriteWins", func(t *testing.T) { testInsertSpanIdempotentFirstWriteWins(t, newStore(t)) })
	t.Run("GetTraceReturnsSpansOrderedByStart", func(t *testing.T) { testGetTraceReturnsSpansOrderedByStart(t, newStore(t)) })
	t.Run("GenerationSpanCarriesCostAndTokens", func(t *testing.T) { testGenerationSpanCarriesCostAndTokens(t, newStore(t)) })
	t.Run("GetTraceByID", func(t *testing.T) { testGetTraceByID(t, newStore(t)) })
	t.Run("GetTraceByIDNotFound", func(t *testing.T) { testGetTraceByIDNotFound(t, newStore(t)) })
	t.Run("QueryTracesByService", func(t *testing.T) { testQueryTracesByService(t, newStore(t)) })
	t.Run("SweepExpired", func(t *testing.T) { testSweepExpired(t, newStore(t)) })
	t.Run("AdjacentTraces", func(t *testing.T) { testAdjacentTraces(t, newStore(t)) })
	t.Run("QueryTracesPagination", func(t *testing.T) { testQueryTracesPagination(t, newStore(t)) })
	t.Run("QueryTracesFilterComposition", func(t *testing.T) { testQueryTracesFilterComposition(t, newStore(t)) })
	t.Run("QueryTracesAggregatesModelsAndTokens", func(t *testing.T) { testQueryTracesAggregatesModelsAndTokens(t, newStore(t)) })
	t.Run("GetTraceSummary", func(t *testing.T) { testGetTraceSummary(t, newStore(t)) })
	t.Run("SpanAttributesRoundTrip", func(t *testing.T) { testSpanAttributesRoundTrip(t, newStore(t)) })
	t.Run("QueryTracesCostNilWhenNoSpanHasCost", func(t *testing.T) { testQueryTracesCostNilWhenNoSpanHasCost(t, newStore(t)) })
	t.Run("QueryTracesRootless", func(t *testing.T) { testQueryTracesRootless(t, newStore(t)) })
	t.Run("QuerySessionsAggregatesAndFilters", func(t *testing.T) { testQuerySessionsAggregatesAndFilters(t, newStore(t)) })
	t.Run("QuerySessionsPagination", func(t *testing.T) { testQuerySessionsPagination(t, newStore(t)) })
	t.Run("GetSession", func(t *testing.T) { testGetSession(t, newStore(t)) })
	t.Run("SummariesSumCacheTokens", func(t *testing.T) { testSummariesSumCacheTokens(t, newStore(t)) })
	t.Run("SummaryCountsCostsPricedWithoutCacheCounts", func(t *testing.T) { testSummaryCacheUnknown(t, newStore(t)) })
	t.Run("QueryTracesFromIncludesSameSecond", func(t *testing.T) { testQueryTracesFromIncludesSameSecond(t, newStore(t)) })
	t.Run("QueryTracesSameStartPaginatesByID", func(t *testing.T) { testQueryTracesSameStartPaginatesByID(t, newStore(t)) })
	t.Run("TraceHistogram", func(t *testing.T) { testTraceHistogram(t, newStore(t)) })
	t.Run("TraceHeatmap", func(t *testing.T) { testTraceHeatmap(t, newStore(t)) })
	t.Run("ListGenerationModels", func(t *testing.T) { testListGenerationModels(t, newStore(t)) })
	t.Run("BlindSpots", func(t *testing.T) { testBlindSpots(t, newStore(t)) })
	t.Run("CostSince", func(t *testing.T) { testCostSince(t, newStore(t)) })
	t.Run("ComparableSpans", func(t *testing.T) { testComparableSpans(t, newStore(t)) })
	t.Run("MergeTrace", func(t *testing.T) { testMergeTrace(t, newStore(t)) })
	t.Run("MergeTraceConcurrent", func(t *testing.T) { testMergeTraceConcurrent(t, newStore(t)) })
	t.Run("IngestBatch", func(t *testing.T) { testIngestBatch(t, newStore) })
	t.Run("QuerySpans", func(t *testing.T) { testQuerySpans(t, newStore(t)) })
	t.Run("UpdateSpanUsage", func(t *testing.T) { testUpdateSpanUsage(t, newStore(t)) })
	t.Run("SpanPricings", func(t *testing.T) { testSpanPricings(t, newStore(t)) })
	t.Run("TimestampOrderIsTimeOrder", func(t *testing.T) { testTimestampOrderIsTimeOrder(t, newStore(t)) })
	t.Run("SpanStatusMessageEventsAndLinks", func(t *testing.T) { testSpanStatusMessageEventsAndLinks(t, newStore(t)) })
	t.Run("SearchSpans", func(t *testing.T) { testSearchSpans(t, newStore(t)) })
	t.Run("SearchSpansFollowsWrites", func(t *testing.T) { testSearchSpansFollowsWrites(t, newStore(t)) })
}

// A span's status text, events and links come back as written from both
// span reads; a span without them reads back nil, not empty; and a retry
// carrying different events does not replace the first write.
func testSpanStatusMessageEventsAndLinks(t *testing.T, s store.Store) {
	ctx := context.Background()
	now := time.Now().UTC()
	tr := newTrace("trace-1", now)
	if err := s.MergeTrace(ctx, tr, true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}
	msg := "exit 127"
	events := `[{"name":"exception","time":"2026-10-03T12:00:00.25Z","attributes":{"exception.message":"command not found"}}]`
	links := `[{"trace_id":"ff","span_id":"aa"}]`
	failed := newSpan(tr.ID, "failed", now)
	failed.Status, failed.StatusMessage = store.StatusError, &msg
	failed.Events, failed.Links = json.RawMessage(events), json.RawMessage(links)
	retry := failed
	retry.Events = json.RawMessage(`[]`)
	for _, sp := range []store.Span{failed, retry, newSpan(tr.ID, "plain", now.Add(time.Second))} {
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatalf("InsertSpan: %v", err)
		}
	}

	_, byTrace, err := s.GetTraceByID(ctx, tr.ID)
	if err != nil {
		t.Fatalf("GetTraceByID: %v", err)
	}
	queried, err := s.QuerySpans(ctx, store.SpanQuery{})
	if err != nil {
		t.Fatalf("QuerySpans: %v", err)
	}
	for _, spans := range [][]store.Span{byTrace, queried} {
		if len(spans) != 2 {
			t.Fatalf("got %d spans, want 2", len(spans))
		}
		if got := spans[0]; store.Deref(got.StatusMessage) != msg || string(got.Events) != events || string(got.Links) != links {
			t.Errorf("failed span: status message %v, events %s, links %s", got.StatusMessage, got.Events, got.Links)
		}
		if got := spans[1]; got.StatusMessage != nil || got.Events != nil || got.Links != nil {
			t.Errorf("plain span: status message %v, events %s, links %s; want all nil", got.StatusMessage, got.Events, got.Links)
		}
	}
}

func testPing(t *testing.T, s store.Store) {
	ctx := context.Background()
	if err := s.Ping(ctx); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func testInsertSpanIdempotentFirstWriteWins(t *testing.T, s store.Store) {
	ctx := context.Background()

	tr := newTrace("trace-1", time.Now())
	if err := s.MergeTrace(ctx, tr, true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}

	sp := newSpan(tr.ID, "span-1", time.Now())
	sp.Name = "first"
	if err := s.InsertSpan(ctx, sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	retry := sp
	retry.Name = "retried-with-different-data"
	if err := s.InsertSpan(ctx, retry); err != nil {
		t.Fatalf("InsertSpan (retry): %v", err)
	}

	_, spans, err := s.GetTraceByID(ctx, tr.ID)
	if err != nil {
		t.Fatalf("GetTraceByID: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1 (retry must not duplicate)", len(spans))
	}
	if spans[0].Name != "first" {
		t.Errorf("Name = %q, want first (first write should win)", spans[0].Name)
	}
}

func testGetTraceReturnsSpansOrderedByStart(t *testing.T, s store.Store) {
	ctx := context.Background()

	tr := newTrace("trace-1", time.Now())
	if err := s.MergeTrace(ctx, tr, true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}

	now := time.Now()
	second := newSpan(tr.ID, "span-2", now.Add(time.Second))
	first := newSpan(tr.ID, "span-1", now)
	if err := s.InsertSpan(ctx, second); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}
	if err := s.InsertSpan(ctx, first); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	_, spans, err := s.GetTraceByID(ctx, tr.ID)
	if err != nil {
		t.Fatalf("GetTraceByID: %v", err)
	}
	if len(spans) != 2 {
		t.Fatalf("got %d spans, want 2", len(spans))
	}
	if spans[0].ID != first.ID || spans[1].ID != second.ID {
		t.Errorf("span order = [%s, %s], want [%s, %s]", spans[0].ID, spans[1].ID, first.ID, second.ID)
	}
}

func testGenerationSpanCarriesCostAndTokens(t *testing.T, s store.Store) {
	ctx := context.Background()

	tr := newTrace("trace-1", time.Now())
	if err := s.MergeTrace(ctx, tr, true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}

	model := "gpt-4"
	inputTokens := int64(100)
	outputTokens := int64(50)
	cost := 0.0123
	sp := newSpan(tr.ID, "span-1", time.Now())
	sp.Kind = store.SpanKindGeneration
	sp.Model = &model
	sp.InputTokens = &inputTokens
	sp.OutputTokens = &outputTokens
	sp.CostUSD = &cost
	cacheRead, cacheWrite, reasoning := int64(70), int64(20), int64(30)
	sp.CacheReadTokens = &cacheRead
	sp.CacheWriteTokens = &cacheWrite
	sp.ReasoningTokens = &reasoning
	if err := s.InsertSpan(ctx, sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	_, spans, err := s.GetTraceByID(ctx, tr.ID)
	if err != nil {
		t.Fatalf("GetTraceByID: %v", err)
	}
	if len(spans) != 1 {
		t.Fatalf("got %d spans, want 1", len(spans))
	}
	got := spans[0]
	if got.Kind != store.SpanKindGeneration {
		t.Errorf("Kind = %q, want generation", got.Kind)
	}
	if got.Model == nil || *got.Model != model {
		t.Errorf("Model = %v, want %q", got.Model, model)
	}
	assertInt64Ptr(t, "CacheReadTokens", got.CacheReadTokens, cacheRead)
	assertInt64Ptr(t, "CacheWriteTokens", got.CacheWriteTokens, cacheWrite)
	assertInt64Ptr(t, "ReasoningTokens", got.ReasoningTokens, reasoning)
	if got.CostUSD == nil || *got.CostUSD != cost {
		t.Errorf("CostUSD = %v, want %v", got.CostUSD, cost)
	}
}

func assertInt64Ptr(t *testing.T, name string, got *int64, want int64) {
	t.Helper()
	if got == nil || *got != want {
		t.Errorf("%s = %v, want %d", name, got, want)
	}
}

func newTrace(id string, startedAt time.Time) store.Trace {
	return store.Trace{
		ID:        id,
		Name:      id,
		StartedAt: startedAt,
		EndedAt:   startedAt.Add(time.Second),
		Status:    store.StatusOK,
		CreatedAt: startedAt,
	}
}

func newSpan(traceID, id string, startedAt time.Time) store.Span {
	return store.Span{
		TraceID:   traceID,
		ID:        id,
		Kind:      store.SpanKindGeneric,
		Name:      id,
		StartedAt: startedAt,
		EndedAt:   startedAt.Add(time.Second),
		Status:    store.StatusOK,
		CreatedAt: startedAt,
	}
}

func testGetTraceByID(t *testing.T, s store.Store) {
	ctx := context.Background()

	tr := newTrace("trace-1", time.Now())
	if err := s.MergeTrace(ctx, tr, true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}
	sp := newSpan(tr.ID, "span-1", time.Now())
	if err := s.InsertSpan(ctx, sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	got, spans, err := s.GetTraceByID(ctx, tr.ID)
	if err != nil {
		t.Fatalf("GetTraceByID: %v", err)
	}
	if got.ID != tr.ID || got.Service != nil {
		t.Errorf("GetTraceByID = %+v, want match for %+v", got, tr)
	}
	if len(spans) != 1 || spans[0].ID != sp.ID {
		t.Errorf("GetTraceByID spans = %+v, want [%+v]", spans, sp)
	}
}

func testGetTraceByIDNotFound(t *testing.T, s store.Store) {
	ctx := context.Background()

	_, _, err := s.GetTraceByID(ctx, "does-not-exist")
	if !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetTraceByID error = %v, want ErrNotFound", err)
	}
}

// The service filter narrows the list and the menu, and pages by keyset
// inside the service: a trace without a service is in neither.
func testQueryTracesByService(t *testing.T, s store.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	api, worker := "api", "worker"
	for i, service := range []*string{&api, &worker, &api, nil, &api} {
		tr := newTrace(fmt.Sprintf("t%d", i), base.Add(time.Duration(i)*time.Second))
		tr.Service = service
		if err := s.MergeTrace(ctx, tr, true); err != nil {
			t.Fatalf("MergeTrace: %v", err)
		}
	}

	var got string
	var cursor *store.Cursor
	for {
		page, next, err := s.QueryTraces(ctx, store.TraceQuery{Service: "api", Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("QueryTraces: %v", err)
		}
		for _, tr := range page {
			got += tr.ID + ":" + store.Deref(tr.Service) + " "
		}
		if cursor = next; next == nil {
			break
		}
	}
	if got != "t4:api t2:api t0:api " {
		t.Errorf("service api, two per page = %q, want t4, t2, t0", got)
	}
	if all, _, err := s.QueryTraces(ctx, store.TraceQuery{}); err != nil || len(all) != 5 || all[1].Service != nil {
		t.Errorf("no filter = %+v, %v; want all five, t3 without a service", all, err)
	}
	if services, err := s.ListServices(ctx); err != nil || fmt.Sprint(services) != "[api worker]" {
		t.Errorf("ListServices = %v, %v; want [api worker]", services, err)
	}
}

// Retention goes by trace: an expired trace takes all its spans, also the
// one a later batch stored after the cutoff; a kept trace keeps its spans,
// also an old one; a span whose trace is gone goes too.
func testSweepExpired(t *testing.T, s store.Store) {
	ctx := context.Background()
	now := time.Now()
	old, recent := now.Add(-48*time.Hour), now.Add(-time.Hour)
	for _, tr := range []store.Trace{newTrace("expired", old), newTrace("kept", recent)} {
		if err := s.MergeTrace(ctx, tr, true); err != nil {
			t.Fatalf("MergeTrace: %v", err)
		}
	}
	for _, sp := range []store.Span{
		newSpan("expired", "first-batch", old),
		newSpan("expired", "later-batch", recent),
		newSpan("kept", "span", recent),
		newSpan("kept", "backdated", old),
		newSpan("never-had-a-trace", "orphan", recent),
	} {
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatalf("InsertSpan: %v", err)
		}
	}

	traces, spans, err := s.SweepExpired(ctx, now.Add(-24*time.Hour))
	if err != nil || traces != 1 || spans != 3 {
		t.Errorf("SweepExpired = %d traces, %d spans, %v; want 1 and 3", traces, spans, err)
	}
	if _, _, err := s.GetTraceByID(ctx, "expired"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("expired trace: err = %v, want ErrNotFound", err)
	}
	left, err := s.QuerySpans(ctx, store.SpanQuery{})
	if err != nil || len(left) != 2 || left[0].TraceID != "kept" || left[1].TraceID != "kept" {
		t.Errorf("spans left = %+v, %v; want the two of the kept trace", left, err)
	}
}

func testAdjacentTraces(t *testing.T, s store.Store) {
	ctx := context.Background()

	traces := seedTracesSecondsApart(t, s, 3, time.Now())
	oldest, middle, newest := traces[0], traces[1], traces[2]

	assertAdjacent := func(t *testing.T, cur store.Trace, wantPrev, wantNext *store.Trace) {
		t.Helper()
		prev, next, err := s.AdjacentTraces(ctx, store.Cursor{StartedAt: cur.StartedAt, ID: cur.ID})
		if err != nil {
			t.Fatalf("AdjacentTraces(%s): %v", cur.ID, err)
		}
		gotID := func(tr *store.Trace) string {
			if tr == nil {
				return "<nil>"
			}
			return tr.ID
		}
		wantID := func(tr *store.Trace) string {
			if tr == nil {
				return "<nil>"
			}
			return tr.ID
		}
		if gotID(prev) != wantID(wantPrev) {
			t.Errorf("AdjacentTraces(%s) prev = %s, want %s", cur.ID, gotID(prev), wantID(wantPrev))
		}
		if gotID(next) != wantID(wantNext) {
			t.Errorf("AdjacentTraces(%s) next = %s, want %s", cur.ID, gotID(next), wantID(wantNext))
		}
	}

	assertAdjacent(t, middle, &newest, &oldest)
	assertAdjacent(t, newest, nil, &middle)
	assertAdjacent(t, oldest, &middle, nil)
}

// seedTracesSecondsApart inserts n traces "trace-0".."trace-(n-1)", one
// second apart, trace-(n-1) newest, and returns them in insertion order.
func seedTracesSecondsApart(t *testing.T, s store.Store, n int, base time.Time) []store.Trace {
	t.Helper()
	traces := make([]store.Trace, n)
	for i := range n {
		tr := newTrace(fmt.Sprintf("trace-%d", i), base.Add(time.Duration(i)*time.Second))
		if err := s.MergeTrace(context.Background(), tr, true); err != nil {
			t.Fatalf("MergeTrace(%s): %v", tr.ID, err)
		}
		traces[i] = tr
	}
	return traces
}

func assertTracePage(t *testing.T, got []store.TraceSummary, next *store.Cursor, want []store.Trace, wantMore bool) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("page = %+v, want ids matching %+v", got, want)
	}
	for i, w := range want {
		if got[i].ID != w.ID {
			t.Fatalf("page[%d].ID = %q, want %q (page: %+v)", i, got[i].ID, w.ID, got)
		}
	}
	if wantMore && next == nil {
		t.Fatal("next cursor = nil, want non-nil")
	}
	if !wantMore && next != nil {
		t.Errorf("next cursor = %+v, want nil", next)
	}
}

func testQueryTracesPagination(t *testing.T, s store.Store) {
	ctx := context.Background()

	// trace-4 is newest (started_at = now+4s) ... trace-0 is oldest.
	traces := seedTracesSecondsApart(t, s, 5, time.Now())

	page1, cursor1, err := s.QueryTraces(ctx, store.TraceQuery{Limit: 2})
	if err != nil {
		t.Fatalf("QueryTraces page1: %v", err)
	}
	assertTracePage(t, page1, cursor1, []store.Trace{traces[4], traces[3]}, true)

	page2, cursor2, err := s.QueryTraces(ctx, store.TraceQuery{Limit: 2, Cursor: cursor1})
	if err != nil {
		t.Fatalf("QueryTraces page2: %v", err)
	}
	assertTracePage(t, page2, cursor2, []store.Trace{traces[2], traces[1]}, true)

	page3, cursor3, err := s.QueryTraces(ctx, store.TraceQuery{Limit: 2, Cursor: cursor2})
	if err != nil {
		t.Fatalf("QueryTraces page3: %v", err)
	}
	assertTracePage(t, page3, cursor3, []store.Trace{traces[0]}, false)
}

func testQueryTracesFilterComposition(t *testing.T, s store.Store) {
	ctx := context.Background()

	base := time.Now()
	ok1 := newTrace("checkout-flow", base)
	ok1.Name = "checkout-flow"
	failed := newTrace("checkout-failed", base.Add(time.Second))
	failed.Name = "checkout-failed"
	failed.Status = store.StatusError
	other := newTrace("signup-flow", base.Add(2*time.Second))
	other.Name = "signup-flow"

	for _, tr := range []store.Trace{ok1, failed, other} {
		if err := s.MergeTrace(ctx, tr, true); err != nil {
			t.Fatalf("MergeTrace(%s): %v", tr.ID, err)
		}
	}

	errStatus := store.StatusError
	byStatus, _, err := s.QueryTraces(ctx, store.TraceQuery{Status: &errStatus})
	if err != nil {
		t.Fatalf("QueryTraces(status): %v", err)
	}
	if len(byStatus) != 1 || byStatus[0].ID != failed.ID {
		t.Errorf("QueryTraces(status=error) = %+v, want [%s]", byStatus, failed.ID)
	}

	// From just after ok1: only failed and other qualify.
	from := base.Add(500 * time.Millisecond)
	byTime, _, err := s.QueryTraces(ctx, store.TraceQuery{From: &from})
	if err != nil {
		t.Fatalf("QueryTraces(from): %v", err)
	}
	if len(byTime) != 2 {
		t.Errorf("QueryTraces(from=%v) = %+v, want 2 matches (failed, other)", from, byTime)
	}
}

// QueryTraces sums tokens across every span, and collects the distinct,
// sorted models of the generation spans only.
func testQueryTracesAggregatesModelsAndTokens(t *testing.T, s store.Store) {
	ctx := context.Background()

	tr := newTrace("trace-1", time.Now())
	if err := s.MergeTrace(ctx, tr, true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}

	gptModel, claudeModel := "gpt-4", "claude-3"
	gptIn, gptOut := int64(100), int64(50)
	claudeIn, claudeOut := int64(20), int64(10)
	gptCost := 0.01

	gptSpan := newSpan(tr.ID, "span-gpt", time.Now())
	gptSpan.Kind = store.SpanKindGeneration
	gptSpan.Model = &gptModel
	gptSpan.InputTokens = &gptIn
	gptSpan.OutputTokens = &gptOut
	gptSpan.CostUSD = &gptCost
	if err := s.InsertSpan(ctx, gptSpan); err != nil {
		t.Fatalf("InsertSpan(gpt): %v", err)
	}

	claudeSpan := newSpan(tr.ID, "span-claude", time.Now().Add(time.Second))
	claudeSpan.Kind = store.SpanKindGeneration
	claudeSpan.Model = &claudeModel
	claudeSpan.InputTokens = &claudeIn
	claudeSpan.OutputTokens = &claudeOut
	// No CostUSD: the mixed case, next to gptSpan's cost.
	if err := s.InsertSpan(ctx, claudeSpan); err != nil {
		t.Fatalf("InsertSpan(claude): %v", err)
	}

	// A non-generation span carrying a Model must not contribute to Models.
	genericModel := "should-not-appear"
	genericSpan := newSpan(tr.ID, "span-generic", time.Now().Add(2*time.Second))
	genericSpan.Model = &genericModel
	if err := s.InsertSpan(ctx, genericSpan); err != nil {
		t.Fatalf("InsertSpan(generic): %v", err)
	}

	got, _, err := s.QueryTraces(ctx, store.TraceQuery{})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("QueryTraces = %+v, want 1 trace", got)
	}
	summary := got[0]

	if summary.TotalInputTokens != 120 || summary.TotalOutputTokens != 60 {
		t.Errorf("tokens = %d → %d, want 120 → 60", summary.TotalInputTokens, summary.TotalOutputTokens)
	}
	if len(summary.Models) != 2 || summary.Models[0] != "gpt-4" || summary.Models[1] != "claude-3" {
		t.Errorf("Models = %+v, want [gpt-4 claude-3] (the one with a cost first, generic span excluded)", summary.Models)
	}
	if summary.TotalCostUSD == nil || *summary.TotalCostUSD != gptCost {
		t.Errorf("TotalCostUSD = %v, want %v (only gptSpan has a cost)", summary.TotalCostUSD, gptCost)
	}
}

// A trace whose spans all have a nil cost has a nil TotalCostUSD, not zero.
func testQueryTracesCostNilWhenNoSpanHasCost(t *testing.T, s store.Store) {
	ctx := context.Background()

	tr := newTrace("trace-1", time.Now())
	if err := s.MergeTrace(ctx, tr, true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}
	model := "google/gemini-3.6-flash"
	sp := newSpan(tr.ID, "span-1", time.Now())
	sp.Kind = store.SpanKindGeneration
	sp.Model = &model
	// No CostUSD: a model with no price row.
	if err := s.InsertSpan(ctx, sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	got, _, err := s.QueryTraces(ctx, store.TraceQuery{})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("QueryTraces = %+v, want 1 trace", got)
	}
	if got[0].TotalCostUSD != nil {
		t.Errorf("TotalCostUSD = %v, want nil (no span has a cost)", *got[0].TotalCostUSD)
	}
}

// seedSessionTrace inserts one trace tagged with sessionID plus a single
// generation span carrying tokens (and cost, when non-nil).
func seedSessionTrace(t *testing.T, s store.Store, id, sessionID string, startedAt time.Time, status store.Status, cost *float64) {
	t.Helper()
	ctx := context.Background()
	tr := newTrace(id, startedAt)
	tr.SessionID = &sessionID
	tr.Status = status
	if err := s.MergeTrace(ctx, tr, true); err != nil {
		t.Fatalf("MergeTrace(%s): %v", id, err)
	}
	in, out := int64(10), int64(5)
	sp := newSpan(id, id+"-span", startedAt)
	sp.Kind = store.SpanKindGeneration
	sp.InputTokens = &in
	sp.OutputTokens = &out
	sp.CostUSD = cost
	if err := s.InsertSpan(ctx, sp); err != nil {
		t.Fatalf("InsertSpan(%s): %v", id, err)
	}
}

func testQuerySessionsAggregatesAndFilters(t *testing.T, s store.Store) {
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	cost := 0.25

	seedSessionTrace(t, s, "t1", "sess-a", base, store.StatusOK, &cost)
	seedSessionTrace(t, s, "t2", "sess-a", base.Add(time.Minute), store.StatusError, &cost)
	seedSessionTrace(t, s, "t3", "sess-b", base.Add(2*time.Minute), store.StatusOK, nil)
	// A trace with no session must not surface as a session.
	if err := s.MergeTrace(ctx, newTrace("t5", base), true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}
	// The service filter keeps the sessions that service took part in.
	service, session := "billing", "sess-billing"
	billed := newTrace("t6", base.Add(-time.Hour))
	billed.Service, billed.SessionID = &service, &session
	if err := s.MergeTrace(ctx, billed, true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}
	if got, _, err := s.QuerySessions(ctx, "billing", nil, 10); err != nil || len(got) != 1 || got[0].ID != session {
		t.Errorf("QuerySessions(billing) = %+v, %v; want only %s", got, err, session)
	}

	got, next, err := s.QuerySessions(ctx, "", nil, 10)
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	if next != nil {
		t.Errorf("next = %+v, want nil", next)
	}
	if len(got) != 3 || got[0].ID != "sess-b" || got[1].ID != "sess-a" || got[2].ID != session {
		t.Fatalf("sessions = %+v, want [sess-b sess-a sess-billing] (newest first)", got)
	}
	if got[0].TotalCostUSD != nil {
		t.Errorf("sess-b cost = %v, want nil (no span has a cost)", *got[0].TotalCostUSD)
	}
	assertSessionTotals(t, got[1], base)
}

func assertSessionTotals(t *testing.T, a store.SessionSummary, base time.Time) {
	t.Helper()
	if a.TraceCount != 2 || a.Status != store.StatusError {
		t.Errorf("sess-a = %d traces, status %q; want 2, error", a.TraceCount, a.Status)
	}
	if a.TotalInputTokens != 20 || a.TotalOutputTokens != 10 {
		t.Errorf("sess-a tokens = %d → %d, want 20 → 10", a.TotalInputTokens, a.TotalOutputTokens)
	}
	if a.TotalCostUSD == nil || *a.TotalCostUSD != 0.5 {
		t.Errorf("sess-a cost = %v, want 0.5", a.TotalCostUSD)
	}
	if !a.StartedAt.Equal(base) || !a.EndedAt.Equal(base.Add(time.Minute+time.Second)) {
		t.Errorf("sess-a span = %v..%v, want %v..%v", a.StartedAt, a.EndedAt, base, base.Add(time.Minute+time.Second))
	}
}

func testQuerySessionsPagination(t *testing.T, s store.Store) {
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	for i := range 5 {
		seedSessionTrace(t, s, fmt.Sprintf("t%d", i), fmt.Sprintf("sess-%d", i), base.Add(time.Duration(i)*time.Minute), store.StatusOK, nil)
	}

	var seen []string
	var cursor *store.Cursor
	for {
		page, next, err := s.QuerySessions(ctx, "", cursor, 2)
		if err != nil {
			t.Fatalf("QuerySessions: %v", err)
		}
		for _, sess := range page {
			seen = append(seen, sess.ID)
		}
		if next == nil {
			break
		}
		cursor = next
	}
	want := []string{"sess-4", "sess-3", "sess-2", "sess-1", "sess-0"}
	if fmt.Sprint(seen) != fmt.Sprint(want) {
		t.Errorf("paged sessions = %v, want %v (no repeats, no gaps)", seen, want)
	}
}

func testGetSession(t *testing.T, s store.Store) {
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	seedSessionTrace(t, s, "late", "sess-a", base.Add(time.Minute), store.StatusOK, nil)
	seedSessionTrace(t, s, "early", "sess-a", base, store.StatusOK, nil)
	seedSessionTrace(t, s, "elsewhere", "sess-b", base, store.StatusOK, nil)

	summary, turns, err := s.GetSession(ctx, "sess-a")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if summary.ID != "sess-a" || summary.TraceCount != 2 || summary.FirstTraceID != "early" {
		t.Errorf("summary = %+v, want sess-a with 2 traces", summary)
	}
	if len(turns) != 2 || turns[0].ID != "early" || turns[1].ID != "late" {
		t.Errorf("turns = %+v, want [early late] (chronological)", turns)
	}

	if _, _, err := s.GetSession(ctx, "nope"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("GetSession(unknown) err = %v, want ErrNotFound", err)
	}
}

// The trace and session aggregates carry the cache buckets, and a span
// that never reported them contributes zero.
func testSummariesSumCacheTokens(t *testing.T, s store.Store) {
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	seedSessionTrace(t, s, "plain", "sess-a", base, store.StatusOK, nil)
	seedSessionTrace(t, s, "cached", "sess-a", base.Add(time.Minute), store.StatusOK, nil)
	in, read, write := int64(100), int64(60), int64(10)
	sp := newSpan("cached", "cached-span-2", base.Add(time.Minute))
	sp.InputTokens, sp.CacheReadTokens, sp.CacheWriteTokens = &in, &read, &write
	if err := s.InsertSpan(ctx, sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	traces, _, err := s.QueryTraces(ctx, store.TraceQuery{})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	sessions, _, err := s.QuerySessions(ctx, "", nil, 10)
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	summary, turns, err := s.GetSession(ctx, "sess-a")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(traces) != 2 || len(sessions) != 1 || len(turns) != 2 {
		t.Fatalf("got %d traces, %d sessions, %d turns; want 2, 1, 2", len(traces), len(sessions), len(turns))
	}
	// {input, cache read, cache write}; QueryTraces is newest first, turns oldest first.
	got := map[string][3]int64{
		"QueryTraces cached": {traces[0].TotalInputTokens, traces[0].TotalCacheReadTokens, traces[0].TotalCacheWriteTokens},
		"QueryTraces plain":  {traces[1].TotalInputTokens, traces[1].TotalCacheReadTokens, traces[1].TotalCacheWriteTokens},
		"QuerySessions":      {sessions[0].TotalInputTokens, sessions[0].TotalCacheReadTokens, sessions[0].TotalCacheWriteTokens},
		"GetSession summary": {summary.TotalInputTokens, summary.TotalCacheReadTokens, summary.TotalCacheWriteTokens},
		"GetSession turn 2":  {turns[1].TotalInputTokens, turns[1].TotalCacheReadTokens, turns[1].TotalCacheWriteTokens},
	}
	want := map[string][3]int64{
		"QueryTraces cached": {110, 60, 10},
		"QueryTraces plain":  {10, 0, 0},
		"QuerySessions":      {120, 60, 10},
		"GetSession summary": {120, 60, 10},
		"GetSession turn 2":  {110, 60, 10},
	}
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s {input, cache read, cache write} = %v, want %v", name, got[name], w)
		}
	}
}

// A From bound on a whole second keeps a trace that started a fraction of
// a second after it.
func testQueryTracesFromIncludesSameSecond(t *testing.T, s store.Store) {
	ctx := context.Background()
	from := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	for id, at := range map[string]time.Time{"before": from.Add(-time.Millisecond), "exact": from, "after": from.Add(465 * time.Millisecond)} {
		if err := s.MergeTrace(ctx, newTrace(id, at), true); err != nil {
			t.Fatalf("MergeTrace(%s): %v", id, err)
		}
	}
	got, _, err := s.QueryTraces(ctx, store.TraceQuery{From: &from})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(got) != 2 || got[0].ID == "before" || got[1].ID == "before" {
		t.Errorf("QueryTraces(from=%v) = %+v, want exact and after, not before", from, got)
	}
	// To is inclusive as well: the list's from/to fields are whole minutes.
	got, _, err = s.QueryTraces(ctx, store.TraceQuery{To: &from})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(got) != 2 || got[0].ID != "exact" || got[1].ID != "before" {
		t.Errorf("QueryTraces(to=%v) = %+v, want exact then before", from, got)
	}
}

// Traces that start at the same instant are ordered by id, the cursor's
// second half: paging one at a time must return each exactly once.
func testQueryTracesSameStartPaginatesByID(t *testing.T, s store.Store) {
	ctx := context.Background()
	at := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	for _, id := range []string{"b", "c", "a"} {
		if err := s.MergeTrace(ctx, newTrace(id, at), true); err != nil {
			t.Fatalf("MergeTrace(%s): %v", id, err)
		}
	}
	var got string
	var cursor *store.Cursor
	for range 3 {
		page, next, err := s.QueryTraces(ctx, store.TraceQuery{Limit: 1, Cursor: cursor})
		if err != nil || len(page) != 1 {
			t.Fatalf("QueryTraces after %q: %+v, err %v; want one trace", got, page, err)
		}
		got, cursor = got+page[0].ID, next
	}
	if got != "cba" || cursor != nil {
		t.Errorf("pages = %q (next %v), want cba and no further page", got, cursor)
	}
}

func testTraceHistogram(t *testing.T, s store.Store) {
	ctx := context.Background()
	base := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	seed := func(service, id string, at time.Time, status store.Status) {
		tr := newTrace(id, at)
		tr.Status, tr.Service = status, &service
		if err := s.MergeTrace(ctx, tr, true); err != nil {
			t.Fatalf("MergeTrace(%s): %v", id, err)
		}
	}
	seed("main", "a", base.Add(10*time.Second+500*time.Millisecond), store.StatusOK)
	seed("main", "b", base.Add(20*time.Second), store.StatusError)
	seed("main", "c", base.Add(5*time.Minute+30*time.Second), store.StatusUnset)
	seed("other", "d", base.Add(time.Hour), store.StatusOK)

	// 5m30s over at most 3 buckets: whole-minute buckets of 2 minutes.
	got, err := s.TraceHistogram(ctx, store.TraceQuery{Service: "main"}, 3)
	if err != nil {
		t.Fatalf("TraceHistogram: %v", err)
	}
	want := []store.TraceBucket{
		{Start: base, End: base.Add(2 * time.Minute), OK: 1, Error: 1},
		{Start: base.Add(2 * time.Minute), End: base.Add(4 * time.Minute)},
		{Start: base.Add(4 * time.Minute), End: base.Add(6 * time.Minute), Unset: 1},
	}
	assertBuckets(t, got, want)

	// Filters narrow the counted set and the range with it.
	errStatus := store.StatusError
	onlyErr, err := s.TraceHistogram(ctx, store.TraceQuery{Service: "main", Status: &errStatus}, 3)
	if err != nil {
		t.Fatalf("TraceHistogram(status): %v", err)
	}
	assertBuckets(t, onlyErr, []store.TraceBucket{{Start: base, End: base.Add(time.Minute), Error: 1}})

	none, err := s.TraceHistogram(ctx, store.TraceQuery{Span: store.SpanFilter{Name: "no-such-span"}}, 3)
	if err != nil {
		t.Fatalf("TraceHistogram(no match): %v", err)
	}
	assertBuckets(t, none, nil)
}

func assertBuckets(t *testing.T, got, want []store.TraceBucket) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("TraceHistogram = %+v, want %+v", got, want)
	}
	for i := range want {
		if !got[i].Start.Equal(want[i].Start) || !got[i].End.Equal(want[i].End) ||
			got[i].OK != want[i].OK || got[i].Error != want[i].Error || got[i].Unset != want[i].Unset {
			t.Errorf("bucket %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func testListGenerationModels(t *testing.T, s store.Store) {
	ctx := context.Background()

	if got, err := s.ListGenerationModels(ctx); err != nil || len(got) != 0 {
		t.Fatalf("ListGenerationModels on an empty store = %v, %v; want none", got, err)
	}

	now := time.Now()
	insert := func(id string, kind store.SpanKind, model *string) {
		t.Helper()
		sp := store.Span{
			TraceID: "trace", ID: id, Kind: kind, Name: id,
			StartedAt: now, EndedAt: now, Status: store.StatusOK, Model: model, CreatedAt: now,
		}
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatalf("InsertSpan(%s): %v", id, err)
		}
	}
	sonnet, haiku, agentModel := "claude-sonnet-5", "claude-haiku-4-5", "agent-level-model"
	insert("s1", store.SpanKindGeneration, &sonnet)
	insert("s2", store.SpanKindGeneration, &haiku)
	insert("s3", store.SpanKindGeneration, &sonnet)
	insert("s4", store.SpanKindGeneration, nil)
	insert("s5", store.SpanKindAgent, &agentModel)

	got, err := s.ListGenerationModels(ctx)
	if err != nil {
		t.Fatalf("ListGenerationModels: %v", err)
	}
	want := []string{"claude-haiku-4-5", "claude-sonnet-5"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("ListGenerationModels = %v, want %v (distinct, sorted, generation spans only)", got, want)
	}
}

// seedComparableSpans leaves three sonnet "chat" generations inside the last
// day, surrounded by spans that differ in exactly one way.
func seedComparableSpans(t *testing.T, s store.Store) (now time.Time) {
	t.Helper()
	ctx := context.Background()
	now = time.Now()
	sonnet, haiku := "claude-sonnet-5", "claude-haiku-4-5"
	n := 0
	insert := func(kind store.SpanKind, name string, model *string, age, dur time.Duration, tokens *int64) {
		t.Helper()
		n++
		start := now.Add(-age)
		sp := store.Span{
			TraceID: "trace", ID: fmt.Sprintf("s%d", n), Kind: kind, Name: name, Model: model,
			StartedAt: start, EndedAt: start.Add(dur), Status: store.StatusOK, InputTokens: tokens, CreatedAt: now,
		}
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatalf("InsertSpan: %v", err)
		}
	}
	tok := func(v int64) *int64 { return &v }
	gen := store.SpanKindGeneration
	insert(gen, "chat", &sonnet, 3*time.Hour, 3*time.Second, tok(300))
	insert(gen, "chat", &sonnet, 2*time.Hour, 2*time.Second, nil) // no token count reported
	insert(gen, "chat", &sonnet, time.Hour, 1500*time.Microsecond, tok(100))
	insert(gen, "chat", &sonnet, 48*time.Hour, time.Minute, tok(9)) // before the window
	insert(gen, "chat", &haiku, time.Hour, time.Minute, tok(9))     // other model
	insert(gen, "chat", nil, time.Hour, time.Minute, tok(9))        // no model
	insert(gen, "other", &sonnet, time.Hour, time.Minute, tok(9))   // other name
	insert(store.SpanKindTool, "chat", &sonnet, time.Hour, time.Minute, tok(9))
	insert(store.SpanKindTool, "search", nil, time.Hour, 40*time.Millisecond, nil)

	return now
}

func testComparableSpans(t *testing.T, s store.Store) {
	ctx := context.Background()
	now := seedComparableSpans(t, s)
	sonnet, gen := "claude-sonnet-5", store.SpanKindGeneration
	like := store.Span{Kind: gen, Name: "chat", Model: &sonnet}
	since := now.Add(-24 * time.Hour)
	durations, tokens, err := s.ComparableSpans(ctx, like, since, 100)
	if err != nil {
		t.Fatalf("ComparableSpans: %v", err)
	}
	if want := []time.Duration{1500 * time.Microsecond, 2 * time.Second, 3 * time.Second}; fmt.Sprint(durations) != fmt.Sprint(want) {
		t.Errorf("durations = %v, want %v (same kind, name and model, inside the window, newest first)", durations, want)
	}
	if want := []int64{100, 300}; fmt.Sprint(tokens) != fmt.Sprint(want) {
		t.Errorf("input tokens = %v, want %v (only where reported)", tokens, want)
	}

	if durations, _, err = s.ComparableSpans(ctx, like, since, 2); err != nil || len(durations) != 2 || durations[0] != 1500*time.Microsecond {
		t.Errorf("limit 2 = %v, %v; want the two newest", durations, err)
	}

	// A nil model matches only spans that have none.
	durations, tokens, err = s.ComparableSpans(ctx, store.Span{Kind: store.SpanKindTool, Name: "search"}, since, 100)
	if err != nil || len(durations) != 1 || durations[0] != 40*time.Millisecond || len(tokens) != 0 {
		t.Errorf("tool without model = %v, %v, %v; want its one 40ms span and no tokens", durations, tokens, err)
	}
	if durations, _, err = s.ComparableSpans(ctx, store.Span{Kind: gen, Name: "nothing"}, since, 100); err != nil || len(durations) != 0 {
		t.Errorf("no comparable span = %v, %v; want none", durations, err)
	}
}

// A whole-second timestamp and a fractional one in the same second must
// order by time in every comparison: ORDER BY, the To bound, the cursor.
func testTimestampOrderIsTimeOrder(t *testing.T, s store.Store) {
	ctx := context.Background()
	whole := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	half := whole.Add(500 * time.Millisecond)
	for id, at := range map[string]time.Time{"whole": whole, "half": half} {
		if err := s.MergeTrace(ctx, newTrace(id, at), true); err != nil {
			t.Fatalf("MergeTrace(%s): %v", id, err)
		}
	}
	got, next, err := s.QueryTraces(ctx, store.TraceQuery{Limit: 1})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(got) != 1 || got[0].ID != "half" || next == nil {
		t.Fatalf("first page = %+v (next %v), want half, the later trace", got, next)
	}
	if got, _, err = s.QueryTraces(ctx, store.TraceQuery{Limit: 1, Cursor: next}); err != nil || len(got) != 1 || got[0].ID != "whole" {
		t.Errorf("second page = %+v (err %v), want whole", got, err)
	}
	to := whole.Add(200 * time.Millisecond)
	if got, _, err = s.QueryTraces(ctx, store.TraceQuery{To: &to}); err != nil || len(got) != 1 || got[0].ID != "whole" {
		t.Errorf("QueryTraces(to=%v) = %+v (err %v), want only whole", to, got, err)
	}
}

// MergeTrace across batches: the time range only widens, error is sticky,
// a root batch replaces name/status/session, a later non-root batch only
// fills what is empty, created_at/metadata survive a batch without them,
// and the service is the first one named, whatever the root says later.
func testMergeTrace(t *testing.T, s store.Store) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	childSession, rootSession, lateSession := "from-child", "from-root", "late"
	childService, rootService := "worker", "gateway"
	for _, step := range []struct {
		batch   store.Trace
		hasRoot bool
		want    string // name, status, session, service, start and end relative to t0
	}{
		{store.Trace{Name: "child", StartedAt: t0, EndedAt: t0.Add(time.Second), Status: store.StatusUnset, Metadata: []byte(`{"a":1}`)},
			false, "child unset <nil> <nil> 0s 1s"},
		{store.Trace{Name: "other-child", StartedAt: t0.Add(500 * time.Millisecond), EndedAt: t0.Add(5 * time.Second), Status: store.StatusError, SessionID: &childSession, Service: &childService},
			false, "child error from-child worker 0s 5s"},
		{store.Trace{Name: "root", StartedAt: t0.Add(-time.Second), EndedAt: t0.Add(2 * time.Second), Status: store.StatusOK, SessionID: &rootSession, Service: &rootService},
			true, "root error from-root worker -1s 5s"},
		{store.Trace{Name: "late", StartedAt: t0, EndedAt: t0, Status: store.StatusUnset, SessionID: &lateSession},
			false, "root error from-root worker -1s 5s"},
	} {
		step.batch.ID = "t1"
		step.batch.CreatedAt = t0.Add(time.Duration(len(step.batch.Name)) * time.Hour) // differs per batch
		if err := s.MergeTrace(ctx, step.batch, step.hasRoot); err != nil {
			t.Fatalf("MergeTrace(%s): %v", step.batch.Name, err)
		}
		got, _, err := s.GetTraceByID(ctx, "t1")
		if err != nil {
			t.Fatalf("GetTraceByID: %v", err)
		}
		session, service := "<nil>", "<nil>"
		if got.SessionID != nil {
			session = *got.SessionID
		}
		if got.Service != nil {
			service = *got.Service
		}
		if desc := fmt.Sprintf("%s %s %s %s %v %v", got.Name, got.Status, session, service, got.StartedAt.Sub(t0), got.EndedAt.Sub(t0)); desc != step.want {
			t.Errorf("after batch %q: trace is %q, want %q", step.batch.Name, desc, step.want)
		}
		if !got.CreatedAt.Equal(t0.Add(5*time.Hour)) || string(got.Metadata) != `{"a":1}` {
			t.Errorf("after batch %q: created %v, metadata %s; want the first batch's kept", step.batch.Name, got.CreatedAt, got.Metadata)
		}
	}
}

// Many batches of one trace merged at once must come out as the same row
// as merging them one by one, in any order.
func testMergeTraceConcurrent(t *testing.T, s store.Store) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	const n = 16
	errs := make(chan error, n)
	for i := range n {
		go func() {
			tr := store.Trace{ID: "t1", Name: fmt.Sprintf("span-%d", i), Status: store.StatusUnset,
				StartedAt: t0.Add(time.Duration(i) * time.Second), EndedAt: t0.Add(time.Duration(i+1) * time.Second), CreatedAt: t0}
			if i == 7 {
				tr.Name, tr.Status = "root", store.StatusOK
			}
			if i == 3 {
				tr.Status = store.StatusError
			}
			errs <- s.MergeTrace(ctx, tr, i == 7)
		}()
	}
	for range n {
		if err := <-errs; err != nil {
			t.Fatalf("MergeTrace: %v", err)
		}
	}
	got, _, err := s.GetTraceByID(ctx, "t1")
	if err != nil {
		t.Fatalf("GetTraceByID: %v", err)
	}
	if got.Name != "root" || got.Status != store.StatusError || !got.StartedAt.Equal(t0) || !got.EndedAt.Equal(t0.Add(n*time.Second)) {
		t.Errorf("trace = %q %s %v to %v, want root, error, %v to %v", got.Name, got.Status, got.StartedAt, got.EndedAt, t0, t0.Add(n*time.Second))
	}
}

func testQuerySpans(t *testing.T, s store.Store) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	session, body, main, other := "conv", "hello", "main", "other"
	inSession, noSession, elsewhere := newTrace("in-session", t0), newTrace("no-session", t0), newTrace("elsewhere", t0)
	inSession.SessionID, inSession.Service, noSession.Service, elsewhere.Service = &session, &main, &main, &other
	for _, tr := range []store.Trace{inSession, noSession, elsewhere} {
		if err := s.MergeTrace(ctx, tr, true); err != nil {
			t.Fatalf("MergeTrace: %v", err)
		}
	}
	for _, sp := range []store.Span{
		{TraceID: "in-session", ID: "gen-late", Kind: store.SpanKindGeneration, StartedAt: t0.Add(2 * time.Second)},
		{TraceID: "in-session", ID: "gen-early", Kind: store.SpanKindGeneration, StartedAt: t0.Add(500 * time.Millisecond)},
		{TraceID: "in-session", ID: "tool", Kind: store.SpanKindTool, StartedAt: t0},
		{TraceID: "no-session", ID: "gen-other-trace", Kind: store.SpanKindGeneration, StartedAt: t0.Add(time.Second)},
		{TraceID: "elsewhere", ID: "gen-elsewhere", Kind: store.SpanKindGeneration, StartedAt: t0.Add(100 * time.Millisecond)},
	} {
		sp.Status, sp.EndedAt, sp.CreatedAt, sp.Input, sp.Metadata = store.StatusOK, sp.StartedAt, t0, &body, []byte(`{"k":"v"}`)
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatalf("InsertSpan(%s): %v", sp.ID, err)
		}
	}

	second := t0.Add(time.Second)
	for _, c := range []struct {
		name string
		q    store.SpanQuery
		want string
	}{
		{"everything, oldest first", store.SpanQuery{}, "tool gen-elsewhere gen-early gen-other-trace gen-late "},
		{"service", store.SpanQuery{Service: main, Bodies: true}, "tool gen-early gen-other-trace gen-late "},
		{"session generations", store.SpanQuery{SessionID: session, Kind: store.SpanKindGeneration, Bodies: true}, "gen-early gen-late "},
		{"first of each kind per trace", store.SpanQuery{Service: main, First: true}, "tool gen-early gen-other-trace "},
		{"session of another service", store.SpanQuery{Service: other, SessionID: session}, ""},
		{"from", store.SpanQuery{Service: main, From: &second}, "gen-other-trace gen-late "},
		{"to", store.SpanQuery{Service: main, To: &second}, "tool gen-early gen-other-trace "},
	} {
		spans, err := s.QuerySpans(ctx, c.q)
		if err != nil {
			t.Fatalf("%s: QuerySpans: %v", c.name, err)
		}
		var got string
		for _, sp := range spans {
			got += sp.ID + " "
			if (sp.Input != nil) != c.q.Bodies || string(sp.Metadata) != `{"k":"v"}` {
				t.Errorf("%s: %s has input %v, metadata %s; want input only with Bodies", c.name, sp.ID, sp.Input, sp.Metadata)
			}
		}
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
	testQuerySpansUsage(ctx, t, s, t0)
}

// Usage: the spans of the named traces (testQuerySpans stored them) with
// their numbers and their place in the tree, and nothing a chart does not read.
func testQuerySpansUsage(ctx context.Context, t *testing.T, s store.Store, t0 time.Time) {
	in, read, cost, model, parent, row, body, session := int64(100), int64(60), 0.01, "m", "tool", "^m$", "hello", "conv"
	child := store.Span{TraceID: "no-session", ID: "gen-child", ParentSpanID: &parent, Kind: store.SpanKindGeneration, Name: "call", Status: store.StatusOK,
		StartedAt: t0.Add(3 * time.Second), EndedAt: t0.Add(4 * time.Second), CreatedAt: t0, Model: &model, Input: &body, Metadata: []byte(`{"k":"v"}`),
		InputTokens: &in, OutputTokens: &in, CacheReadTokens: &read, CacheWriteTokens: &read, CostUSD: &cost, PricePattern: &row}
	if err := s.InsertSpan(ctx, child); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}
	spans, err := s.QuerySpans(ctx, store.SpanQuery{TraceIDs: []string{"no-session", "elsewhere"}, Usage: true})
	if err != nil || len(spans) != 3 || spans[0].ID != "gen-elsewhere" || spans[1].ID != "gen-other-trace" {
		t.Fatalf("usage of two traces = %+v, err %v; want gen-elsewhere, gen-other-trace, gen-child", spans, err)
	}
	want := child
	want.Name, want.Status, want.Input, want.Metadata, want.CreatedAt = "", "", nil, nil, time.Time{}
	if !reflect.DeepEqual(spans[2], want) {
		t.Errorf("usage span = %+v\nwant %+v", spans[2], want)
	}
	spans, err = s.QuerySpans(ctx, store.SpanQuery{SessionID: session, Kind: store.SpanKindGeneration, Usage: true})
	if err != nil || len(spans) != 2 || spans[0].ID != "gen-early" || spans[0].Input != nil {
		t.Errorf("usage of a session's generations = %+v, err %v; want gen-early, gen-late without bodies", spans, err)
	}
}

// Price provenance is written at insert, rewritten by UpdateSpanUsage, read
// back on the span, and grouped by SpanPricings; a span spoor did not price
// (no fingerprint) is in no group.
func testSpanPricings(t *testing.T, s store.Store) {
	ctx := context.Background()
	now := time.Now().UTC()
	model, row, old, current := "m[1m]", "^m$", "aaaa0001", "bbbb0002"
	for _, id := range []string{"a", "b", "c", "unpriced"} {
		sp := newSpan("t1", id, now)
		if sp.Model = &model; id != "unpriced" {
			sp.PricePattern, sp.PriceFingerprint = &row, &old
		}
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatalf("InsertSpan: %v", err)
		}
	}
	repriced := newSpan("t1", "c", now)
	repriced.PricePattern, repriced.PriceFingerprint = &row, &current
	if err := s.UpdateSpanUsage(ctx, []store.Span{repriced}); err != nil {
		t.Fatalf("UpdateSpanUsage: %v", err)
	}
	got, err := s.SpanPricings(ctx)
	if err != nil {
		t.Fatalf("SpanPricings: %v", err)
	}
	sort.Slice(got, func(i, j int) bool { return got[i].Fingerprint < got[j].Fingerprint })
	want := []store.SpanPricing{{Model: model, Pattern: row, Fingerprint: old, Spans: 2}, {Model: model, Pattern: row, Fingerprint: current, Spans: 1}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SpanPricings = %+v, want %+v", got, want)
	}
	spans, err := s.QuerySpans(ctx, store.SpanQuery{})
	if err != nil {
		t.Fatalf("GetTraceByID: %v", err)
	}
	for _, sp := range spans {
		wantFP := map[string]string{"a": old, "b": old, "c": current}[sp.ID]
		if store.Deref(sp.PriceFingerprint) != wantFP || (wantFP != "") != (store.Deref(sp.PricePattern) == row) {
			t.Errorf("span %s read back with row %q, fingerprint %q; want fingerprint %q", sp.ID, store.Deref(sp.PricePattern), store.Deref(sp.PriceFingerprint), wantFP)
		}
	}
}

func testUpdateSpanUsage(t *testing.T, s store.Store) {
	ctx := context.Background()
	now := time.Now().UTC()
	body := "hello"
	for _, id := range []string{"updated", "untouched"} {
		sp := newSpan("t1", id, now)
		sp.Input = &body
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatalf("InsertSpan: %v", err)
		}
	}
	in, read, cost := int64(100), int64(80), 0.25
	upd := newSpan("t1", "updated", now) // no body, as QuerySpans returns it
	upd.InputTokens, upd.CacheReadTokens, upd.CostUSD = &in, &read, &cost
	if err := s.UpdateSpanUsage(ctx, []store.Span{upd}); err != nil {
		t.Fatalf("UpdateSpanUsage: %v", err)
	}
	spans, err := s.QuerySpans(ctx, store.SpanQuery{Bodies: true})
	if err != nil || len(spans) != 2 {
		t.Fatalf("QuerySpans = %d spans, err %v", len(spans), err)
	}
	for _, sp := range spans {
		got := fmt.Sprintf("%s: input %s, tokens %d/%d, cache read %d, cost %v", sp.ID, *sp.Input,
			store.Deref(sp.InputTokens), store.Deref(sp.OutputTokens), store.Deref(sp.CacheReadTokens), store.Deref(sp.CostUSD))
		want := "untouched: input hello, tokens 0/0, cache read 0, cost 0"
		if sp.ID == "updated" {
			want = "updated: input hello, tokens 100/0, cache read 80, cost 0.25"
		}
		if got != want {
			t.Errorf("after UpdateSpanUsage: %s; want %s", got, want)
		}
	}
}

// A trace whose spans all name a parent has no root yet: a sender that
// exports the root last is still working. No spans at all is not rootless.
func testQueryTracesRootless(t *testing.T, s store.Store) {
	ctx := context.Background()
	now, parent := time.Now(), "not-arrived"
	for _, id := range []string{"empty", "live", "done"} {
		if err := s.MergeTrace(ctx, newTrace(id, now), id == "done"); err != nil {
			t.Fatalf("MergeTrace: %v", err)
		}
	}
	child, root, under := newSpan("live", "a", now), newSpan("done", "root", now), newSpan("done", "b", now)
	child.ParentSpanID, under.ParentSpanID = &parent, &root.ID
	for _, sp := range []store.Span{child, root, under} {
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatalf("InsertSpan: %v", err)
		}
	}
	got, _, err := s.QueryTraces(ctx, store.TraceQuery{})
	if err != nil || len(got) != 3 {
		t.Fatalf("QueryTraces: %v, %d traces, want 3", err, len(got))
	}
	for _, tr := range got {
		if want := tr.ID == "live"; tr.Rootless != want {
			t.Errorf("trace %s: Rootless = %v, want %v", tr.ID, tr.Rootless, want)
		}
	}
}

// Every count is over what is stored: a running turn whose root has not
// arrived, a span whose parent never came, a model call without tokens (a
// failed call has none to send and is not counted), a
// model or a price, a cost priced without cache counts, a prompt that is
// not a list of messages.
func testBlindSpots(t *testing.T, s store.Store) {
	ctx := context.Background()
	if got, err := s.BlindSpots(ctx); err != nil || got.Traces != 0 || len(got.ModelSpans) != 0 {
		t.Fatalf("BlindSpots on an empty store = %+v, %v; want zeros", got, err)
	}

	now, missing, model, pattern := time.Now(), "not-arrived", "m", "^m$"
	for _, id := range []string{"live", "done"} {
		if err := s.MergeTrace(ctx, newTrace(id, now), id == "done"); err != nil {
			t.Fatalf("MergeTrace: %v", err)
		}
	}
	child, root, g1, g2, g3 := newSpan("live", "a", now), newSpan("done", "root", now), newSpan("done", "g1", now), newSpan("done", "g2", now), newSpan("done", "g3", now)
	child.ParentSpanID = &missing
	text, list, ten := "a prompt that is not a message list", `[{"role":"user","content":"hi"}]`, int64(10)
	g1.Kind, g1.ParentSpanID, g1.Model, g1.Input = store.SpanKindGeneration, &root.ID, &model, &text
	g2.Kind, g2.ParentSpanID, g2.Model, g2.PricePattern, g2.InputTokens, g2.Input = store.SpanKindGeneration, &root.ID, &model, &pattern, &ten, &list
	g3.Kind, g3.ParentSpanID, g3.Model, g3.Status = store.SpanKindGeneration, &root.ID, &model, store.StatusError // failed: no usage to report
	for _, sp := range []store.Span{child, root, g1, g2, g3} {
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatalf("InsertSpan: %v", err)
		}
	}
	got, err := s.BlindSpots(ctx)
	if err != nil {
		t.Fatalf("BlindSpots: %v", err)
	}
	want := store.BlindSpots{Traces: 2, Spans: 5, Generations: 3, Rootless: 1, Orphans: 1, NoInputTokens: 1, CacheUnknown: 1, NoMessages: 1,
		ModelSpans: []store.ModelCount{{Model: "m", Spans: 3}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BlindSpots = %+v, want %+v", got, want)
	}
}

// The window keeps the query's filters, sums every costed span of the traces
// started inside it, and counts the traces that have no cost at all.
func testCostSince(t *testing.T, s store.Store) {
	ctx := context.Background()
	now, svc, cost := time.Now(), "svc", 0.25
	for _, tr := range []struct {
		id  string
		at  time.Time
		svc *string
	}{{"old", now.Add(-time.Hour), &svc}, {"new", now, &svc}, {"other", now, nil}, {"free", now, &svc}} {
		trace := newTrace(tr.id, tr.at)
		trace.Service = tr.svc
		if err := s.MergeTrace(ctx, trace, true); err != nil {
			t.Fatalf("MergeTrace: %v", err)
		}
		for _, sid := range []string{"a", "b"} {
			sp := newSpan(tr.id, sid, tr.at)
			if tr.id != "free" {
				sp.CostUSD = &cost
			}
			if err := s.InsertSpan(ctx, sp); err != nil {
				t.Fatalf("InsertSpan: %v", err)
			}
		}
	}
	got, err := s.CostSince(ctx, store.TraceQuery{Service: svc}, now.Add(-10*time.Minute))
	if err != nil || got != (store.CostWindow{USD: 0.5, Traces: 2, Uncosted: 1}) {
		t.Errorf("CostSince(svc, 10 min) = %+v, %v; want $0.50 over 2 traces, 1 without a cost", got, err)
	}
	if got, err = s.CostSince(ctx, store.TraceQuery{}, now.Add(-2*time.Hour)); err != nil || got != (store.CostWindow{USD: 1.5, Traces: 4, Uncosted: 1}) {
		t.Errorf("CostSince(all, 2 h) = %+v, %v; want $1.50 over 4 traces", got, err)
	}
}

// A trace summary counts the spans spoor priced without any cache count from
// the sender: a priced span with one, a span the sender priced itself and a
// span with no input do not count.
func testSummaryCacheUnknown(t *testing.T, s store.Store) {
	ctx := context.Background()
	base := time.Now().UTC().Truncate(time.Second)
	seedSessionTrace(t, s, "tr", "sess", base, store.StatusOK, nil)
	in, zero, read, cost, row := int64(100), int64(0), int64(60), 0.01, "^model$"
	for id, set := range map[string]func(*store.Span){
		"priced, no cache count": func(sp *store.Span) { sp.InputTokens, sp.CostUSD, sp.PricePattern = &in, &cost, &row },
		"priced, cache counted": func(sp *store.Span) {
			sp.InputTokens, sp.CacheReadTokens, sp.CostUSD, sp.PricePattern = &in, &read, &cost, &row
		},
		"priced by the sender": func(sp *store.Span) { sp.InputTokens, sp.CostUSD = &in, &cost },
		"priced, no input":     func(sp *store.Span) { sp.InputTokens, sp.CostUSD, sp.PricePattern = &zero, &cost, &row },
	} {
		sp := newSpan("tr", id, base)
		set(&sp)
		if got, want := sp.CacheUnknown(), id == "priced, no cache count"; got != want {
			t.Errorf("%s: CacheUnknown() = %v, want %v", id, got, want)
		}
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatalf("InsertSpan: %v", err)
		}
	}
	traces, _, err := s.QueryTraces(ctx, store.TraceQuery{})
	if err != nil || len(traces) != 1 {
		t.Fatalf("QueryTraces: %d traces, err %v", len(traces), err)
	}
	if traces[0].CacheUnknown != 1 {
		t.Errorf("CacheUnknown = %d, want 1", traces[0].CacheUnknown)
	}
	listed, _, err := s.QuerySessions(ctx, "", nil, 10)
	one, _, err2 := s.GetSession(ctx, "sess")
	if err != nil || err2 != nil || len(listed) != 1 || listed[0].CacheUnknown != 1 || one.CacheUnknown != 1 {
		t.Errorf("session CacheUnknown: listed %+v (err %v), one %d (err %v), want 1 in both", listed, err, one.CacheUnknown, err2)
	}
}

// GetTraceSummary is the row QueryTraces lists, span counts included, or
// ErrNotFound.
func testGetTraceSummary(t *testing.T, s store.Store) {
	ctx := context.Background()
	tr := newTrace("trace-1", time.Now())
	if err := s.MergeTrace(ctx, tr, true); err != nil {
		t.Fatal(err)
	}
	failed := newSpan(tr.ID, "bad", time.Now())
	failed.Status = store.StatusError
	for _, sp := range []store.Span{newSpan(tr.ID, "ok", time.Now()), failed} {
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.GetTraceSummary(ctx, tr.ID)
	if err != nil || got.ID != tr.ID || got.SpanCount != 2 || got.ErrorSpans != 1 {
		t.Errorf("GetTraceSummary = %+v, %v; want trace-1 with 2 spans, 1 failed", got, err)
	}
	if _, err := s.GetTraceSummary(ctx, "absent"); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("an absent trace: %v, want ErrNotFound", err)
	}
}

// A span's attributes come back exactly as stored, whatever the adapter does
// to keep them small, are left out of reads that do not ask for bodies, and
// are nil when the span had none.
func testSpanAttributesRoundTrip(t *testing.T, s store.Store) {
	ctx := context.Background()
	tr := newTrace("trace-1", time.Now())
	if err := s.MergeTrace(ctx, tr, true); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("prompt ção ✓ ", 5000)
	withAttrs := newSpan(tr.ID, "with", time.Now())
	withAttrs.Attributes = json.RawMessage(`{"gen_ai.request.model":"m","gen_ai.input.messages":"` + big + `","n":3,"tags":["a","b"]}`)
	plain := newSpan(tr.ID, "plain", time.Now().Add(time.Second))
	for _, sp := range []store.Span{withAttrs, plain} {
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatal(err)
		}
	}
	_, spans, err := s.GetTraceByID(ctx, tr.ID)
	if err != nil || len(spans) != 2 {
		t.Fatalf("GetTraceByID: %d spans, %v", len(spans), err)
	}
	if string(spans[0].Attributes) != string(withAttrs.Attributes) {
		t.Errorf("attributes changed on the way: %d bytes in, %d out", len(withAttrs.Attributes), len(spans[0].Attributes))
	}
	if spans[1].Attributes != nil {
		t.Errorf("a span without attributes reads back %q, want nil", spans[1].Attributes)
	}
	light, err := s.QuerySpans(ctx, store.SpanQuery{TraceIDs: []string{tr.ID}})
	if err != nil || len(light) != 2 || light[0].Attributes != nil {
		t.Errorf("a read without bodies should not carry the originals: %v, %d spans", err, len(light))
	}
	full, err := s.QuerySpans(ctx, store.SpanQuery{TraceIDs: []string{tr.ID}, Bodies: true})
	if err != nil || len(full) != 2 || string(full[0].Attributes) != string(withAttrs.Attributes) {
		t.Errorf("a read with bodies should carry them: %v", err)
	}
}
