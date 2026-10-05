package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

const (
	defaultTraceLimit = 50
	maxTraceLimit     = 200
)

// MergeTrace does the whole merge inside the upsert: SQLite runs one
// statement atomically, so no other batch can write between the read of
// the stored row and the update. created_at keeps its first value.
func (s writer) MergeTrace(ctx context.Context, t store.Trace, hasRoot bool) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO traces (`+traceColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET
			service    = COALESCE(traces.service, excluded.service),
			session_id = CASE WHEN ?10 THEN COALESCE(excluded.session_id, traces.session_id)
				ELSE COALESCE(traces.session_id, excluded.session_id) END,
			name       = CASE WHEN ?10 OR traces.name = '' THEN excluded.name ELSE traces.name END,
			started_at = MIN(traces.started_at, excluded.started_at),
			ended_at   = MAX(traces.ended_at, excluded.ended_at),
			status     = CASE WHEN 'error' IN (traces.status, excluded.status) THEN 'error'
				WHEN ?10 THEN excluded.status ELSE traces.status END,
			metadata   = COALESCE(excluded.metadata, traces.metadata)`,
		t.ID, t.Service, t.SessionID, t.Name,
		formatTime(t.StartedAt), formatTime(t.EndedAt), string(t.Status),
		nullableText(t.Metadata), formatTime(t.CreatedAt), hasRoot)
	if err != nil {
		return fmt.Errorf("merging trace: %w", err)
	}
	return nil
}

func (s *Store) GetTraceByID(ctx context.Context, traceID string) (store.Trace, []store.Span, error) {
	t, err := scanTrace(s.db.QueryRowContext(ctx, `SELECT `+traceColumns+` FROM traces WHERE id = ?`, traceID))
	if err := notFound(err, "trace"); err != nil {
		return store.Trace{}, nil, err
	}
	spans, err := s.selectSpans(ctx, spanColumns, scanSpan, []string{"trace_id = ?"}, traceID)
	return t, spans, err
}

func (s *Store) GetTraceSummary(ctx context.Context, traceID string) (store.TraceSummary, error) {
	t, err := scanTraceSummary(s.db.QueryRowContext(ctx, traceSummaryQuery+`WHERE t.id = ? GROUP BY t.id`, traceID))
	return t, notFound(err, "trace")
}

func (s *Store) QueryTraces(ctx context.Context, q store.TraceQuery) ([]store.TraceSummary, *store.Cursor, error) {
	limit := q.Limit
	if limit <= 0 || limit > maxTraceLimit {
		limit = defaultTraceLimit
	}
	whereClause, args := traceWhere(q, true)
	// #nosec G202 -- traceSummaryQuery and whereClause are fixed strings; every
	// user-controlled value is bound via args.
	// The page is chosen before the spans are joined, so the index on
	// started_at stops the scan at the limit instead of aggregating every trace.
	summaries, err := queryAll(ctx, s.db, scanTraceSummary, strings.Replace(traceSummaryQuery, "FROM traces t",
		"FROM (SELECT * FROM traces t "+whereClause+" ORDER BY t.started_at DESC, t.id DESC LIMIT ?) t", 1)+`
		GROUP BY t.id ORDER BY t.started_at DESC, t.id DESC`,
		append(args, limit+1)...) // one extra row tells whether a next page exists
	if err != nil {
		return nil, nil, fmt.Errorf("querying traces: %w", err)
	}
	var next *store.Cursor
	if len(summaries) > limit {
		last := summaries[limit-1]
		next = &store.Cursor{StartedAt: last.StartedAt, ID: last.ID}
		summaries = summaries[:limit]
	}
	return summaries, next, nil
}

// traceColumns is the select list scanTrace reads.
const traceColumns = `id, service, session_id, name, started_at, ended_at, status, metadata, created_at`

