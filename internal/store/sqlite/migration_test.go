package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// A batch that fails part way leaves nothing behind, so the sender's retry
// of the whole request is safe. The failure is forced by a trigger: no
// well-formed span is refused by the schema itself.
func TestIngestBatchFailureLeavesNothingWritten(t *testing.T) {
	s := newTestStore(t).(*Store)
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER refuse BEFORE INSERT ON spans WHEN new.id = 'b' BEGIN SELECT RAISE(ABORT, 'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	batch := []store.Span{}
	for _, id := range []string{"a", "b"} {
		batch = append(batch, store.Span{TraceID: "t1", ID: id, Kind: store.SpanKindGeneric, Name: id, StartedAt: now, EndedAt: now, Status: store.StatusOK, CreatedAt: now})
	}
	traces := []store.TraceMerge{{Trace: store.Trace{ID: "t1", Name: "t1", StartedAt: now, EndedAt: now, Status: store.StatusOK, CreatedAt: now}, HasRoot: true}}
	if err := s.IngestBatch(ctx, traces, batch); err == nil {
		t.Fatal("IngestBatch with a refused span: no error")
	}
	var n int
	if err := s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM spans) + (SELECT COUNT(*) FROM traces)`).Scan(&n); err != nil || n != 0 {
		t.Errorf("%d rows left by the failed batch (err %v), want 0", n, err)
	}

	if _, err := s.db.Exec(`DROP TRIGGER refuse`); err != nil {
		t.Fatal(err)
	}
	if err := s.IngestBatch(ctx, traces, batch); err != nil {
		t.Fatalf("IngestBatch (retry): %v", err)
	}
	if _, got, err := s.GetTraceByID(ctx, "t1"); err != nil || len(got) != 2 {
		t.Errorf("after the retry: %d spans, err %v; want 2", len(got), err)
	}
}
