package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func doDetail(t *testing.T, h *Handlers, traceID string) *httptest.ResponseRecorder {
	t.Helper()
	return doDetailQuery(t, h, traceID, "")
}

func doDetailQuery(t *testing.T, h *Handlers, traceID, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/traces/" + traceID
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	r := httptest.NewRequest("GET", target, nil)
	r.SetPathValue("trace_id", traceID)
	w := httptest.NewRecorder()
	h.Detail(w, r)
	return w
}

// An orphan span is rendered at the root, end to end.
func TestDetailOrphanSpanRendersAtRoot(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())
	dangling := "does-not-exist"
	seedSpan(t, s, tr.ID, "orphan-span", &dangling, time.Now())

	w := doDetail(t, h, tr.ID)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "orphan-span") {
		t.Errorf("orphan span must still render (at the root), got: %s", w.Body.String())
	}
}

// The middle trace of three has a neighbour in both directions.
func TestDetailShowsPrevNextLinks(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	base := time.Now()
	oldest := seedTrace(t, s, "trace-oldest", base)
	middle := seedTrace(t, s, "trace-middle", base.Add(time.Second))
	newest := seedTrace(t, s, "trace-newest", base.Add(2*time.Second))

	w := doDetail(t, h, middle.ID)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "/traces/"+newest.ID) {
		t.Errorf("body missing prev link to %s, got: %s", newest.ID, body)
	}
	if !strings.Contains(body, "/traces/"+oldest.ID) {
		t.Errorf("body missing next link to %s, got: %s", oldest.ID, body)
	}
}

// The oldest of two traces has no older neighbour: "Next" still renders, as
// a read-only element (a button that disappears is a misclick trap), while
// "Previous" stays a link.
func TestDetailAtBoundaryShowsReadOnlyButton(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	base := time.Now()
	oldest := seedTrace(t, s, "trace-oldest", base)
	newest := seedTrace(t, s, "trace-newest", base.Add(time.Second))

	w := doDetail(t, h, oldest.ID)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, "Next →") {
		t.Errorf("oldest trace must still render a (read-only) Next button, got: %s", body)
	}
	if !strings.Contains(body, `aria-disabled="true"`) {
		t.Errorf("Next must be read-only (aria-disabled) at the oldest trace, got: %s", body)
	}
	if got, want := strings.Count(body, `href="/traces/`), 1; got != want {
		t.Errorf("expected exactly %d trace link (Previous only, Next is read-only), got %d: %s", want, got, body)
	}
	if !strings.Contains(body, "/traces/"+newest.ID) {
		t.Errorf("body missing Previous link to %s, got: %s", newest.ID, body)
	}
}

// A single trace: both Prev and Next are read-only, never absent.
func TestDetailBothBoundariesShowReadOnlyButtons(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())

	w := doDetail(t, h, tr.ID)
	body := w.Body.String()
	if !strings.Contains(body, "← Previous</span>") {
		t.Errorf("expected a read-only Previous button, got: %s", body)
	}
	if !strings.Contains(body, "Next →</span>") {
		t.Errorf("expected a read-only Next button, got: %s", body)
	}
	if got, want := strings.Count(body, `aria-disabled="true"`), 2; got != want {
		t.Errorf("expected both nav buttons read-only, got %d aria-disabled markers, want %d: %s", got, want, body)
	}
}

// With no ?span=, the panel shows the chronologically first root span.
func TestDetailDefaultsToFirstRootSpan(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())
	seedSpan(t, s, tr.ID, "span-first", nil, time.Now())
	seedSpan(t, s, tr.ID, "span-second", nil, time.Now().Add(time.Second))

	w := doDetail(t, h, tr.ID)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	if !strings.Contains(body, `?span=span-first`) {
		t.Errorf("expected the tree link for span-first to carry ?span=span-first, got: %s", body)
	}
}

