package web

import (
	"fmt"
	"html"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func doList(t *testing.T, h *Handlers, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	target := "/"
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	r := httptest.NewRequest("GET", target, nil)
	r.URL.RawQuery = rawQuery
	w := httptest.NewRecorder()
	h.List(w, r)
	return w
}

// GET / renders the columns and the seeded traces.
func TestListRendersColumnsAndSeededTrace(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	seedServiceTrace(t, s, "acme", "trace-1", time.Now())
	seedTrace(t, s, "trace-2", time.Now())

	w := doList(t, h, "")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	// trace-2 named no service: its cell is a dash, never a made-up name.
	for _, want := range []string{"<th>Trace</th>", "<th>Service</th>", "Started", "Duration", "Cost", `aria-label="ok"`, "trace-1", `<span class="ellip" title="acme">acme</span>`, `<span class="ellip" title="">—</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %s", want, body)
		}
	}
}

func TestListLiveTrueRendersPollingTrigger(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)

	w := doList(t, h, "live=1")
	if !strings.Contains(w.Body.String(), `hx-trigger="every 5s"`) {
		t.Errorf("?live=1 should render the polling trigger, got: %s", w.Body.String())
	}
}

func TestListLiveAbsentByDefault(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)

	w := doList(t, h, "")
	if strings.Contains(w.Body.String(), `hx-trigger="every 5s"`) {
		t.Errorf("no ?live=1 should render no polling trigger, got: %s", w.Body.String())
	}
}

// ?service= narrows the list to one service, drops the Service column,
// keeps the filter through the form, the next page and Clear, and the
// header's menu offers every service seen.
func TestListServiceFilter(t *testing.T) {
	h := serviceListHandlers(t)

	all := doList(t, h, "").Body.String()
	for _, want := range []string{"trace-0", "trace-1", "<th>Service</th>", `<a href="/?service=api" title="api">api</a>`, `<a href="/?service=back&#43;end" title="back end">back end</a>`, `<a href="/" aria-current="true">All services</a>`} {
		if !strings.Contains(all, want) {
			t.Errorf("no filter: body missing %s", want)
		}
	}

	body := doList(t, h, "service=api&limit=2").Body.String()
	for _, want := range []string{"trace-3", "trace-2", `<span class="ellip" title="api">api</span></summary>`, `<a href="/?service=api" title="api" aria-current="true">api</a>`,
		`<input type="hidden" name="service" value="api">`, `hx-get="/?limit=2" title="Remove this filter">service api<`, `<a href="/sessions?service=api">Sessions</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("?service=api: body missing %s", want)
		}
	}
	for _, unwanted := range []string{"trace-1", "trace-0", "<th>Service</th>"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("?service=api, first page of two: body should not contain %s", unwanted)
		}
	}
}

// serviceListHandlers seeds trace-0..3, oldest first: three of service api
// and trace-1 of service "back end".
func serviceListHandlers(t *testing.T) *Handlers {
	t.Helper()
	s := newTestStore(t)
	now := time.Now()
	for i, service := range []string{"api", "back end", "api", "api"} {
		seedServiceTrace(t, s, service, fmt.Sprintf("trace-%d", i), now.Add(time.Duration(i)*time.Second))
	}
	return newTestHandlers(t, s)
}

