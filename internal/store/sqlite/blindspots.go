package sqlite

import (
	"context"
	"fmt"

	"github.com/jhisse/spoor/internal/store"
)

func (s *Store) BlindSpots(ctx context.Context) (store.BlindSpots, error) {
	var b store.BlindSpots
	gen := string(store.SpanKindGeneration)
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM traces),
		(SELECT COUNT(*) FROM spans),
		(SELECT COUNT(*) FROM spans WHERE kind = ?),
		(SELECT COUNT(*) FROM traces t WHERE (SELECT MIN(parent_span_id IS NOT NULL) FROM spans WHERE trace_id = t.id) = 1),
		(SELECT COUNT(*) FROM spans c WHERE parent_span_id IS NOT NULL
			AND NOT EXISTS (SELECT 1 FROM spans p WHERE p.trace_id = c.trace_id AND p.id = c.parent_span_id)),
		(SELECT COUNT(*) FROM spans WHERE kind = ? AND input_tokens IS NULL AND status != ?),
		(SELECT COUNT(*) FROM spans WHERE kind = ? AND model IS NULL),
		(SELECT `+cacheUnknownSQL+` FROM spans sp),
		(SELECT COUNT(*) FROM spans WHERE kind = ? AND input IS NOT NULL AND input NOT LIKE '[%')`,
		gen, gen, store.StatusError, gen, gen).Scan(&b.Traces, &b.Spans, &b.Generations, &b.Rootless, &b.Orphans,
		&b.NoInputTokens, &b.NoModel, &b.CacheUnknown, &b.NoMessages)
	if err != nil {
		return b, fmt.Errorf("counting blind spots: %w", err)
	}
	b.ModelSpans, err = queryAll(ctx, s.db, func(sc scanner) (m store.ModelCount, err error) { return m, sc.Scan(&m.Model, &m.Spans) },
		`SELECT model, COUNT(*) FROM spans WHERE kind = ? AND model IS NOT NULL GROUP BY model ORDER BY model`, gen)
	if err != nil {
		return b, fmt.Errorf("counting spans per model: %w", err)
	}
	return b, nil
}
