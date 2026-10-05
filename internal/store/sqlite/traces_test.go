package sqlite

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// seedTracesOneMinuteApart inserts n traces "t1".."tN", t1 newest, one
// minute apart, and returns them in that (newest-first) order.
func seedTracesOneMinuteApart(t *testing.T, s store.Store, n int, now time.Time) []store.Trace {
	t.Helper()
	traces := make([]store.Trace, n)
	for i := range traces {
		tr := store.Trace{
			ID:        fmt.Sprintf("t%d", i+1),
			Name:      fmt.Sprintf("t%d", i+1),
			StartedAt: now.Add(-time.Duration(i) * time.Minute),
			EndedAt:   now.Add(-time.Duration(i) * time.Minute).Add(time.Second),
			Status:    store.StatusOK,
			CreatedAt: now,
		}
		traces[i] = tr
		if err := s.MergeTrace(context.Background(), tr, true); err != nil {
			t.Fatalf("MergeTrace(%s): %v", tr.ID, err)
		}
	}
	return traces
}

// A trace inserted between two page fetches must not repeat or lose a row:
// an OFFSET would shift here, keyset must not.
func TestQueryTracesPaginationImmuneToInsertion(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	now := time.Now()
	seedTracesOneMinuteApart(t, s, 5, now) // t1 newest .. t5 oldest

	page1, cursor, err := s.QueryTraces(ctx, store.TraceQuery{Limit: 3})
	if err != nil {
		t.Fatalf("QueryTraces page1: %v", err)
	}
	if len(page1) != 3 || page1[0].ID != "t1" || page1[1].ID != "t2" || page1[2].ID != "t3" {
		t.Fatalf("page1 = %+v, want [t1,t2,t3]", page1)
	}
	if cursor == nil {
		t.Fatal("page1 next cursor = nil, want non-nil")
	}

	// Lands between t2 and t3, on page 1, after the cursor was taken: it must
	// not appear on page 2.
	newTr := store.Trace{
		ID:        "t-new",
		Name:      "t-new",
		StartedAt: now.Add(-90 * time.Second),
		EndedAt:   now.Add(-90 * time.Second).Add(time.Second),
		Status:    store.StatusOK,
		CreatedAt: now,
	}
	if err := s.MergeTrace(ctx, newTr, true); err != nil {
		t.Fatalf("MergeTrace(t-new): %v", err)
	}

	page2, cursor2, err := s.QueryTraces(ctx, store.TraceQuery{Limit: 3, Cursor: cursor})
	if err != nil {
		t.Fatalf("QueryTraces page2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != "t4" || page2[1].ID != "t5" {
		t.Fatalf("page2 = %+v, want [t4,t5] (t-new must be excluded, no duplicate/lost row)", page2)
	}
	if cursor2 != nil {
		t.Errorf("page2 next cursor = %+v, want nil (no rows remain)", cursor2)
	}
}