// The service filter survives the next page and Clear, and a service
// nothing was stored under is a filter miss.
func TestListServiceFilterPagesAndClears(t *testing.T) {
	h := serviceListHandlers(t)
	next := extractHref(doList(t, h, "service=api&limit=2").Body.String(), "Next page")
	if !strings.Contains(next, "service=api") {
		t.Fatalf("the next page lost the service filter: %s", next)
	}
	u, _ := url.Parse(next)
	if page2 := doList(t, h, u.RawQuery).Body.String(); !strings.Contains(page2, "trace-0") || strings.Contains(page2, "trace-1") || strings.Contains(page2, "Next page") {
		t.Errorf("second page of service api should hold trace-0 alone, got: %s", page2)
	}

	// Nothing matches: each filter says what dropping it alone would list.
	body := doList(t, h, "service=api&status=error").Body.String()
	for _, want := range []string{"No trace matches these 2 filters together", `<b>3</b> traces without <a class="link" href="/?service=api">status error</a>`,
		`<b>0</b> traces without <a class="link" href="/?status=error">service api</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("service and status: body missing %s, got: %s", want, body)
		}
	}
	// A service nothing was stored under is a filter miss, not an empty instance.
	body = doList(t, h, "service=does-not-exist").Body.String()
	if strings.Contains(body, "trace-0") || !strings.Contains(body, "No trace matches this filter") || !strings.Contains(body, `<b>4</b> traces without <a class="link" href="/">service does-not-exist</a>`) {
		t.Errorf("unknown service: want the filtered empty state leading back to every trace, got: %s", body)
	}
}

// End to end: no trace repeats across two pages.
func TestListPaginationNoDuplicatesAcrossPages(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)

	now := time.Now()
	for i := range 5 {
		seedTrace(t, s, "trace-"+string(rune('a'+i)), now.Add(time.Duration(i)*time.Minute))
	}

	w1 := doList(t, h, "limit=3")
	body1 := w1.Body.String()

	nextURL := extractHref(body1, "Next page")
	if nextURL == "" {
		t.Fatalf("expected a next-page link in page 1, body: %s", body1)
	}
	parsed, err := url.Parse(nextURL)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", nextURL, err)
	}

	w2 := doList(t, h, parsed.RawQuery)
	body2 := w2.Body.String()

	// A trace's name appears twice on its page (the href and the link text),
	// so the check is that it is on exactly one of the two pages.
	for i := range 5 {
		name := "trace-" + string(rune('a'+i))
		in1, in2 := strings.Contains(body1, name), strings.Contains(body2, name)
		if in1 == in2 {
			t.Errorf("%s: present in page1=%v, page2=%v — want present in exactly one", name, in1, in2)
		}
	}
}

// No filter and no traces: the empty-instance message, not the
// filter-matched-nothing one.
func TestListEmptyStateUnfiltered(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)

	w := doList(t, h, "")
	body := w.Body.String()
	if !strings.Contains(body, "No traces yet") {
		t.Errorf("body should show the unfiltered empty-state message, got: %s", body)
	}
	if strings.Contains(body, `class="filters"`) {
		t.Error("with nothing stored there is nothing to filter: the form is not shown")
	}
	if strings.Contains(body, "Clear all") {
		t.Errorf("unfiltered empty state should not offer a clear-filters link, got: %s", body)
	}
}

// Every page sets its own <title>.
func TestListTitleTag(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)

	w := doList(t, h, "")
	if !strings.Contains(w.Body.String(), "<title>spoor — Traces</title>") {
		t.Errorf("expected list page title, got: %s", w.Body.String())
	}
}

// ?from=/?to= narrows the results, and the submitted values are shown again
// in the datetime-local inputs.
func TestListFromToFilterAndRedisplay(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	seedTrace(t, s, "trace-old", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	seedTrace(t, s, "trace-new", time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))

	w := doList(t, h, "from="+url.QueryEscape("2026-03-01T00:00"))
	body := w.Body.String()
	if strings.Contains(body, "trace-old") {
		t.Errorf("?from=2026-03-01 must exclude trace-old, got: %s", body)
	}
	if !strings.Contains(body, "trace-new") {
		t.Errorf("?from=2026-03-01 must include trace-new, got: %s", body)
	}
	if !strings.Contains(body, `value="2026-03-01T00:00"`) {
		t.Errorf("expected the from value to re-render in the input, got: %s", body)
	}
}

// The list shows the trace's generation models, its summed tokens, and its
// cost when at least one span has one.
func TestListShowsModelTokensAndCost(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())

	model := "anthropic/claude-haiku-4-5"
	inputTokens, outputTokens := int64(188), int64(28)
	cost := 0.0123
	sp := store.Span{
		TraceID: tr.ID, ID: "span-1", Kind: store.SpanKindGeneration,
		Name: "span-1", Status: store.StatusOK, Model: &model,
		InputTokens: &inputTokens, OutputTokens: &outputTokens, CostUSD: &cost,
		StartedAt: time.Now(), EndedAt: time.Now().Add(time.Second), CreatedAt: time.Now(),
	}
	if err := s.InsertSpan(t.Context(), sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	w := doList(t, h, "")
	body := w.Body.String()
	for _, want := range []string{model, `188 <small>in</small> · 28 <small>out</small>`, "$0.0123"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %s", want, body)
		}
	}
}

// A trace whose only generation has no cost renders "–", not "$0.0000".
func TestListNullCostRendersDashNotZero(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())

	model := "google/gemini-3.6-flash"
	sp := store.Span{
		TraceID: tr.ID, ID: "span-1", Kind: store.SpanKindGeneration,
		Name: "span-1", Status: store.StatusOK, Model: &model,
		StartedAt: time.Now(), EndedAt: time.Now().Add(time.Second), CreatedAt: time.Now(),
	}
	if err := s.InsertSpan(t.Context(), sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	w := doList(t, h, "")
	body := w.Body.String()
	if strings.Contains(body, "$0.0000") {
		t.Errorf("expected no misleading $0.0000 cost, got: %s", body)
	}
	if !strings.Contains(body, ">–<") {
		t.Errorf("expected a bare \"–\" cost cell, got: %s", body)
	}
}

func extractHref(body, linkText string) string {
	idx := strings.Index(body, linkText)
	if idx == -1 {
		return ""
	}
	tagStart := strings.LastIndex(body[:idx], "<a ")
	if tagStart == -1 {
		return ""
	}
	hrefIdx := strings.Index(body[tagStart:idx], `href="`)
	if hrefIdx == -1 {
		return ""
	}
	start := tagStart + hrefIdx + len(`href="`)
	end := strings.Index(body[start:], `"`)
	if end == -1 {
		return ""
	}
	// html/template escapes "&" inside attribute values; undo it as a browser would.
	return html.UnescapeString(body[start : start+end])
}

// An instance with no traces shows what to do next: the ingest endpoint on
// the host the browser is using, a curl that works as pasted, and `spoor demo`.
func TestListEmptyStateOnboarding(t *testing.T) {
	s := newTestStore(t)
	h := &Handlers{Store: s, IngestAddr: ":4318"}

	r := httptest.NewRequest("GET", "http://spoor.internal:8080/", nil)
	w := httptest.NewRecorder()
	h.List(w, r)
	body := w.Body.String()
	for _, want := range []string{
		"http://spoor.internal:4318/v1/traces",
		`curl -s http://spoor.internal:8080/sample.pb | curl -X POST http://spoor.internal:4318/v1/traces -H "Content-Type: application/x-protobuf" --data-binary @-`,
		"spoor demo",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("empty state should contain %q, got: %s", want, body)
		}
	}
	for _, unwanted := range []string{"Clear filters", "Authorization", "API key", "bootstrap"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("empty state should not mention %q, got: %s", unwanted, body)
		}
	}

	// A filter that matches nothing is a different situation: no onboarding.
	if body := doList(t, h, "status=error").Body.String(); strings.Contains(body, "spoor demo") {
		t.Errorf("a filtered empty result should not show onboarding, got: %s", body)
	}
}

