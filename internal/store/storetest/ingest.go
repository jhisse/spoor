package storetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func testIngestBatch(t *testing.T, newStore func(t *testing.T) store.Store) {
	t.Run("StoresAndRetryIsIdempotent", func(t *testing.T) { testIngestBatchRetry(t, newStore(t)) })
	t.Run("ConcurrentBatchesOfOneTrace", func(t *testing.T) { testIngestBatchConcurrent(t, newStore(t)) })
}

// A whole batch resent (what an SDK does after a timeout) changes nothing:
// same spans, first write kept, same trace row.
func testIngestBatchRetry(t *testing.T, s store.Store) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	traces := []store.TraceMerge{
		{Trace: newTrace("t1", t0), HasRoot: true},
		{Trace: newTrace("t2", t0.Add(time.Minute)), HasRoot: true},
	}
	spans := []store.Span{newSpan("t1", "a", t0), newSpan("t1", "b", t0), newSpan("t2", "a", t0.Add(time.Minute))}
	if err := s.IngestBatch(ctx, traces, spans); err != nil {
		t.Fatalf("IngestBatch: %v", err)
	}
	before, _, err := s.GetTraceByID(ctx, "t1")
	if err != nil {
		t.Fatalf("GetTraceByID: %v", err)
	}

	spans[0].Name = "a partial resend"
	if err := s.IngestBatch(ctx, traces, spans); err != nil {
		t.Fatalf("IngestBatch (retry): %v", err)
	}
	after, got, err := s.GetTraceByID(ctx, "t1")
	if err != nil {
		t.Fatalf("GetTraceByID: %v", err)
	}
	if len(got) != 2 || got[0].Name == "a partial resend" || got[1].Name == "a partial resend" {
		t.Errorf("t1 spans after the retry = %+v, want the two first-written spans", got)
	}
	if after.Name != before.Name || !after.StartedAt.Equal(before.StartedAt) || !after.EndedAt.Equal(before.EndedAt) || after.Status != before.Status {
		t.Errorf("t1 after the retry = %+v, want unchanged %+v", after, before)
	}
	if _, got, err := s.GetTraceByID(ctx, "t2"); err != nil || len(got) != 1 {
		t.Errorf("t2: %d spans, err %v; want 1 span", len(got), err)
	}
}

// Many senders, one trace, each batch a transaction of several spans:
// every span lands and the trace row is the same as merging one by one.
func testIngestBatchConcurrent(t *testing.T, s store.Store) {
	ctx := context.Background()
	t0 := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	const senders, perBatch = 8, 20
	errs := make(chan error, senders)
	for i := range senders {
		go func() {
			at := t0.Add(time.Duration(i) * time.Second)
			tr := newTrace("t1", at)
			tr.Name, tr.Status = fmt.Sprintf("batch-%d", i), store.StatusUnset
			if i == 5 {
				tr.Name, tr.Status = "root", store.StatusOK
			}
			if i == 2 {
				tr.Status = store.StatusError
			}
			spans := make([]store.Span, perBatch)
			for j := range spans {
				spans[j] = newSpan("t1", fmt.Sprintf("s%d-%d", i, j), at)
			}
			errs <- s.IngestBatch(ctx, []store.TraceMerge{{Trace: tr, HasRoot: i == 5}}, spans)
		}()
	}
	for range senders {
		if err := <-errs; err != nil {
			t.Fatalf("IngestBatch: %v", err)
		}
	}
	got, spans, err := s.GetTraceByID(ctx, "t1")
	if err != nil {
		t.Fatalf("GetTraceByID: %v", err)
	}
	if len(spans) != senders*perBatch {
		t.Errorf("%d spans, want %d", len(spans), senders*perBatch)
	}
	if got.Name != "root" || got.Status != store.StatusError || !got.StartedAt.Equal(t0) || !got.EndedAt.Equal(t0.Add(senders*time.Second)) {
		t.Errorf("trace = %q %s %v to %v, want root, error, %v to %v", got.Name, got.Status, got.StartedAt, got.EndedAt, t0, t0.Add(senders*time.Second))
	}
}