// traceSummaryQuery selects what scanTraceSummary reads; callers append
// their WHERE and GROUP BY t.id.
//
// The cost sum is left un-COALESCEd: SUM over no non-NULL value is NULL,
// read as "no span in this trace has a cost", distinct from a real total
// of zero. Models lists the generation spans' models, the one that cost
// most first (a model with no cost last, ties by calls, then by name).
const traceSummaryQuery = `SELECT t.id, t.service, t.session_id, t.name, t.started_at, t.ended_at,
	t.status, t.metadata, t.created_at,
	SUM(sp.cost_usd),
	COALESCE(SUM(sp.input_tokens), 0),
	COALESCE(SUM(sp.output_tokens), 0),
	COALESCE(SUM(sp.cache_read_tokens), 0),
	COALESCE(SUM(sp.cache_write_tokens), 0),
	(SELECT GROUP_CONCAT(m ORDER BY c DESC, n DESC, m) FROM (SELECT model m, SUM(cost_usd) c, COUNT(*) n
		FROM spans WHERE trace_id = t.id AND kind = 'generation' AND model IS NOT NULL GROUP BY model)),
	COALESCE(MIN(sp.parent_span_id IS NOT NULL), 0),
	` + cacheUnknownSQL + `,
	COUNT(sp.id), COALESCE(SUM(sp.status = 'error'), 0)
	FROM traces t LEFT JOIN spans sp ON sp.trace_id = t.id `

// cacheUnknownSQL counts the spans store.Span.CacheUnknown describes.
const cacheUnknownSQL = `COALESCE(SUM(sp.price_pattern IS NOT NULL AND sp.cache_read_tokens IS NULL
	AND sp.cache_write_tokens IS NULL AND sp.input_tokens > 0), 0)`

func traceWhere(q store.TraceQuery, withCursor bool) (string, []any) {
	var where []string
	var args []any
	add := func(cond string, arg ...any) {
		where, args = append(where, cond), append(args, arg...)
	}
	if q.Service != "" {
		add("t.service = ?", q.Service)
	}
	if q.From != nil {
		add("t.started_at >= ?", formatTime(*q.From))
	}
	if q.To != nil {
		add("t.started_at <= ?", formatTime(*q.To))
	}
	if q.Status != nil {
		add("t.status = ?", string(*q.Status))
	}
	for _, m := range []store.Measure{store.MeasureCost, store.MeasureDuration, store.MeasureTokens} {
		if r := q.Ranges[m]; r.Min > 0 {
			add(measureSQL(m)+" >= ?", r.Min)
		}
		if r := q.Ranges[m]; r.Max > 0 {
			add(measureSQL(m)+" < ?", r.Max)
		}
	}
	if conds, spanArgs := spanWhere(q.Span); len(conds) > 0 {
		add("EXISTS (SELECT 1 FROM spans s WHERE s.trace_id = t.id AND "+strings.Join(conds, " AND ")+")", spanArgs...)
	}
	if withCursor && q.Cursor != nil {
		add("(t.started_at, t.id) < (?, ?)", formatTime(q.Cursor.StartedAt), q.Cursor.ID)
	}
	if len(where) == 0 {
		return "", args
	}
	return "WHERE " + strings.Join(where, " AND "), args
}

