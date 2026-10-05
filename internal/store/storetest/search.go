package storetest

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// seedSearch writes one trace per span so each hit names its own trace.
func seedSearch(t *testing.T, s store.Store, service, id string, at time.Time, edit func(*store.Span)) {
	t.Helper()
	ctx := context.Background()
	tr := newTrace("trace-"+id, at)
	tr.Service = &service
	if err := s.MergeTrace(ctx, tr, true); err != nil {
		t.Fatalf("MergeTrace(%s): %v", id, err)
	}
	sp := newSpan("trace-"+id, id, at)
	edit(&sp)
	if err := s.InsertSpan(ctx, sp); err != nil {
		t.Fatalf("InsertSpan(%s): %v", id, err)
	}
}

// hitIDs searches through QueryTraces: the ids of the traces with a matching
// span, newest first, which seedSearch makes the span ids.
func hitIDs(t *testing.T, s store.Store, service, text string) string {
	t.Helper()
	return filterIDs(t, s, service, store.SpanFilter{Text: text})
}

func filterIDs(t *testing.T, s store.Store, service string, f store.SpanFilter) string {
	t.Helper()
	traces, _, err := s.QueryTraces(context.Background(), store.TraceQuery{Service: service, Span: f})
	if err != nil {
		t.Fatalf("QueryTraces(%+v): %v", f, err)
	}
	ids := make([]string, len(traces))
	for i, tr := range traces {
		ids[i] = strings.TrimPrefix(tr.ID, "trace-")
	}
	return strings.Join(ids, ",")
}

func testSearchSpans(t *testing.T, s store.Store) {
	now := time.Now()

	refused := "connection refused"
	filler := strings.Repeat("lorem ipsum dolor sit amet ", 200)
	pt := `[{"role":"user","content":"` + filler + `Qual a cotação da ação da Petrobras? Não consegui a informação."}]`
	call := `[{"role":"assistant","tool_calls":[{"name":"get_stock_price","arguments":"{\"ticker\":\"PETR4\"}"}]}]`
	seedSearch(t, s, "acme", "pt", now.Add(-3*time.Minute), func(sp *store.Span) { sp.Input = &pt })
	seedSearch(t, s, "acme", "call", now.Add(-2*time.Minute), func(sp *store.Span) { sp.Output = &call })
	seedSearch(t, s, "acme", "meta", now.Add(-time.Minute), func(sp *store.Span) {
		sp.Name = "fetch_url"
		sp.Metadata = []byte(`{"error.type":"ConnectionTimeout","note":"cotação indisponível"}`)
		sp.StatusMessage, sp.Events = &refused, []byte(`[{"name":"exception","attributes":{"exception.message":"ECONNREFUSED 10.0.0.1"}}]`)
		sp.Status, sp.Kind = store.StatusError, store.SpanKindTool
	})
	seedSearch(t, s, "other", "elsewhere", now, func(sp *store.Span) { sp.Input = &pt })

	for _, c := range []struct{ name, text, want string }{
		{"a word in input", "Petrobras", "pt"},
		{"case and accents are ignored", "COTACAO", "meta,pt"},
		{"accented query on the same text", "cotação", "meta,pt"},
		{"every word, any order", "petrobras ação", "pt"},
		{"all words required", "petrobras ConnectionTimeout", ""},
		{"an identifier keeps its parts together", "get_stock_price", "call"},
		{"part of an identifier, from a word start", "stock_pri", "call"},
		{"the start of a word", "Connection", "meta"},
		{"not the middle of a word", "Timeout", ""},
		{"span name", "fetch_url", "meta"},
		{"metadata value", "indisponível", "meta"},
		{"a quoted phrase: these words, in this order", `"ação da Petrobras"`, "pt"},
		{"a quoted phrase is not a set of words", `"Petrobras da ação"`, ""},
		{"a quoted word is whole, not a prefix", `"Connect"`, ""},
		{"an excluded word", "cotação -petrobras", "meta"},
		{"an excluded phrase", `cotação -"ação da Petrobras"`, "meta"},
		{"only exclusions: nothing to look for", "-petrobras", ""},
		{"status message", "refused", "meta"},
		{"event attributes", "ECONNREFUSED", "meta"},
		{"nothing to look for", ` "  - * `, ""},
		{"empty: no filter", "", "meta,call,pt"},
	} {
		if got := hitIDs(t, s, "acme", c.text); got != c.want {
			t.Errorf("%s: search %q = %q, want %q", c.name, c.text, got, c.want)
		}
	}
	if got := hitIDs(t, s, "", "Petrobras"); got != "elsewhere,pt" {
		t.Errorf("every service, newest first: got %q", got)
	}

	testSearchSpansHits(t, s, pt, now)
}

