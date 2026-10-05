package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// What the person asked, as the list reads it, and whole: the newest user
// message of the earliest model call, else the earliest agent span's prompt.
func TestAskedOf(t *testing.T) {
	now := time.Now()
	long := strings.Repeat("palavra ", 60) // past the list's 180 characters
	call := gen("t", "g1", nil, now, prompt(msgSystem, msgUser1, msgAsst1, `{"role":"user","content":"`+long+`"}`))
	agent := store.Span{TraceID: "t", ID: "a", Kind: store.SpanKindAgent, StartedAt: now, Input: ptr("fix the failing test")}
	for name, c := range map[string]struct {
		spans []store.Span
		want  string
	}{
		"a model call":       {[]store.Span{call}, strings.TrimSpace(long)},
		"only an agent span": {[]store.Span{agent}, "fix the failing test"},
		"the call wins":      {[]store.Span{agent, call}, strings.TrimSpace(long)},
		"a call without a body falls back to the agent": {[]store.Span{gen("t", "g0", nil, now, nil), agent}, "fix the failing test"},
		"nothing recorded": {[]store.Span{gen("t", "g0", nil, now, nil)}, ""},
	} {
		if got := askedOf(c.spans, ""); got != c.want {
			t.Errorf("%s: askedOf = %q, want %q", name, got, c.want)
		}
	}
}

// The trace page shows the question under the name, whole in the title,
// omits it when it is the name or absent, and reads no more than the spans
// it already holds.
func TestDetailShowsWhatWasAskedFromTheLoadedSpans(t *testing.T) {
	s := newTestStore(t)
	counting := &countingStore{Store: s}
	h := newTestHandlers(t, s)
	h.Store = counting
	now := time.Now()
	tr := seedTrace(t, s, "trace-1", now)
	insertSpans(t, s, gen(tr.ID, "g1", nil, now, prompt(msgSystem, msgUser2)))

	body := doDetail(t, h, tr.ID).Body.String()
	if !strings.Contains(body, `<p class="asked" title="second question">second question</p>`) {
		t.Errorf("the question should sit under the trace name, got: %s", body)
	}
	if counting.querySpans != 0 || counting.getTrace != 1 {
		t.Errorf("reads: QuerySpans %d, GetTraceByID %d; want 0 and 1 (the spans already loaded)", counting.querySpans, counting.getTrace)
	}

	named := store.Trace{ID: "trace-2", Name: "second question", StartedAt: now, EndedAt: now.Add(time.Second), Status: store.StatusOK, CreatedAt: now} // named after the question
	if err := s.MergeTrace(t.Context(), named, true); err != nil {
		t.Fatal(err)
	}
	insertSpans(t, s, gen("trace-2", "g2", nil, now, prompt(msgSystem, msgUser2)))
	seedTrace(t, s, "trace-3", now.Add(2*time.Second))
	insertSpans(t, s, gen("trace-3", "g3", nil, now, nil))
	for _, id := range []string{"trace-2", "trace-3"} {
		if body := doDetail(t, h, id).Body.String(); strings.Contains(body, `class="asked"`) {
			t.Errorf("trace %q: no line when the question is the name or absent", id)
		}
	}
}

// countingStore counts the reads a page makes of spans.
type countingStore struct {
	store.Store
	querySpans, getTrace int
}

func (c *countingStore) QuerySpans(ctx context.Context, q store.SpanQuery) ([]store.Span, error) {
	c.querySpans++
	return c.Store.QuerySpans(ctx, q)
}

func (c *countingStore) GetTraceByID(ctx context.Context, id string) (store.Trace, []store.Span, error) {
	c.getTrace++
	return c.Store.GetTraceByID(ctx, id)
}

// The path to the selected span runs from a root through its ancestors, and
// its place counts spans in the tree's display order.
func TestSpanTrailAndPosition(t *testing.T) {
	now := time.Now()
	sp := func(id string, parent *string, at int) store.Span {
		return store.Span{TraceID: "t", ID: id, Name: "n-" + id, ParentSpanID: parent, StartedAt: now.Add(time.Duration(at) * time.Second), EndedAt: now.Add(time.Duration(at+1) * time.Second)}
	}
	root, agent, chain := "root", "agent", "chain"
	roots := BuildSpanTree([]store.Span{
		sp("root", nil, 0), sp("agent", &root, 1), sp("early-sibling", &agent, 2),
		sp("chain", &agent, 3), sp("llm", &chain, 4), sp("late", &root, 5),
	})
	trail, at, of := spanTrail(roots, "llm")
	var names []string
	for _, st := range trail {
		names = append(names, st.Name)
	}
	if got := strings.Join(names, " > "); got != "n-root > n-agent > n-chain > n-llm" || at != 5 || of != 6 {
		t.Errorf("trail %q, span %d of %d; want root > agent > chain > llm, 5 of 6", got, at, of)
	}
	if trail, at, of := spanTrail(roots, "nope"); trail != nil || at != 0 || of != 6 {
		t.Errorf("an unknown span has no trail and no place: %v, %d of %d", trail, at, of)
	}
}

