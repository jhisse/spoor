package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// measureSQL is m as an expression over traces t, usable in any trace
// query's WHERE: the cost is a correlated subquery, NULL when unknown.
func measureSQL(m store.Measure) string {
	switch m {
	case store.MeasureDuration:
		return `(julianday(t.ended_at) - julianday(t.started_at)) * 86400`
	case store.MeasureTokens:
		return `(SELECT SUM(IFNULL(input_tokens, 0) + IFNULL(output_tokens, 0)) FROM spans
			WHERE trace_id = t.id AND (input_tokens IS NOT NULL OR output_tokens IS NOT NULL))`
	}
	return `(SELECT SUM(cost_usd) FROM spans WHERE trace_id = t.id)`
}

// TraceHeatmap buckets a value by counting the bounds it reaches, the same
// comparisons the Min/Max filter makes: a cell counts what its link lists.
func (s *Store) TraceHeatmap(ctx context.Context, q store.TraceQuery, m store.Measure, start time.Time, width time.Duration, bounds []float64) ([]store.HeatCell, error) {
	whereClause, whereArgs := traceWhere(q, false)
	args := []any{start.Unix(), int64(width / time.Second)}
	for _, b := range bounds {
		args = append(args, b)
	}
	// #nosec G202 -- measureSQL and whereClause are fixed strings; values are bound.
	cells, err := queryAll(ctx, s.db, func(sc scanner) (c store.HeatCell, err error) {
		return c, sc.Scan(&c.X, &c.Y, &c.Count, &c.TraceID)
	}, `SELECT (unixepoch(started_at) - ?) / ?, `+strings.Repeat(`(v >= ?) + `, len(bounds))+`0, COUNT(*), MIN(id)
		FROM (SELECT t.id, t.started_at, `+measureSQL(m)+` AS v FROM traces t `+whereClause+`)
		WHERE v IS NOT NULL GROUP BY 1, 2 ORDER BY 1, 2`, append(args, whereArgs...)...)
	if err != nil {
		return nil, fmt.Errorf("querying trace heat map: %w", err)
	}
	return cells, nil
}
