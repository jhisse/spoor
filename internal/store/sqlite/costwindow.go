package sqlite

import (
	"context"
	"fmt"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func (s *Store) CostSince(ctx context.Context, q store.TraceQuery, since time.Time) (store.CostWindow, error) {
	q.From = &since
	whereClause, args := traceWhere(q, false)
	var w store.CostWindow
	// #nosec G202 -- whereClause is built by traceWhere from placeholders only.
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(SUM(sp.cost_usd), 0), COUNT(DISTINCT t.id),
		COUNT(DISTINCT t.id) - COUNT(DISTINCT CASE WHEN sp.cost_usd IS NOT NULL THEN t.id END)
		FROM traces t LEFT JOIN spans sp ON sp.trace_id = t.id `+whereClause, args...).Scan(&w.USD, &w.Traces, &w.Uncosted)
	if err != nil {
		return w, fmt.Errorf("summing cost since %s: %w", since.UTC().Format(time.RFC3339), err)
	}
	return w, nil
}