// ?span= selects a span; one that is not of this trace falls back to the
// first root instead of erroring.
func TestDetailSpanQuerySelectsSpan(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())
	seedSpan(t, s, tr.ID, "span-first", nil, time.Now())
	seedSpan(t, s, tr.ID, "span-second", nil, time.Now().Add(time.Second))

	w := doDetailQuery(t, h, tr.ID, "span=span-second")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "span-second") {
		t.Errorf("expected span-second's own name to render in the detail panel, got: %s", w.Body.String())
	}

	w = doDetailQuery(t, h, tr.ID, "span=does-not-belong-to-this-trace")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200 (fallback, not error), body: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), `?span=span-first`) {
		t.Errorf("unresolved ?span= should fall back to the first root, got: %s", w.Body.String())
	}
}

// Structured messages end to end, in the shape of
// testdata/openllmetry-anthropic-tool-use-2's completion.
func TestDetailRendersToolCallArguments(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())

	output := `[{"role":"assistant","finish_reason":"tool_use","tool_calls":[{"id":"toolu_1","name":"convert_currency","arguments":"{\"amount\": 250, \"from_currency\": \"USD\", \"to_currency\": \"EUR\"}"}]}]`
	sp := store.Span{
		TraceID:   tr.ID,
		ID:        "span-tool-call",
		Kind:      store.SpanKindGeneration,
		Name:      "span-tool-call",
		StartedAt: time.Now(),
		EndedAt:   time.Now().Add(time.Second),
		Status:    store.StatusOK,
		Output:    &output,
		CreatedAt: time.Now(),
	}
	if err := s.InsertSpan(t.Context(), sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	w := doDetail(t, h, tr.ID)
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// Arguments render as a key/value tree: the assertion looks for its cells,
	// not JSON punctuation.
	for _, want := range []string{"convert_currency", "tool_use", "amount", "250", "from_currency", "USD"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %s", want, body)
		}
	}
	if strings.Contains(body, `amount&#34;: 250`) {
		t.Errorf("expected the tree rendering, not the old raw-JSON-blob rendering, got: %s", body)
	}
}

// The <title>, and the breadcrumb back to the list filtered to the trace's
// service.
func TestDetailTitleAndBreadcrumb(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedServiceTrace(t, s, "check out", "trace-1", time.Now())

	w := doDetail(t, h, tr.ID)
	body := w.Body.String()
	if !strings.Contains(body, "<title>spoor — trace-1</title>") {
		t.Errorf("expected detail page title, got: %s", body)
	}
	for _, want := range []string{`href="/?service=check&#43;out"`, `<dt>Service</dt><dd class="plain">check out</dd>`, `<span class="ellip" title="check out">check out</span></summary>`} {
		if !strings.Contains(body, want) {
			t.Errorf("a trace with a service: body missing %s", want)
		}
	}

	// A trace whose sender named no service says so, and leads back to every trace.
	body = doDetail(t, h, seedTrace(t, s, "trace-2", time.Now()).ID).Body.String()
	for _, want := range []string{`<a href="/">Traces</a>`, `<dt>Service</dt><dd class="plain">—</dd>`, `<span class="ellip" title="">All services</span></summary>`} {
		if !strings.Contains(body, want) {
			t.Errorf("a trace without a service: body missing %s", want)
		}
	}
}

// The selected span's metadata is collapsed by default too.
func TestDetailSpanMetadataCollapsedByDefault(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := store.Trace{
		ID: "trace-1", Name: "trace-1", Status: store.StatusOK,
		Metadata:  []byte(`{"trace_key":"trace_value"}`),
		StartedAt: time.Now(), EndedAt: time.Now().Add(time.Second), CreatedAt: time.Now(),
	}
	if err := s.MergeTrace(t.Context(), tr, true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}
	sp := store.Span{
		TraceID: tr.ID, ID: "span-1", Kind: store.SpanKindGeneric,
		Name: "span-1", Status: store.StatusOK, Metadata: []byte(`{"foo":"bar"}`),
		StartedAt: time.Now(), EndedAt: time.Now().Add(time.Second), CreatedAt: time.Now(),
	}
	if err := s.InsertSpan(t.Context(), sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	w := doDetail(t, h, tr.ID)
	body := w.Body.String()
	if strings.Count(body, `<details class="disc">`) != 2 {
		t.Errorf("expected both trace and span metadata as closed <details>, got: %s", body)
	}
	if strings.Contains(body, "<details open") {
		t.Errorf("expected no metadata group open by default, got: %s", body)
	}
}

// The span panel shows the span's absolute UTC start time, not just its
// duration.
func TestDetailSpanTimestampAbsolute(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())
	startedAt := time.Date(2026, 3, 4, 12, 0, 0, 0, time.UTC)
	seedSpan(t, s, tr.ID, "span-1", nil, startedAt)

	w := doDetail(t, h, tr.ID)
	body := w.Body.String()
	if !strings.Contains(body, "2026-03-04 12:00:00 UTC") {
		t.Errorf("expected absolute span start timestamp, got: %s", body)
	}
}

