package sqlite

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// spanColumns is the select list scanSpan reads, and InsertSpan's column list.
const spanColumns = `trace_id, id, parent_span_id, kind, name,
	started_at, ended_at, status, model, input, output,
	input_tokens, output_tokens, cost_usd, metadata, created_at,
	cache_read_tokens, cache_write_tokens, reasoning_tokens,
	status_message, events, links, price_pattern, price_fingerprint, attributes`

func (s writer) InsertSpan(ctx context.Context, sp store.Span) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO spans (`+spanColumns+`) VALUES (?`+strings.Repeat(", ?", 24)+`)
		ON CONFLICT (trace_id, id) DO NOTHING`,
		sp.TraceID, sp.ID, sp.ParentSpanID, string(sp.Kind), sp.Name,
		formatTime(sp.StartedAt), formatTime(sp.EndedAt), string(sp.Status),
		sp.Model, sp.Input, sp.Output, sp.InputTokens, sp.OutputTokens, sp.CostUSD,
		nullableText(sp.Metadata), formatTime(sp.CreatedAt),
		sp.CacheReadTokens, sp.CacheWriteTokens, sp.ReasoningTokens,
		sp.StatusMessage, nullableText(sp.Events), nullableText(sp.Links), sp.PricePattern, sp.PriceFingerprint,
		packAttributes(sp.Attributes),
	)
	if err != nil {
		return fmt.Errorf("inserting span: %w", err)
	}
	return nil
}

func (s *Store) QuerySpans(ctx context.Context, q store.SpanQuery) ([]store.Span, error) {
	where, args := []string{"TRUE"}, []any{}
	add := func(cond string, arg ...any) {
		where, args = append(where, cond), append(args, arg...)
	}
	if q.Service != "" {
		add("trace_id IN (SELECT id FROM traces WHERE service = ?)", q.Service)
	}
	if q.SessionID != "" {
		add("trace_id IN (SELECT id FROM traces WHERE session_id = ?)", q.SessionID)
	}
	if len(q.TraceIDs) > 0 {
		ids := make([]any, len(q.TraceIDs))
		for i, id := range q.TraceIDs {
			ids[i] = id
		}
		add("trace_id IN (?"+strings.Repeat(",?", len(ids)-1)+")", ids...)
	}
	if q.Kind != "" && (q.SessionID != "" || len(q.TraceIDs) > 0) {
		// The unary plus keeps the planner on the traces named above, not on every span of the kind.
		add("+kind = ?", string(q.Kind))
	} else if q.Kind != "" {
		add("kind = ?", string(q.Kind))
	}
	if q.From != nil {
		add("started_at >= ?", formatTime(*q.From))
	}
	if q.To != nil {
		add("started_at <= ?", formatTime(*q.To))
	}
	if q.First {
		add("started_at = (SELECT MIN(o.started_at) FROM spans o WHERE o.trace_id = spans.trace_id AND o.kind = spans.kind)")
	}
	cols, scan := spanColumns, scanSpan
	if q.Usage { // every one of these columns is in idx_spans_trace_usage
		cols, scan = `trace_id, id, parent_span_id, kind, started_at, ended_at, model,
			input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, cost_usd, price_pattern`, scanUsage
	} else if !q.Bodies { // the bodies, and the originals they were read from
		cols = strings.NewReplacer("input, output", "NULL, NULL", "attributes", "NULL").Replace(cols)
	}
	return s.selectSpans(ctx, cols, scan, where, args...)
}

func scanUsage(sc scanner) (sp store.Span, err error) {
	return sp, sc.Scan(&sp.TraceID, &sp.ID, &sp.ParentSpanID, &sp.Kind, at{&sp.StartedAt}, at{&sp.EndedAt}, &sp.Model,
		&sp.InputTokens, &sp.OutputTokens, &sp.CacheReadTokens, &sp.CacheWriteTokens, &sp.CostUSD, &sp.PricePattern)
}

func (s *Store) selectSpans(ctx context.Context, cols string, scan func(scanner) (store.Span, error), where []string, args ...any) ([]store.Span, error) {
	// #nosec G202 -- cols and where are fixed strings from this file; values are bound.
	spans, err := queryAll(ctx, s.db, scan,
		`SELECT `+cols+` FROM spans WHERE `+strings.Join(where, " AND ")+` ORDER BY started_at ASC`, args...)
	if err != nil {
		return nil, fmt.Errorf("querying spans: %w", err)
	}
	return spans, nil
}

func (s *Store) UpdateSpanUsage(ctx context.Context, spans []store.Span) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("starting span usage update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, sp := range spans {
		if _, err := tx.ExecContext(ctx, `
			UPDATE spans SET input_tokens = ?, output_tokens = ?, cache_read_tokens = ?,
				cache_write_tokens = ?, reasoning_tokens = ?, cost_usd = ?, price_pattern = ?, price_fingerprint = ?
			WHERE trace_id = ? AND id = ?`,
			sp.InputTokens, sp.OutputTokens, sp.CacheReadTokens,
			sp.CacheWriteTokens, sp.ReasoningTokens, sp.CostUSD, sp.PricePattern, sp.PriceFingerprint, sp.TraceID, sp.ID); err != nil {
			return fmt.Errorf("updating span usage: %w", err)
		}
	}
	return tx.Commit()
}

func (s *Store) SpanPricings(ctx context.Context) ([]store.SpanPricing, error) {
	pricings, err := queryAll(ctx, s.db, func(sc scanner) (p store.SpanPricing, err error) {
		return p, sc.Scan(&p.Model, &p.Pattern, &p.Fingerprint, &p.Spans)
	}, `SELECT model, price_pattern, price_fingerprint, COUNT(*) FROM spans
		WHERE price_fingerprint IS NOT NULL GROUP BY model, price_pattern, price_fingerprint`)
	if err != nil {
		return nil, fmt.Errorf("querying span pricings: %w", err)
	}
	return pricings, nil
}

func scanSpan(sc scanner) (sp store.Span, err error) {
	var packed []byte
	if err = sc.Scan(
		&sp.TraceID, &sp.ID, &sp.ParentSpanID, &sp.Kind, &sp.Name,
		at{&sp.StartedAt}, at{&sp.EndedAt}, &sp.Status, &sp.Model, &sp.Input, &sp.Output,
		&sp.InputTokens, &sp.OutputTokens, &sp.CostUSD, (*[]byte)(&sp.Metadata), at{&sp.CreatedAt},
		&sp.CacheReadTokens, &sp.CacheWriteTokens, &sp.ReasoningTokens,
		&sp.StatusMessage, (*[]byte)(&sp.Events), (*[]byte)(&sp.Links), &sp.PricePattern, &sp.PriceFingerprint, &packed); err != nil {
		return sp, err
	}
	sp.Attributes, err = unpackAttributes(packed)
	return sp, err
}

func (s *Store) ComparableSpans(ctx context.Context, like store.Span, since time.Time, limit int) (durations []time.Duration, inputTokens []int64, err error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT started_at, ended_at, input_tokens FROM spans
		WHERE kind = ? AND name = ? AND model IS ? AND started_at >= ?
		ORDER BY started_at DESC LIMIT ?`,
		string(like.Kind), like.Name, like.Model, formatTime(since), limit,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("querying comparable spans: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var start, end time.Time
		var tokens *int64
		if err := rows.Scan(at{&start}, at{&end}, &tokens); err != nil {
			return nil, nil, fmt.Errorf("scanning comparable span: %w", err)
		}
		durations = append(durations, end.Sub(start))
		if tokens != nil {
			inputTokens = append(inputTokens, *tokens)
		}
	}
	return durations, inputTokens, rows.Err()
}
