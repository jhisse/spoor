package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// failedToolSpan is a tool span as an OpenTelemetry SDK reports a failure:
// status text, one exception event with a stack trace, one plain event.
func failedToolSpan(start time.Time) store.Span {
	return store.Span{
		ID: "tool", Kind: store.SpanKindTool, Name: "tool.Bash", Status: store.StatusError,
		StartedAt: start, EndedAt: start.Add(2 * time.Second), CreatedAt: start,
		StatusMessage: strPtr("exit 127:\n  jq: command not found"),
		Events: json.RawMessage(`[
			{"name":"exception","time":"` + start.Add(1500*time.Millisecond).Format(time.RFC3339Nano) + `","attributes":{"exception.type":"ExecError","exception.message":"jq <missing>","exception.stacktrace":"at run (tool.ts:7)"}},
			{"name":"retry.scheduled","time":"` + start.Add(1600*time.Millisecond).Format(time.RFC3339Nano) + `","attributes":{"attempt":2}},
			{"name":"no.time","time":"1970-01-01T00:00:00Z"}]`),
		Links: json.RawMessage(`[{"trace_id":"ff","span_id":"aa"}]`),
	}
}

func TestSpanEventsSplitsExceptionsFromTheTimeline(t *testing.T) {
	sp := failedToolSpan(time.Now().UTC())
	v := spanEvents(&sp)
	if len(v.Exceptions) != 1 || v.Exceptions[0].Exception.Type != "ExecError" || v.Exceptions[0].Offset != "+"+fmtDuration(1500*time.Millisecond) {
		t.Errorf("Exceptions = %+v", v.Exceptions)
	}
	if len(v.Events) != 2 || v.Events[0].Name != "retry.scheduled" || v.Events[0].Offset == "" {
		t.Errorf("Events = %+v", v.Events)
	}
	if v.Events[1].Offset != "" {
		t.Errorf("an event outside the span's time range must show no offset, got %q", v.Events[1].Offset)
	}
	if v.Links != 1 {
		t.Errorf("Links = %d, want 1", v.Links)
	}
}

func TestErrorReason(t *testing.T) {
	sp := failedToolSpan(time.Now().UTC())
	if got := errorReason(sp); got != "exit 127: jq: command not found" {
		t.Errorf("status text on one line = %q", got)
	}
	sp.StatusMessage = nil
	if got := errorReason(sp); got != "ExecError: jq <missing>" {
		t.Errorf("without a status text the first exception is the reason, got %q", got)
	}
	sp.Events = json.RawMessage(`[{"name":"exception","time":"2026-01-01T00:00:00Z","attributes":{"exception.type":"Killed"}}]`)
	if got := errorReason(sp); got != "Killed" {
		t.Errorf("exception with a type only = %q", got)
	}
	sp.Events = nil
	if got := errorReason(sp); got != "" {
		t.Errorf("a failure with no reason sent must stay without one, got %q", got)
	}
	ok := failedToolSpan(time.Now().UTC())
	ok.Status = store.StatusOK
	if got := errorReason(ok); got != "" {
		t.Errorf("a span that did not fail has no error reason, got %q", got)
	}
}

// The reason must be readable without opening the span: on its tree row, in
// "worth a look", and in full in the panel (stack trace collapsed).
func TestDetailShowsWhyASpanFailed(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	start := time.Now().UTC()
	tr := seedTrace(t, s, "trace-1", start)
	root := store.Span{TraceID: tr.ID, ID: "root", Kind: store.SpanKindAgent, Name: "agent",
		Status: store.StatusUnset, StartedAt: start, EndedAt: start.Add(3 * time.Second), CreatedAt: start}
	tool := failedToolSpan(start.Add(time.Second))
	tool.TraceID, tool.ParentSpanID = tr.ID, strPtr("root")
	for _, sp := range []store.Span{root, tool} {
		if err := s.InsertSpan(t.Context(), sp); err != nil {
			t.Fatalf("InsertSpan: %v", err)
		}
	}

	body := doDetail(t, h, tr.ID).Body.String() // opens on the first failure
	for _, want := range []string{
		`title="exit 127: jq: command not found"`,                 // tree row
		`: exit 127: jq: command not found"><svg class="st look"`, // worth a look
		"</svg> Error</h3>", "exit 127:\n  jq: command not found", // panel: status text as sent
		"ExecError", "jq &lt;missing&gt;", "Stack trace · 1 line<", "at run (tool.ts:7)",
		"Events · 2", "retry.scheduled", "attempt", "1 span link to other spans",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page is missing %q", want)
		}
	}

	body = doDetailQuery(t, h, tr.ID, "span=root").Body.String()
	if strings.Contains(body, "</svg> Error</h3>") || strings.Contains(body, "Events ·") {
		t.Errorf("a span with no status text and no events must show neither block")
	}
}