// Previous/Next carry the neighbouring trace's name, truncated, with the
// full name as a tooltip.
func TestDetailPrevNextShowTraceName(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	base := time.Now()
	oldest := seedTrace(t, s, "trace-oldest-with-a-long-name", base)
	newest := seedTrace(t, s, "trace-newest-with-a-long-name", base.Add(time.Second))

	w := doDetail(t, h, oldest.ID)
	body := w.Body.String()
	if !strings.Contains(body, `title="`+newest.Name+`"`) {
		t.Errorf("expected Previous link title with the full neighbor name, got: %s", body)
	}
}

// A generation's tree row shows its cost next to the duration, and an ok
// parent of a failed descendant shows the "error below" indicator.
func TestDetailTreeRowShowsGenerationCostAndErrorIndicator(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())

	cost := 0.0042
	parent := store.Span{
		TraceID: tr.ID, ID: "parent", Kind: store.SpanKindGeneration,
		Name: "parent", Status: store.StatusOK, CostUSD: &cost,
		StartedAt: time.Now(), EndedAt: time.Now().Add(time.Second), CreatedAt: time.Now(),
	}
	if err := s.InsertSpan(t.Context(), parent); err != nil {
		t.Fatalf("InsertSpan(parent): %v", err)
	}
	errChild := store.Span{
		TraceID: tr.ID, ID: "err-child", ParentSpanID: strPtr("parent"),
		Kind: store.SpanKindGeneric, Name: "err-child", Status: store.StatusError,
		StartedAt: time.Now().Add(time.Second), EndedAt: time.Now().Add(2 * time.Second), CreatedAt: time.Now(),
	}
	if err := s.InsertSpan(t.Context(), errChild); err != nil {
		t.Fatalf("InsertSpan(err-child): %v", err)
	}

	w := doDetail(t, h, tr.ID)
	body := w.Body.String()
	if !strings.Contains(body, "$0.0042") {
		t.Errorf("expected the generation span's cost in its tree row, got: %s", body)
	}
	if !strings.Contains(body, `title="error in a descendant"`) {
		t.Errorf("expected the amber error-below indicator on the ok-status parent, got: %s", body)
	}
}

// A message longer than the excerpt limit renders inside a <details>, not
// as one unbounded block.
func TestDetailLongContentIsClamped(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())

	longContent := strings.Repeat("a", excerptLimit*2)
	input := `[{"role":"system","content":"` + longContent + `"}]`
	sp := store.Span{
		TraceID: tr.ID, ID: "span-1", Kind: store.SpanKindGeneration,
		Name: "span-1", Status: store.StatusOK, Input: &input,
		StartedAt: time.Now(), EndedAt: time.Now().Add(time.Second), CreatedAt: time.Now(),
	}
	if err := s.InsertSpan(t.Context(), sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	w := doDetail(t, h, tr.ID)
	body := w.Body.String()
	if !strings.Contains(body, "Show 600 more characters") {
		t.Errorf("expected the tail of the long content behind a counted disclosure, got: %s", body)
	}
}

