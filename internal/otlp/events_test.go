package otlp

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/jhisse/spoor/internal/store"
)

// failedSpan is derived from the specification, not captured: what an SDK's
// recordException plus setStatus(ERROR, message) puts on the wire (an
// "exception" event with exception.type/.message/.stacktrace, and
// Status.message), followed by a plain event and a link.
func failedSpan(start time.Time) *tracev1.Span {
	span := newTestSpan(testTraceID, "0102030405060708", "",
		uint64(start.UnixNano()), uint64(start.Add(time.Second).UnixNano()), tracev1.Status_STATUS_CODE_ERROR)
	span.Status.Message = "TimeoutError: upstream timed out"
	span.Events = []*tracev1.Span_Event{
		{Name: "exception", TimeUnixNano: uint64(start.Add(250 * time.Millisecond).UnixNano()), Attributes: []*commonv1.KeyValue{
			stringAttr("exception.type", "TimeoutError"),
			stringAttr("exception.message", "upstream timed out"),
			stringAttr("exception.stacktrace", "Traceback (most recent call last):\n  File \"tool.py\", line 7"),
		}},
		{Name: "retry.scheduled", TimeUnixNano: uint64(start.Add(300 * time.Millisecond).UnixNano())},
	}
	span.Links = []*tracev1.Span_Link{{TraceId: mustHexDecode("ffffffffffffffffffffffffffffffff"), SpanId: mustHexDecode("aaaaaaaaaaaaaaaa")}}
	return span
}

func assertFailedSpanStored(t *testing.T, sp store.Span, start time.Time) {
	t.Helper()
	if sp.StatusMessage == nil || *sp.StatusMessage != "TimeoutError: upstream timed out" {
		t.Errorf("StatusMessage = %v, want the status text", sp.StatusMessage)
	}
	var events []store.SpanEvent
	if err := json.Unmarshal(sp.Events, &events); err != nil || len(events) != 2 {
		t.Fatalf("Events = %s (%v), want 2 events", sp.Events, err)
	}
	ex, ok := ExceptionOf(events[0])
	if !ok || ex.Type != "TimeoutError" || ex.Message != "upstream timed out" || ex.Stacktrace == "" {
		t.Errorf("first event as exception = %+v, %v", ex, ok)
	}
	if got := events[0].Time.Sub(start.Truncate(time.Nanosecond)); got != 250*time.Millisecond {
		t.Errorf("exception offset from span start = %v, want 250ms", got)
	}
	if _, ok := ExceptionOf(events[1]); ok || events[1].Name != "retry.scheduled" || events[1].Attributes != nil {
		t.Errorf("second event = %+v, want a plain event without attributes", events[1])
	}
	if string(sp.Links) != `[{"trace_id":"ffffffffffffffffffffffffffffffff","span_id":"aaaaaaaaaaaaaaaa"}]` {
		t.Errorf("Links = %s", sp.Links)
	}
}

// A failed span through the handler, read back from the store, in both
// encodings.
func TestHandlerStoresStatusMessageEventsAndLinks(t *testing.T) {
	start := time.Now().UTC()
	body := newRequestBody(t, failedSpan(start))

	t.Run("protobuf", func(t *testing.T) {
		st := newHandlerTestStore(t)
		h := &Handler{Store: st}
		for range 2 { // a retry must not change what the first write stored
			if w := postTraces(h, body); w.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
			}
		}
		_, spans, err := st.GetTraceByID(context.Background(), testTraceID)
		if err != nil || len(spans) != 1 {
			t.Fatalf("got %d spans, err %v", len(spans), err)
		}
		assertFailedSpanStored(t, spans[0], start)
	})

	// OTLP/JSON writes a link's traceId/spanId as hex too (decode.go).
	t.Run("json", func(t *testing.T) {
		jsonBody := []byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{
			"traceId":"` + testTraceID + `","spanId":"0102030405060708","name":"tool.Bash",
			"startTimeUnixNano":"1000000000","endTimeUnixNano":"2000000000",
			"status":{"code":"STATUS_CODE_ERROR","message":"exit 127"},
			"events":[{"name":"exception","timeUnixNano":"1500000000","attributes":[{"key":"exception.message","value":{"stringValue":"command not found"}}]}],
			"links":[{"traceId":"ffffffffffffffffffffffffffffffff","spanId":"aaaaaaaaaaaaaaaa"}]}]}]}]}`)
		var req collectortrace.ExportTraceServiceRequest
		if err := unmarshalOTLPJSON(jsonBody, &req); err != nil {
			t.Fatalf("unmarshalOTLPJSON: %v", err)
		}
		sp := translateSpan(req.ResourceSpans[0].ScopeSpans[0].Spans[0], time.Now())
		var events []store.SpanEvent
		_ = json.Unmarshal(sp.Events, &events)
		if len(events) != 1 || *sp.StatusMessage != "exit 127" {
			t.Fatalf("status message %v, events %s", sp.StatusMessage, sp.Events)
		}
		if ex, _ := ExceptionOf(events[0]); ex.Message != "command not found" || ex.Type != "" {
			t.Errorf("exception = %+v, want only the message", ex)
		}
		if string(sp.Links) != `[{"trace_id":"ffffffffffffffffffffffffffffffff","span_id":"aaaaaaaaaaaaaaaa"}]` {
			t.Errorf("Links = %s", sp.Links)
		}
	})
}

// A capture of a successful call carries no event, link or status message:
// the three columns stay NULL, never "[]" or "". The Claude Code captures do
// carry events, which must be stored.
func TestRealCapturesEventsLinksAndStatusMessage(t *testing.T) {
	files, err := filepath.Glob("../../testdata/*.pb")
	if err != nil || len(files) == 0 {
		t.Fatalf("no captures found: %v", err)
	}
	// The failed calls are in TestErrorCapturesKeepThePrompt.
	files = slices.DeleteFunc(files, func(f string) bool { return strings.Contains(f, "-error") })
	sawToolOutput := false
	for _, file := range files {
		claudeCode := strings.HasPrefix(filepath.Base(file), "claude-code")
		data, err := os.ReadFile(file) // #nosec G304 -- test-only glob of testdata/
		if err != nil {
			t.Fatal(err)
		}
		var req collectortrace.ExportTraceServiceRequest
		if err := proto.Unmarshal(data, &req); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		for _, rs := range req.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				for _, span := range ss.Spans {
					s := translateSpan(span, time.Now())
					if claudeCode {
						sawToolOutput = sawToolOutput || strings.Contains(string(s.Events), "tool.output")
						continue
					}
					if s.Events != nil || s.Links != nil || s.StatusMessage != nil {
						t.Errorf("%s span %s: events %s, links %s, status message %v; want all nil", file, s.Name, s.Events, s.Links, s.StatusMessage)
					}
				}
			}
		}
	}
	if !sawToolOutput {
		t.Errorf("the Claude Code tool capture must store its tool.output event")
	}
}
