package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/otlp"
	"github.com/jhisse/spoor/internal/store"
	"github.com/jhisse/spoor/internal/store/sqlite"
)

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spoor.db")
	if err := sqlite.Migrate(path); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	s, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newTestHandlers(t *testing.T, s store.Store) *Handlers {
	t.Helper()
	return &Handlers{Store: s}
}

func seedTrace(t *testing.T, s store.Store, id string, startedAt time.Time) store.Trace {
	t.Helper()
	tr := store.Trace{
		ID:        id,
		Name:      id,
		StartedAt: startedAt,
		EndedAt:   startedAt.Add(time.Second),
		Status:    store.StatusOK,
		CreatedAt: startedAt,
	}
	if err := s.MergeTrace(t.Context(), tr, true); err != nil {
		t.Fatalf("MergeTrace(%s): %v", id, err)
	}
	return tr
}

// seedServiceTrace is seedTrace for a trace sent by the named service.
func seedServiceTrace(t *testing.T, s store.Store, service, id string, startedAt time.Time) store.Trace {
	t.Helper()
	tr := seedTrace(t, s, id, startedAt)
	tr.Service = &service
	if err := s.MergeTrace(t.Context(), tr, true); err != nil {
		t.Fatalf("MergeTrace(%s): %v", id, err)
	}
	return tr
}

func seedSpan(t *testing.T, s store.Store, traceID, id string, parentID *string, startedAt time.Time) store.Span {
	t.Helper()
	sp := store.Span{
		TraceID:      traceID,
		ID:           id,
		ParentSpanID: parentID,
		Kind:         store.SpanKindGeneric,
		Name:         id,
		StartedAt:    startedAt,
		EndedAt:      startedAt.Add(time.Second),
		Status:       store.StatusOK,
		CreatedAt:    startedAt,
	}
	if err := s.InsertSpan(t.Context(), sp); err != nil {
		t.Fatalf("InsertSpan(%s): %v", id, err)
	}
	return sp
}

func ptr[T any](v T) *T { return &v }

// gen builds a generation span; tests fill in tokens/cost on the result.
func gen(traceID, id string, parent *string, at time.Time, input *string) store.Span {
	return store.Span{
		TraceID: traceID, ID: id, ParentSpanID: parent, Kind: store.SpanKindGeneration, Name: id,
		StartedAt: at, EndedAt: at.Add(time.Second), Status: store.StatusOK, Input: input, CreatedAt: at,
	}
}

func insertSpans(t *testing.T, s store.Store, spans ...store.Span) {
	t.Helper()
	for _, sp := range spans {
		if err := s.InsertSpan(t.Context(), sp); err != nil {
			t.Fatalf("InsertSpan(%s): %v", sp.ID, err)
		}
	}
}

// ingestCapture pushes a captured OTLP payload through the ingestion
// handler, so the UI is tested against the stored shape it really gets.
func ingestCapture(t *testing.T, s store.Store, file string) {
	t.Helper()
	body, err := os.ReadFile("../../testdata/" + file) // #nosec G304 -- test-only, file comes from call sites in this file
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/x-protobuf")
	w := httptest.NewRecorder()
	(&otlp.Handler{Store: s}).ServeHTTP(w, r)
	if w.Code != 200 {
		t.Fatalf("ingest %s: %d %s", file, w.Code, w.Body.String())
	}
}

func onlyTrace(t *testing.T, s store.Store) (store.Trace, []store.Span) {
	t.Helper()
	traces, _, err := s.QueryTraces(t.Context(), store.TraceQuery{Limit: 10})
	if err != nil || len(traces) != 1 {
		t.Fatalf("QueryTraces: %v, %d traces", err, len(traces))
	}
	tr, spans, err := s.GetTraceByID(t.Context(), traces[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	return tr, spans
}