// TraceHistogram buckets by whole minutes so a bucket's bounds survive the
// list's minute-precision from/to filter fields unchanged.
func (s *Store) TraceHistogram(ctx context.Context, q store.TraceQuery, maxBuckets int) ([]store.TraceBucket, error) {
	whereClause, args := traceWhere(q, false)
	var lo, hi sql.NullString
	// #nosec G202 -- see QueryTraces: whereClause is fixed strings only.
	if err := s.db.QueryRowContext(ctx, `SELECT MIN(t.started_at), MAX(t.started_at) FROM traces t `+whereClause, args...).Scan(&lo, &hi); err != nil {
		return nil, fmt.Errorf("querying trace time range: %w", err)
	}
	if !lo.Valid {
		return nil, nil
	}
	var first, last time.Time
	if err := errors.Join(at{&first}.Scan(lo.String), at{&last}.Scan(hi.String)); err != nil {
		return nil, fmt.Errorf("parsing trace time range: %w", err)
	}
	start := first.Truncate(time.Minute)
	width := (last.Sub(start)/time.Duration(maxBuckets) + time.Minute).Truncate(time.Minute)
	buckets := make([]store.TraceBucket, last.Sub(start)/width+1)
	for i := range buckets {
		buckets[i].Start = start.Add(time.Duration(i) * width)
		buckets[i].End = buckets[i].Start.Add(width)
	}

	// #nosec G202 -- as above.
	rows, err := s.db.QueryContext(ctx, `
		SELECT (unixepoch(t.started_at) - ?) / ?,
		       SUM(t.status = 'ok'), SUM(t.status = 'error'), SUM(t.status NOT IN ('ok', 'error'))
		FROM traces t `+whereClause+` GROUP BY 1`,
		append([]any{start.Unix(), int64(width / time.Second)}, args...)...)
	if err != nil {
		return nil, fmt.Errorf("querying trace histogram: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var i int
		var b store.TraceBucket
		if err := rows.Scan(&i, &b.OK, &b.Error, &b.Unset); err != nil {
			return nil, fmt.Errorf("scanning trace histogram: %w", err)
		}
		if i < 0 || i >= len(buckets) { // a trace stored after the range was read
			continue
		}
		buckets[i].OK, buckets[i].Error, buckets[i].Unset = b.OK, b.Error, b.Unset
	}
	return buckets, rows.Err()
}

// AdjacentTraces' two queries mirror each other: >/ASC for the newer
// neighbour, </DESC for the older. No row is a nil trace, not an error.
func (s *Store) AdjacentTraces(ctx context.Context, cur store.Cursor) (prev, next *store.Trace, err error) {
	neighbour := func(cmp, order string) (*store.Trace, error) {
		// #nosec G202 -- cmp and order are the literals passed below.
		t, err := scanTrace(s.db.QueryRowContext(ctx, `SELECT `+traceColumns+` FROM traces
			WHERE (started_at, id) `+cmp+` (?, ?) ORDER BY started_at `+order+`, id `+order+` LIMIT 1`,
			formatTime(cur.StartedAt), cur.ID))
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("querying adjacent trace: %w", err)
		}
		return &t, nil
	}
	if prev, err = neighbour(">", "ASC"); err != nil {
		return nil, nil, err
	}
	next, err = neighbour("<", "DESC")
	return prev, next, err
}

func (s *Store) ListServices(ctx context.Context) ([]string, error) {
	services, err := queryAll(ctx, s.db, func(sc scanner) (v string, err error) { return v, sc.Scan(&v) },
		`SELECT DISTINCT service FROM traces WHERE service IS NOT NULL ORDER BY service`)
	if err != nil {
		return nil, fmt.Errorf("querying services: %w", err)
	}
	return services, nil
}

func traceDest(t *store.Trace) []any {
	return []any{&t.ID, &t.Service, &t.SessionID, &t.Name, at{&t.StartedAt}, at{&t.EndedAt},
		&t.Status, (*[]byte)(&t.Metadata), at{&t.CreatedAt}}
}

func scanTrace(sc scanner) (t store.Trace, err error) {
	return t, sc.Scan(traceDest(&t)...)
}

func scanTraceSummary(sc scanner) (t store.TraceSummary, err error) {
	var models sql.NullString
	err = sc.Scan(append(traceDest(&t.Trace), &t.TotalCostUSD, &t.TotalInputTokens, &t.TotalOutputTokens,
		&t.TotalCacheReadTokens, &t.TotalCacheWriteTokens, &models, &t.Rootless, &t.CacheUnknown, &t.SpanCount, &t.ErrorSpans)...)
	if models.String != "" {
		t.Models = strings.Split(models.String, ",")
	}
	return t, err
}