// The header sums cost, tokens, span count and models over the trace's spans.
func TestDetailHeaderShowsTotals(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())

	model := "openai/gpt-4o-mini"
	in1, out1 := int64(100), int64(50)
	cost1 := 0.01
	sp1 := store.Span{
		TraceID: tr.ID, ID: "span-1", Kind: store.SpanKindGeneration,
		Name: "span-1", Status: store.StatusOK, Model: &model,
		InputTokens: &in1, OutputTokens: &out1, CostUSD: &cost1,
		StartedAt: time.Now(), EndedAt: time.Now().Add(time.Second), CreatedAt: time.Now(),
	}
	if err := s.InsertSpan(t.Context(), sp1); err != nil {
		t.Fatalf("InsertSpan(sp1): %v", err)
	}
	in2, out2 := int64(20), int64(10)
	sp2 := store.Span{
		TraceID: tr.ID, ID: "span-2", Kind: store.SpanKindGeneric,
		Name: "span-2", Status: store.StatusOK, InputTokens: &in2, OutputTokens: &out2,
		StartedAt: time.Now().Add(time.Second), EndedAt: time.Now().Add(2 * time.Second), CreatedAt: time.Now(),
	}
	if err := s.InsertSpan(t.Context(), sp2); err != nil {
		t.Fatalf("InsertSpan(sp2): %v", err)
	}

	w := doDetail(t, h, tr.ID)
	body := w.Body.String()
	for _, want := range []string{"$0.0100", "120 <small>in</small> · 60 <small>out</small>", "<dt>Spans</dt><dd>2</dd>", model} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %s", want, body)
		}
	}
}

// The arrow keys' targets at the three positions of a 3-span order: both
// neighbours in the middle, no prev at the first, no next at the last.
func TestAdjacentSpanIDs(t *testing.T) {
	now := time.Now()
	root := store.Span{ID: "root", Name: "root", StartedAt: now}
	child := store.Span{ID: "child", Name: "child", ParentSpanID: strPtr("root"), StartedAt: now.Add(time.Second)}
	grandchild := store.Span{ID: "grandchild", Name: "grandchild", ParentSpanID: strPtr("child"), StartedAt: now.Add(2 * time.Second)}
	roots := BuildSpanTree([]store.Span{root, child, grandchild})

	cases := []struct {
		selected           string
		wantPrev, wantNext string
	}{
		{"root", "", "child"},
		{"child", "root", "grandchild"},
		{"grandchild", "child", ""},
	}
	for _, c := range cases {
		prev, next := adjacentSpanIDs(roots, c.selected)
		if prev != c.wantPrev || next != c.wantNext {
			t.Errorf("adjacentSpanIDs(%q) = (%q, %q), want (%q, %q)", c.selected, prev, next, c.wantPrev, c.wantNext)
		}
	}
}

func TestAdjacentSpanIDsUnknownSelection(t *testing.T) {
	prev, next := adjacentSpanIDs(nil, "does-not-exist")
	if prev != "" || next != "" {
		t.Errorf("adjacentSpanIDs(nil, ...) = (%q, %q), want (\"\", \"\")", prev, next)
	}
}

// The arrow-key shortcuts end to end: hidden anchors carry the next/prev
// span URLs, guarded against firing while a form field has focus, and the
// Previous/Next trace links have the same keyup triggers.
func TestDetailKeyboardNavTriggersRendered(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	base := time.Now()
	seedTrace(t, s, "trace-oldest", base)
	middle := seedTrace(t, s, "trace-middle", base.Add(time.Second))
	seedTrace(t, s, "trace-newest", base.Add(2*time.Second))
	seedSpan(t, s, middle.ID, "span-first", nil, base)
	seedSpan(t, s, middle.ID, "span-second", nil, base.Add(time.Second))

	w := doDetailQuery(t, h, middle.ID, "span=span-first")
	body := w.Body.String()

	// One guard serves every shortcut: no modifier, and no control that has its own use for keys.
	const guard = "!ctrlKey&&!metaKey&&!altKey&&!target.isContentEditable&&!target.closest('input,select,textarea,button,summary')"
	if !strings.Contains(body, "keyup[key=='ArrowDown' && "+guard+"] from:body") {
		t.Errorf("expected an ArrowDown trigger to span-second, got: %s", body)
	}
	if !strings.Contains(body, `href="/traces/`+middle.ID+`?span=span-second"`) {
		t.Errorf("expected the ArrowDown hidden link to target span-second, got: %s", body)
	}
	if strings.Contains(body, "ArrowUp") {
		t.Errorf("span-first has no prev span, expected no ArrowUp trigger, got: %s", body)
	}
	if !strings.Contains(body, "keyup[key=='ArrowLeft' && "+guard+"] from:body") {
		t.Errorf("expected the Previous link to also carry an ArrowLeft keyup trigger, got: %s", body)
	}
	if !strings.Contains(body, "keyup[key=='ArrowRight' && "+guard+"] from:body") {
		t.Errorf("expected the Next link to also carry an ArrowRight keyup trigger, got: %s", body)
	}
}