func testSearchSpansHits(t *testing.T, s store.Store, pt string, now time.Time) {
	ctx := context.Background()
	// Text that is FTS5 syntax when unquoted must be searched for, not run.
	for _, text := range []string{`"`, `ação"`, `"a" OR "b`, `NEAR(a b)`, `input:ação`, `a AND`, `-ação`, `ação*`, `(`, `^a`, `a + b`, "a\x00b", `a NOT`, `-"`} {
		if _, _, err := s.QueryTraces(ctx, store.TraceQuery{Span: store.SpanFilter{Text: text}}); err != nil {
			t.Errorf("search %q: %v", text, err)
		}
	}
	if got := hitIDs(t, s, "acme", "input:ação"); got != "" {
		t.Errorf(`"input:ação" must look for the word input, got %q`, got)
	}

	// Every set field describes the same span: meta is the one failed tool.
	for _, c := range []struct {
		f    store.SpanFilter
		want string
	}{
		{store.SpanFilter{Kind: store.SpanKindTool}, "meta"},
		{store.SpanFilter{Name: "fetch_url"}, "meta"},
		{store.SpanFilter{Failed: true}, "meta"},
		{store.SpanFilter{Kind: store.SpanKindTool, Name: "fetch_url", Failed: true, Text: "cotação"}, "meta"},
		{store.SpanFilter{Kind: store.SpanKindGeneric, Text: "cotação"}, "pt"},
		{store.SpanFilter{Kind: store.SpanKindTool, Text: "petrobras"}, ""},
		{store.SpanFilter{Model: "no-such-model"}, ""},
	} {
		if got := filterIDs(t, s, "acme", c.f); got != c.want {
			t.Errorf("QueryTraces(%+v) = %q, want %q", c.f, got, c.want)
		}
	}
	if names, err := s.ListSpanNames(ctx, store.SpanKindTool); err != nil || len(names) != 1 || names[0] != "fetch_url" {
		t.Errorf("ListSpanNames(tool) = %v, %v; want [fetch_url]", names, err)
	}

	testMatchingSpans(t, s, pt, now)
}

func testMatchingSpans(t *testing.T, s store.Store, pt string, now time.Time) {
	ctx := context.Background()
	// A second, later span in trace-pt that matches too, and one that does not.
	later := newSpan("trace-pt", "pt-later", now.Add(-time.Minute))
	later.Output = &pt
	other := newSpan("trace-pt", "pt-other", now.Add(-2*time.Minute))
	for _, sp := range []store.Span{later, other} {
		if err := s.InsertSpan(ctx, sp); err != nil {
			t.Fatalf("InsertSpan: %v", err)
		}
	}
	// Each trace's spans together, oldest first; only its first hit carries a snippet.
	describe := func(f store.SpanFilter, traces ...string) string {
		t.Helper()
		hits, err := s.MatchingSpans(ctx, f, traces)
		if err != nil {
			t.Fatalf("MatchingSpans(%+v): %v", f, err)
		}
		var out []string
		for _, h := range hits {
			out = append(out, fmt.Sprintf("%s/%s %s %s %s snippet=%v", h.TraceID, h.SpanID, h.SpanName, h.Kind, h.Status, h.Snippet != ""))
		}
		return strings.Join(out, "; ")
	}
	if got, want := describe(store.SpanFilter{Text: "Petrobras"}, "trace-pt", "trace-call", "trace-elsewhere"),
		"trace-elsewhere/elsewhere elsewhere generic ok snippet=true; trace-pt/pt pt generic ok snippet=true; trace-pt/pt-later pt-later generic ok snippet=false"; got != want {
		t.Errorf("MatchingSpans(Petrobras) = %s, want %s", got, want)
	}
	if got, want := describe(store.SpanFilter{Kind: store.SpanKindTool}, "trace-meta", "trace-pt"), "trace-meta/meta fetch_url tool error snippet=false"; got != want {
		t.Errorf("MatchingSpans(kind=tool) = %s, want %s: no text, no snippet", got, want)
	}
	if got := describe(store.SpanFilter{}, "trace-pt"); got != "" {
		t.Errorf("MatchingSpans(no filter) = %s, want nothing: there is no match to explain", got)
	}
	hits, _ := s.MatchingSpans(ctx, store.SpanFilter{Text: "Petrobras"}, []string{"trace-pt"})
	if h := hits[0]; !strings.Contains(h.Snippet, store.HitStart+"Petrobras"+store.HitEnd) || len(h.Snippet) > 400 {
		t.Errorf("snippet must mark the match and stay short (%d bytes of a %d byte input): %q", len(h.Snippet), len(pt), h.Snippet)
	}
}

// A retried span is ignored (first write wins) and must not reach the
// index either; sweeping must take the index along.
func testSearchSpansFollowsWrites(t *testing.T, s store.Store) {
	ctx := context.Background()
	first, retry := "the original payload", "a different resend"
	old := time.Now().Add(-48 * time.Hour)
	seedSearch(t, s, "acme", "old", old, func(sp *store.Span) { sp.Input = &first })
	seedSearch(t, s, "acme", "old", old, func(sp *store.Span) { sp.Input = &retry })
	seedSearch(t, s, "acme", "new", time.Now(), func(sp *store.Span) { sp.Input = &first })

	if got := hitIDs(t, s, "", "resend"); got != "" {
		t.Errorf("an ignored retry was indexed: %q", got)
	}
	if got := hitIDs(t, s, "", "original"); got != "new,old" {
		t.Errorf("before the sweep: %q", got)
	}

	if _, _, err := s.SweepExpired(ctx, time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if got := hitIDs(t, s, "", "original"); got != "new" {
		t.Errorf("after the sweep: %q, want the swept span gone", got)
	}
}