// The model cell names the model that cost most and counts the others: a
// cheap background model that sorts first by name must not stand for the trace.
func TestListNamesTheModelThatCostMost(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now()
	tr := seedTrace(t, s, "trace-1", now)
	cheap, dear := gen(tr.ID, "g1", nil, now, nil), gen(tr.ID, "g2", nil, now.Add(time.Second), nil)
	cheap.Model, cheap.CostUSD = ptr("a-small"), ptr(0.001)
	dear.Model, dear.CostUSD = ptr("z-large"), ptr(0.25)
	insertSpans(t, s, cheap, dear)

	body := doList(t, h, "").Body.String()
	if !strings.Contains(body, `z-large <span class="muted">+1</span>`) {
		t.Errorf("the model cell should name z-large and count one more, got: %s", body)
	}
}

// A trace row says what the person asked under the trace's name: the last
// user message of the first model call, or a coding agent's prompt on its
// agent span. A row whose name is the question already adds nothing.
func TestListShowsWhatWasAsked(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now()
	tr := seedTrace(t, s, "trace-1", now)
	call := gen(tr.ID, "g1", nil, now, prompt(msgSystem, msgUser1, msgAsst1, msgUser2))
	agent := gen("trace-2", "a1", nil, now, ptr("fix the failing test"))
	agent.Kind = store.SpanKindAgent
	seedTrace(t, s, "trace-2", now.Add(time.Second))
	insertSpans(t, s, call, agent)

	body := doList(t, h, "").Body.String()
	for _, want := range []string{`class="ask" title="second question"`, `class="ask" title="fix the failing test"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the list should show %q under the trace name", want)
		}
	}
	if strings.Contains(body, `title="first question"`) {
		t.Errorf("a trace answers its newest user message, not the first")
	}
}
