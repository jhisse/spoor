package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jhisse/spoor/internal/store"
)

// writer runs the two ingest statements on either the pool or one
// transaction: Store embeds it over the pool, IngestBatch builds one over
// a transaction.
type writer struct {
	db interface {
		ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	}
}

// IngestBatch commits a whole OTLP request once: one fsync per request
// instead of one per span, and no reader sees half of it. The transaction
// only writes, so it takes the write lock at its first statement and
// waits out other writers under busy_timeout (no read snapshot to go stale).
func (s *Store) IngestBatch(ctx context.Context, traces []store.TraceMerge, spans []store.Span) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning ingest: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	w := writer{tx}
	for _, sp := range spans {
		if err := w.InsertSpan(ctx, sp); err != nil {
			return err
		}
	}
	for _, t := range traces {
		if err := w.MergeTrace(ctx, t.Trace, t.HasRoot); err != nil {
			return err
		}
	}
	return tx.Commit()
}
