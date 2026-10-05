package otlp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// postSignal carries a credential header: it must make no difference.
func postSignal(path, contentType string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(""))
	r.Header.Set("Content-Type", contentType)
	r.Header.Set("x-api-key", "spoor_deadbeef_example-key")
	w := httptest.NewRecorder()
	(&Handler{}).Discard(w, r)
	return w
}

// An SDK exporting logs and metrics next to traces must get the OTLP
// success shape back (empty protobuf body, or {} for JSON), stored nowhere.
func TestDiscardAcceptsLogsAndMetricsWithoutStoring(t *testing.T) {
	st := newHandlerTestStore(t)

	for _, path := range []string{"/v1/logs", "/v1/metrics"} {
		w := postSignal(path, "application/x-protobuf")
		if w.Code != http.StatusOK || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "application/x-protobuf" {
			t.Errorf("%s protobuf: status=%d body=%q content-type=%q, want 200, empty, application/x-protobuf",
				path, w.Code, w.Body.String(), w.Header().Get("Content-Type"))
		}
		w = postSignal(path, "application/json")
		if w.Code != http.StatusOK || w.Body.String() != "{}" {
			t.Errorf("%s json: status=%d body=%q, want 200 and {}", path, w.Code, w.Body.String())
		}
	}

	traces, _, err := st.QueryTraces(context.Background(), store.TraceQuery{Limit: 10})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(traces) != 0 {
		t.Errorf("discarded signals created %d traces, want 0", len(traces))
	}
}

func loadDemo(t *testing.T, now time.Time) (st store.Store, n int) {
	t.Helper()
	st = newHandlerTestStore(t)
	n, err := (&Handler{Store: st}).LoadDemo(context.Background(), now)
	if err != nil {
		t.Fatalf("LoadDemo: %v", err)
	}
	return st, n
}

func TestLoadDemoMakesEveryCaptureRecent(t *testing.T) {
	now := time.Now()
	st, n := loadDemo(t, now)
	ctx := context.Background()

	traces, _, err := st.QueryTraces(ctx, store.TraceQuery{Limit: 200})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if n != 21 || len(traces) != n {
		t.Fatalf("LoadDemo reported %d traces, store has %d, want 21 of each", n, len(traces))
	}
	if newest := traces[0].EndedAt; !newest.Equal(now.Truncate(time.Nanosecond)) {
		t.Errorf("newest trace ends at %v, want exactly now (%v)", newest, now)
	}
	for _, tr := range traces {
		if age := now.Sub(tr.StartedAt); age < 0 || age > 2*time.Hour {
			t.Errorf("trace %q started %v ago, want within the last 2h", tr.Name, age)
		}
		if tr.EndedAt.Before(tr.StartedAt) {
			t.Errorf("trace %q ends before it starts after the shift", tr.Name)
		}
	}
}

func TestLoadDemoGroupsMultiTurnCapturesIntoSessions(t *testing.T) {
	st, _ := loadDemo(t, time.Now())
	ctx := context.Background()

	sessions, _, err := st.QuerySessions(ctx, "", nil, 50)
	if err != nil {
		t.Fatalf("QuerySessions: %v", err)
	}
	got := map[string]int64{}
	for _, s := range sessions {
		got[s.ID] = s.TraceCount
	}
	for _, id := range []string{"demo-openllmetry-anthropic", "demo-openllmetry-openai-openrouter"} {
		if got[id] != 2 {
			t.Errorf("session %q has %d traces, want 2 (all sessions: %v)", id, got[id], got)
		}
	}
	// Plus the captures that carry their own session id: Claude Code (3
	// sessions), the OpenAI Agents SDK and Pydantic AI.
	if len(sessions) != 7 {
		t.Errorf("got %d sessions, want the 2 multi-turn pairs and 5 sent by the SDK: %v", len(sessions), got)
	}

	// Turn 1 must precede turn 2 inside a session.
	_, turns, err := st.GetSession(ctx, "demo-openllmetry-openai-openrouter")
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if !turns[0].StartedAt.Before(turns[1].StartedAt) {
		t.Errorf("turns out of order: %v then %v", turns[0].StartedAt, turns[1].StartedAt)
	}
}

// The curl the UI prints pipes GET /sample.pb into POST /v1/traces; pasting
// it twice must yield two new traces dated now, not one stale duplicate.
func TestSampleIsANewRecentTraceEveryTime(t *testing.T) {
	st := newHandlerTestStore(t)
	h := &Handler{Store: st}

	for range 2 {
		sample := httptest.NewRecorder()
		Sample(sample, httptest.NewRequest(http.MethodGet, "/sample.pb", nil))
		if w := postTraces(h, sample.Body.Bytes()); w.Code != http.StatusOK {
			t.Fatalf("ingesting the sample: status = %d, body = %s", w.Code, w.Body.String())
		}
	}

	traces, _, err := st.QueryTraces(context.Background(), store.TraceQuery{Limit: 10})
	if err != nil {
		t.Fatalf("QueryTraces: %v", err)
	}
	if len(traces) != 2 {
		t.Fatalf("got %d traces after ingesting the sample twice, want 2", len(traces))
	}
	for _, tr := range traces {
		if age := time.Since(tr.EndedAt); age < 0 || age > time.Minute {
			t.Errorf("sample trace ended %v ago, want just now", age)
		}
		if len(tr.Models) == 0 {
			t.Errorf("sample trace lost its generation span's model")
		}
	}
}