// The span panel shows the path and the place, ancestors as links that keep
// the rest of the address.
func TestDetailSpanPanelShowsPathAndPlace(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now()
	tr := seedTrace(t, s, "trace-1", now)
	parent := "p"
	p := gen(tr.ID, "p", nil, now, nil)
	p.Name, p.Kind = "interaction", store.SpanKindAgent
	c := gen(tr.ID, "c", &parent, now.Add(time.Second), nil)
	c.Name = "llm_request"
	insertSpans(t, s, p, c)

	body := doDetailQuery(t, h, tr.ID, "span=c&q=x").Body.String()
	for _, want := range []string{
		`aria-label="Path to this span"`, `hx-get="/traces/trace-1?span=p&amp;q=x"`, `>interaction</a>`,
		`<span aria-current="true">llm_request</span>`, `Span 2 of 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("span panel missing %q", want)
		}
	}
}

// The Tree | Read switch sits under the header, before the pointers and the
// chart, and each side keeps the rest of the address.
func TestViewSwitchKeepsTheAddressAndComesFirst(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now()
	tr := seedTrace(t, s, "trace-1", now)
	insertSpans(t, s,
		gen(tr.ID, "g1", nil, now, prompt(msgSystem, msgUser1)),
		gen(tr.ID, "g2", nil, now.Add(time.Second), prompt(msgSystem, msgUser1, msgAsst1, msgUser2)))

	body := doDetailQuery(t, h, tr.ID, "span=g2&q=question&fold=0").Body.String()
	for _, link := range []string{"/traces/trace-1?fold=0&amp;q=question&amp;span=g2", "/traces/trace-1?fold=0&amp;q=question&amp;span=g2&amp;view=read"} {
		if !strings.Contains(body, `<a href="`+link+`"`) {
			t.Errorf("the view switch should link to %q", link)
		}
	}
	switchAt := strings.Index(body, `aria-label="View"`)
	if switchAt < 0 || switchAt > strings.Index(body, ">Worth a look<") && strings.Contains(body, ">Worth a look<") ||
		switchAt > strings.Index(body, `id="span-tree"`) || switchAt > strings.Index(body, "Context per step") {
		t.Errorf("the switch should come before the tree, the pointers and the chart")
	}
}

// The tree view fills the window (no page scroll for an arrow key to move),
// with the context per step open beside the tree, above the selected span;
// the pointers sit on the line of the view switch. The Read view scrolls as a
// document, with the same chart above the transcript.
func TestTraceLayoutPutsContextAboveTheSelectedSpan(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now()
	tr := seedTrace(t, s, "trace-1", now)
	insertSpans(t, s,
		gen(tr.ID, "g1", nil, now, prompt(msgSystem, msgUser1)),
		gen(tr.ID, "g2", nil, now.Add(time.Second), prompt(msgSystem, msgUser1, msgAsst1, msgUser2)))

	body := doDetail(t, h, tr.ID).Body.String()
	at := func(s string) int { return strings.Index(body, s) }
	if !strings.Contains(body, `<div id="trace-detail" class="fit">`) {
		t.Error("the tree view should fit the window")
	}
	order := []string{`aria-label="View"`, ">Worth a look<", `id="span-tree"`, `class="col"`, `ctxband panel" open`, `id="span-detail"`}
	for k := 1; k < len(order); k++ {
		if at(order[k-1]) >= at(order[k]) || at(order[k]) < 0 {
			t.Errorf("order: %q should come before %q (switch, pointers, tree, then the open context above the selected span)", order[k-1], order[k])
		}
	}

	read := doDetailQuery(t, h, tr.ID, "view=read").Body.String()
	if strings.Contains(read, `class="fit"`) || !strings.Contains(read, `ctxband panel" open`) || strings.Index(read, `ctxband panel" open`) > strings.Index(read, `turn-n`) {
		t.Error("the Read view scrolls with the page and starts with the context chart")
	}
}
