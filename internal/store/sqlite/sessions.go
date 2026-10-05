package sqlite

import (
	"context"
	"fmt"

	"github.com/jhisse/spoor/internal/store"
)

// sessionQuery selects what scanSessionSummary reads; callers append
// their WHERE and GROUP BY t.session_id.
const sessionQuery = `SELECT
	t.session_id,
	(SELECT f.id FROM traces f WHERE f.session_id = t.session_id ORDER BY f.started_at, f.id LIMIT 1),
	MIN(t.started_at),
	MAX(t.ended_at),
	COUNT(DISTINCT t.id),
	CASE
		WHEN MAX(CASE WHEN t.status = 'error' THEN 1 ELSE 0 END) = 1 THEN 'error'
		WHEN MIN(CASE WHEN t.status = 'ok' THEN 1 ELSE 0 END) = 1 THEN 'ok'
		ELSE 'unset'
	END,
	SUM(CASE WHEN sp.cost_usd IS NOT NULL THEN sp.cost_usd END),
	COALESCE(SUM(sp.input_tokens), 0),
	COALESCE(SUM(sp.output_tokens), 0),
	COALESCE(SUM(sp.cache_read_tokens), 0),
	COALESCE(SUM(sp.cache_write_tokens), 0),
	` + cacheUnknownSQL + `
	FROM traces t LEFT JOIN spans sp ON sp.trace_id = t.id `

func (s *Store) QuerySessions(ctx context.Context, service string, cursor *store.Cursor, limit int) ([]store.SessionSummary, *store.Cursor, error) {
	if limit <= 0 || limit > maxTraceLimit {
		limit = defaultTraceLimit
	}
	args := []any{service}
	having := ""
	if cursor != nil {
		having = "HAVING (MAX(t.ended_at), t.session_id) < (?, ?)"
		args = append(args, formatTime(cursor.StartedAt), cursor.ID)
	}
	sessions, err := queryAll(ctx, s.db, scanSessionSummary, sessionQuery+`
		WHERE (?1 = '' OR t.service = ?1) AND t.session_id IS NOT NULL AND t.session_id != ''
		GROUP BY t.session_id
		`+having+`
		ORDER BY MAX(t.ended_at) DESC, t.session_id DESC
		LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return nil, nil, fmt.Errorf("querying sessions: %w", err)
	}
	var next *store.Cursor
	if len(sessions) > limit {
		last := sessions[limit-1]
		next = &store.Cursor{StartedAt: last.EndedAt, ID: last.ID}
		sessions = sessions[:limit]
	}
	return sessions, next, nil
}

func (s *Store) GetSession(ctx context.Context, sessionID string) (store.SessionSummary, []store.TraceSummary, error) {
	const where = `WHERE t.session_id = ? `
	session, err := scanSessionSummary(s.db.QueryRowContext(ctx,
		sessionQuery+where+`GROUP BY t.session_id`, sessionID))
	if err := notFound(err, "session"); err != nil {
		return store.SessionSummary{}, nil, err
	}
	traces, err := queryAll(ctx, s.db, scanTraceSummary,
		traceSummaryQuery+where+`GROUP BY t.id ORDER BY t.started_at ASC, t.id ASC`, sessionID)
	if err != nil {
		return store.SessionSummary{}, nil, fmt.Errorf("querying session turns: %w", err)
	}
	return session, traces, nil
}

func scanSessionSummary(sc scanner) (s store.SessionSummary, err error) {
	return s, sc.Scan(&s.ID, &s.FirstTraceID, at{&s.StartedAt}, at{&s.EndedAt}, &s.TraceCount, &s.Status, &s.TotalCostUSD,
		&s.TotalInputTokens, &s.TotalOutputTokens, &s.TotalCacheReadTokens, &s.TotalCacheWriteTokens, &s.CacheUnknown)
}
