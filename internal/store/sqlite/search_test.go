package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func seedSearchable(t *testing.T, s *Store, id, input string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	if err := s.MergeTrace(ctx, store.Trace{ID: id, Name: id, StartedAt: now, EndedAt: now, Status: store.StatusOK, CreatedAt: now}, true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}
	sp := store.Span{TraceID: id, ID: id, Kind: store.SpanKindGeneric, Name: id, StartedAt: now, EndedAt: now, Status: store.StatusOK, Input: &input, CreatedAt: now}
	if err := s.InsertSpan(ctx, sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}
}

func searchIDs(t *testing.T, s *Store, text string) []string {
	t.Helper()
	hits, err := s.MatchingSpans(context.Background(), store.SpanFilter{Text: text}, []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("MatchingSpans(%q): %v", text, err)
	}
	ids := []string{}
	for _, h := range hits {
		ids = append(ids, h.SpanID)
	}
	return ids
}

// The index finds its span by rowid, and spans has no INTEGER PRIMARY KEY:
// SQLite's documentation allows VACUUM to renumber such rowids. The pinned
// driver keeps them; this fails if an upgrade stops doing so. A changed
// indexed column must be re-indexed too.
func TestSearchSurvivesVacuumAndUpdate(t *testing.T) {
	s := newTestStore(t).(*Store)
	seedSearchable(t, s, "a", "alpha")
	seedSearchable(t, s, "b", "bravo")
	seedSearchable(t, s, "c", "charlie")
	for _, stmt := range []string{
		`DELETE FROM spans WHERE id = 'a'`,
		`VACUUM`,
		`UPDATE spans SET input = 'delta' WHERE id = 'b'`,
		`INSERT INTO spans_fts (spans_fts, rank) VALUES ('integrity-check', 1)`,
	} {
		if _, err := s.db.Exec(stmt); err != nil {
			t.Fatalf("%s: %v", stmt, err)
		}
	}
	for text, want := range map[string]string{"alpha": "", "bravo": "", "delta": "b", "charlie": "c"} {
		got := searchIDs(t, s, text)
		if (want == "" && len(got) != 0) || (want != "" && (len(got) != 1 || got[0] != want)) {
			t.Errorf("search %q = %v, want [%s]", text, got, want)
		}
	}
}

func TestFTSQuery(t *testing.T) {
	for text, want := range map[string]string{
		"get_stock_price":      `"get_stock_price"*`,
		" two  words ":         `"two"* "words"*`,
		`say "hi there" OR x`:  `"say"* "hi there" "OR"* "x"*`,
		`a -b -"c d" e`:        `"a"* "e"* NOT "b"* NOT "c d"`,
		"-only":                "",
		"- ( * :":              "",
		"a\x00b\nc":            `"a"* "b"* "c"*`,
		"ação NEAR(a, b)*":     `"ação"* "NEAR(a,"* "b)*"*`,
		`unclosed "quote here`: `"unclosed"* "quote here"`,
		"\"with\x00control\"":  `"with control"`,
	} {
		if got := ftsQuery(text); got != want {
			t.Errorf("ftsQuery(%q) = %s, want %s", text, got, want)
		}
	}
}
