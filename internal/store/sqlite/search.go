package sqlite

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/jhisse/spoor/internal/store"
)

// snippet() picks the column with the match and keeps about 24 words of it.
const (
	searchSnippet = `snippet(spans_fts, -1, ?, ?, '…', 24)`
	searchFrom    = ` FROM spans_fts JOIN spans s ON s.rowid = spans_fts.rowid
		JOIN traces t ON t.id = s.trace_id
		WHERE spans_fts MATCH ? `
)

// ftsQuery turns typed text into an FTS5 expression: every term a quoted
// phrase (a prefix one unless the person quoted it), the positive ones all
// required, each excluded one after a NOT. Quoting stops FTS5 syntax in the
// text (AND, NEAR, a column filter) from being run; a control character
// becomes a space because FTS5 ends its input at a NUL. "" means nothing to
// look for: FTS5 cannot ask for "everything but".
func ftsQuery(text string) string {
	var must, not string
	for _, t := range store.ParseSearch(text) {
		q := `"` + strings.ReplaceAll(strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return ' '
			}
			return r
		}, t.Text), `"`, `""`) + `"`
		if !t.Phrase {
			q += "*"
		}
		if t.Not {
			not += " NOT " + q
		} else {
			must += " " + q
		}
	}
	if must == "" {
		return ""
	}
	return must[1:] + not
}

func spanWhere(f store.SpanFilter) (where []string, args []any) {
	add := func(cond string, arg ...any) { where, args = append(where, cond), append(args, arg...) }
	if f.Kind != "" {
		add("s.kind = ?", string(f.Kind))
	}
	if f.Name != "" {
		add("s.name = ?", f.Name)
	}
	if f.Model != "" {
		add("s.model = ?", f.Model)
	}
	if f.Failed {
		add("s.status = 'error'")
	}
	if match := ftsQuery(f.Text); match != "" {
		// The unary plus keeps the planner from seeking each matching rowid per trace.
		add("+s.rowid IN (SELECT rowid FROM spans_fts WHERE spans_fts MATCH ?)", match)
	} else if f.Text != "" {
		add("0")
	}
	return where, args
}

func (s *Store) MatchingSpans(ctx context.Context, f store.SpanFilter, traceIDs []string) ([]store.SpanHit, error) {
	where, args := spanWhere(f)
	if len(where) == 0 || len(traceIDs) == 0 {
		return nil, nil
	}
	for _, id := range traceIDs {
		args = append(args, id)
	}
	var rowids []int64 // parallel to hits
	// #nosec G202 -- where is fixed strings; every value is bound.
	hits, err := queryAll(ctx, s.db, func(sc scanner) (h store.SpanHit, err error) {
		rowids = append(rowids, 0)
		return h, sc.Scan(&h.TraceID, &h.SpanID, &h.SpanName, &h.Kind, &h.Status, &rowids[len(rowids)-1])
	}, `SELECT s.trace_id, s.id, s.name, s.kind, s.status, s.rowid FROM spans s WHERE `+strings.Join(where, " AND ")+`
		AND s.trace_id IN (?`+strings.Repeat(",?", len(traceIDs)-1)+`) ORDER BY s.trace_id, s.started_at, s.id`, args...)
	match := ftsQuery(f.Text)
	if err != nil || match == "" || len(hits) == 0 {
		return hits, err
	}
	// snippet() reads the span's text back: only for each trace's first hit.
	first := map[int64]*store.SpanHit{}
	args = []any{store.HitStart, store.HitEnd, match}
	for i := range hits {
		if i == 0 || hits[i].TraceID != hits[i-1].TraceID {
			first[rowids[i]], args = &hits[i], append(args, rowids[i])
		}
	}
	// #nosec G202 -- only placeholders are concatenated.
	_, err = queryAll(ctx, s.db, func(sc scanner) (rowid int64, err error) {
		var snippet string
		err = sc.Scan(&rowid, &snippet)
		first[rowid].Snippet = snippet
		return rowid, err
	}, `SELECT rowid, `+searchSnippet+` FROM spans_fts WHERE spans_fts MATCH ? AND rowid IN (?`+strings.Repeat(",?", len(first)-1)+`)`, args...)
	if err != nil {
		return nil, fmt.Errorf("reading search snippets: %w", err)
	}
	return hits, nil
}

func (s *Store) ListSpanNames(ctx context.Context, kind store.SpanKind) ([]string, error) {
	names, err := queryAll(ctx, s.db, func(sc scanner) (n string, err error) { return n, sc.Scan(&n) },
		`SELECT DISTINCT name FROM spans WHERE kind = ? ORDER BY name`, string(kind))
	if err != nil {
		return nil, fmt.Errorf("querying span names: %w", err)
	}
	return names, nil
}
