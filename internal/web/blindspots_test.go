package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/otlp"
	"github.com/jhisse/spoor/internal/store"
)

func blindSpotsBody(t *testing.T, h *Handlers) string {
	t.Helper()
	w := httptest.NewRecorder()
	h.BlindSpots(w, httptest.NewRequest("GET", "/blind-spots", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200, body: %s", w.Code, w.Body.String())
	}
	return w.Body.String()
}

// The page states the retention the server was started with and how to
// change it; it has nothing to submit.
func TestBlindSpotsShowsEffectiveRetention(t *testing.T) {
	s := newTestStore(t)

	body := blindSpotsBody(t, &Handlers{Store: s})
	for _, want := range []string{"<title>spoor — Blind spots</title>", "<b>kept forever</b>", "SPOOR_RETENTION_DAYS", "This process runs no ingest server"} {
		if !strings.Contains(body, want) {
			t.Errorf("no retention configured: page missing %q", want)
		}
	}
	if strings.Count(body, "<form") != 1 { // the header's theme switch is the one form
		t.Errorf("the page should submit nothing, got: %s", body)
	}

	body = blindSpotsBody(t, &Handlers{Store: s, RetentionDays: 30})
	if !strings.Contains(body, "<b>30 days</b>") || strings.Contains(body, "kept forever") {
		t.Errorf("retention of 30 days: got: %s", body)
	}
}

// Only a generation model that matches no model_prices row is listed, with
// its span count; every other count is on the page; and the ingest
// server's counters are shown when the process has one.
func TestBlindSpotsCountsWhatCannotBeRead(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)

	body := blindSpotsBody(t, h)
	if !strings.Contains(body, "Every model seen has a price") || !strings.Contains(body, "Estimated cache cost") {
		t.Errorf("empty store: got: %s", body)
	}

	now, missing := time.Now(), "gone"
	for id, model := range map[string]string{"s1": "acme/in-house-model", "s2": "claude-sonnet-5"} {
		sp := store.Span{
			TraceID: "t1", ID: id, Kind: store.SpanKindGeneration, Name: id, ParentSpanID: &missing,
			StartedAt: now, EndedAt: now, Status: store.StatusOK, Model: &model, CreatedAt: now,
		}
		if err := s.InsertSpan(t.Context(), sp); err != nil {
			t.Fatalf("InsertSpan: %v", err)
		}
	}
	ingest := &otlp.Handler{Store: s}
	ingest.Rejected.Add(3)
	h.Ingest = ingest

	body = blindSpotsBody(t, h)
	for _, want := range []string{
		`acme/in-house-model · 1</span>`,
		`<dt>Spans without their parent</dt><dd class="n">2</dd>`,
		`<dt>Model calls without a token count</dt><dd class="n">2</dd>`,
		`<dt>Model calls without a price</dt><dd class="n">1</dd>`,
		`<dt>Spans refused</dt><dd class="n">3</dd>`,
		`<dt>Log and metric requests dropped</dt><dd class="n">0</dd>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %s", want)
		}
	}
	if strings.Contains(body, "claude-sonnet-5 ·") {
		t.Errorf("a model with a price row must not be listed, got: %s", body)
	}
}