func TestDetailNotFoundForUnknownTrace(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)

	w := doDetail(t, h, "does-not-exist")
	if w.Code != 404 {
		t.Errorf("status = %d, want 404", w.Code)
	}
	for _, want := range []string{"Trace not found", "This database has no trace with this id.", "another database", `href="/sessions"`, ">Blind spots</a>"} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("the not-found page should contain %q", want)
		}
	}

	h.RetentionDays = 30
	if body := doDetail(t, h, "does-not-exist").Body.String(); !strings.Contains(body, "more than 30 days ago are deleted") {
		t.Errorf("with a retention set, the page should say traces older than it are deleted, got: %s", body)
	}
}

// An instrumentor that repeats a child's usage on its parent must not
// double the header: it counts what the tree's Σ counts.
func TestTraceTotalsMatchTreeRollup(t *testing.T) {
	now := time.Now().UTC()
	usage := func(id string, parent *string, in, out int64, cost float64) store.Span {
		return store.Span{TraceID: "t", ID: id, ParentSpanID: parent, Kind: store.SpanKindGeneration,
			StartedAt: now, EndedAt: now.Add(time.Second), InputTokens: &in, OutputTokens: &out, CostUSD: &cost}
	}
	agent, orphanParent := "agent", "never-arrived"
	spans := []store.Span{
		usage("agent", nil, 100, 10, 0.5), // repeats its child's usage
		usage("llm", &agent, 100, 10, 0.5),
		usage("orphan", &orphanParent, 7, 3, 0.25),
	}
	roots := BuildSpanTree(spans)
	got := computeTraceTotals(spans, roots)
	if got.InputTokens != 107 || store.Deref(got.OutputTokens) != 13 || got.CostUSD == nil || *got.CostUSD != 0.75 || got.SpanCount != 3 {
		t.Errorf("totals = %d in, %d out, cost %v, %d spans; want 107, 13, 0.75, 3", got.InputTokens, store.Deref(got.OutputTokens), got.CostUSD, got.SpanCount)
	}
	var tree TokenMix
	for _, r := range roots {
		tree = tree.Add(r.Tokens)
	}
	if got.Tokens != tree {
		t.Errorf("header mix = %+v, want the sum over every root %+v", got.Tokens, tree)
	}
	if tree.Total() != got.InputTokens+store.Deref(got.OutputTokens) {
		t.Errorf("header counts %d tokens, tree roots %d", got.InputTokens+store.Deref(got.OutputTokens), tree.Total())
	}
	// No span reported an output count: unknown, not zero.
	noOutput := []store.Span{{ID: "g", Kind: store.SpanKindGeneration, InputTokens: i64Ptr(5)}}
	if got := computeTraceTotals(noOutput, BuildSpanTree(noOutput)); got.OutputTokens != nil || got.InputTokens != 5 {
		t.Errorf("totals without an output count = %d in, %v out; want 5 and nil", got.InputTokens, got.OutputTokens)
	}
	if none := computeTraceTotals(nil, nil); none.CostUSD != nil {
		t.Errorf("empty trace cost = %v, want nil", *none.CostUSD)
	}
}
