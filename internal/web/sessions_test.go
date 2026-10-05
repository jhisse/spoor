package web

import (
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/jhisse/spoor/internal/store"
)

func doSessions(t *testing.T, h *Handlers, rawQuery string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", "/sessions?"+rawQuery, nil)
	w := httptest.NewRecorder()
	h.Sessions(w, r)
	return w
}

// seedSessionTurn inserts a trace in sessionID with one generation span
// whose Input/Output use the message-array shape internal/otlp produces.
func seedSessionTurn(t *testing.T, s store.Store, traceID, sessionID string, startedAt time.Time) {
	t.Helper()
	tr := store.Trace{
		ID: traceID, Name: traceID, SessionID: &sessionID,
		StartedAt: startedAt, EndedAt: startedAt.Add(time.Second), Status: store.StatusOK, CreatedAt: startedAt,
	}
	if err := s.MergeTrace(t.Context(), tr, true); err != nil {
		t.Fatalf("MergeTrace: %v", err)
	}
	input := `[{"role":"system","content":"be brief"},{"role":"user","content":"qual a capital?"}]`
	output := `[{"role":"assistant","content":"Brasília","finish_reason":"stop"}]`
	sp := store.Span{
		TraceID: traceID, ID: traceID + "-span", Kind: store.SpanKindGeneration, Name: "chat",
		StartedAt: startedAt, EndedAt: startedAt.Add(time.Second), Status: store.StatusOK, CreatedAt: startedAt,
		Input: &input, Output: &output,
	}
	if err := s.InsertSpan(t.Context(), sp); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}
}

func TestSessionsListsSessions(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	seedSessionTurn(t, s, "trace-1", "conv/1", time.Now().UTC())
	other := "conv-of-billing"
	billed := seedServiceTrace(t, s, "billing", "trace-2", time.Now().UTC())
	billed.SessionID = &other
	if err := s.MergeTrace(t.Context(), billed, true); err != nil {
		t.Fatal(err)
	}

	w := doSessions(t, h, "")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	wantHref := "/sessions?session=" + url.QueryEscape("conv/1")
	if !strings.Contains(body, wantHref) || !strings.Contains(body, other) {
		t.Errorf("body missing escaped session link %q or the other session, got: %s", wantHref, body)
	}

	// ?service= keeps the sessions that service took part in, and the tabs keep the service.
	body = doSessions(t, h, "service=billing").Body.String()
	if !strings.Contains(body, other) || strings.Contains(body, wantHref) || !strings.Contains(body, `<a href="/?service=billing">Traces</a>`) {
		t.Errorf("?service=billing: want only its session and tabs that keep it, got: %s", body)
	}
	if body := doSessions(t, h, "service=nobody").Body.String(); !strings.Contains(body, "No sessions from this service") {
		t.Errorf("a service without sessions should say so, got: %s", body)
	}
}

func TestSessionsReplayShowsMessageTextNotRawJSON(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	seedSessionTurn(t, s, "trace-1", "conv-1", time.Now().UTC())

	w := doSessions(t, h, "session=conv-1")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()
	for _, want := range []string{"qual a capital?", "Brasília", `href="/traces/trace-1"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q, got: %s", want, body)
		}
	}
	for _, unwanted := range []string{"be brief", "&#34;role&#34;"} {
		if strings.Contains(body, unwanted) {
			t.Errorf("body contains %q — preview should be the latest message's text only", unwanted)
		}
	}
}

func TestSessionsNotFound(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)

	if w := doSessions(t, h, "session=nope"); w.Code != 404 {
		t.Errorf("unknown session: status = %d, want 404", w.Code)
	}
}

func TestShortTextTruncatesOnRuneBoundary(t *testing.T) {
	got := shortText(strings.Repeat("ç", 200))
	if !utf8.ValidString(got) {
		t.Errorf("shortText produced invalid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n != 181 {
		t.Errorf("rune count = %d, want 180 + ellipsis", n)
	}
}

func TestPreviewTextFallsBackToRawString(t *testing.T) {
	raw := "plain   prompt\ntext"
	if got := previewText(&raw); got != "plain prompt text" {
		t.Errorf("previewText = %q, want whitespace-collapsed raw text", got)
	}
}

// noTraceReads fails the test if the handler reads a trace per turn.
type noTraceReads struct {
	store.Store
	t *testing.T
}

func (s noTraceReads) GetTraceByID(context.Context, string) (store.Trace, []store.Span, error) {
	s.t.Error("GetTraceByID called: the replay must not read one trace per turn")
	return store.Trace{}, nil, nil
}

func TestSessionsReplayPreviewsEveryTurnWithoutPerTurnReads(t *testing.T) {
	s := newTestStore(t)
	now := time.Now().UTC()
	seedSessionTurn(t, s, "trace-1", "conv", now)
	seedSessionTurn(t, s, "trace-2", "conv", now.Add(time.Minute))
	later := "ignored"
	if err := s.InsertSpan(t.Context(), store.Span{
		TraceID: "trace-2", ID: "second-generation", Kind: store.SpanKindGeneration,
		StartedAt: now.Add(2 * time.Minute), EndedAt: now.Add(3 * time.Minute), Status: store.StatusOK, CreatedAt: now, Input: &later, Output: &later,
	}); err != nil {
		t.Fatalf("InsertSpan: %v", err)
	}

	w := doSessions(t, &Handlers{Store: noTraceReads{s, t}}, "session=conv")
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	if n := strings.Count(body, "qual a capital?"); n != 2 {
		t.Errorf("prompt preview appears %d times, want once per turn (2)", n)
	}
	if strings.Contains(body, "ignored") {
		t.Error("a turn previews a later generation span, want its first")
	}
}
