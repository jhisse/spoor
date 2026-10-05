package sqlite

import (
	"context"
	"fmt"
	"time"
)

// SweepExpired deletes by trace, not by each row's own age: a span of a
// later batch is younger than its trace row and would otherwise outlive it.
// spans.trace_id has no foreign key, so the second statement is the cascade;
// it also removes any span whose trace is gone for another reason.
// That statement walks the spans primary-key index on every sweep;
// delete by the swept trace ids if a sweep ever shows up in a profile.
func (s *Store) SweepExpired(ctx context.Context, cutoff time.Time) (deletedTraces, deletedSpans int64, err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, fmt.Errorf("starting sweep: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	traces, err := tx.ExecContext(ctx, `DELETE FROM traces WHERE created_at < ?`, formatTime(cutoff))
	if err != nil {
		return 0, 0, fmt.Errorf("sweeping traces: %w", err)
	}
	spans, err := tx.ExecContext(ctx, `DELETE FROM spans WHERE NOT EXISTS (SELECT 1 FROM traces WHERE id = spans.trace_id)`)
	if err != nil {
		return 0, 0, fmt.Errorf("sweeping spans: %w", err)
	}
	deletedTraces, _ = traces.RowsAffected()
	deletedSpans, _ = spans.RowsAffected()
	return deletedTraces, deletedSpans, tx.Commit()
}
