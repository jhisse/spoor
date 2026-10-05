package otlp

import (
	"encoding/hex"
	"encoding/json"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/jhisse/spoor/internal/store"
)

// translateEvents keeps every event as sent: no name is filtered or interpreted.
func translateEvents(events []*tracev1.Span_Event) json.RawMessage {
	out := make([]store.SpanEvent, len(events))
	for i, e := range events {
		out[i] = store.SpanEvent{Name: e.Name, Time: unixNanoTime(e.TimeUnixNano), Attributes: attrsJSON(e.Attributes)}
	}
	return jsonArray(out)
}

type spanLink struct {
	TraceID    string          `json:"trace_id"`
	SpanID     string          `json:"span_id"`
	Attributes json.RawMessage `json:"attributes,omitempty"`
}

func translateLinks(links []*tracev1.Span_Link) json.RawMessage {
	out := make([]spanLink, len(links))
	for i, l := range links {
		out[i] = spanLink{TraceID: hex.EncodeToString(l.TraceId), SpanID: hex.EncodeToString(l.SpanId), Attributes: attrsJSON(l.Attributes)}
	}
	return jsonArray(out)
}

func attrsJSON(attrs []*commonv1.KeyValue) json.RawMessage {
	if len(attrs) == 0 {
		return nil
	}
	b, _ := json.Marshal(jsonAttrs(attrs)) // strings, numbers, booleans, lists and maps of them: cannot fail
	return b
}

// jsonArray is nil for an empty list, so the column stays NULL.
func jsonArray[T any](items []T) json.RawMessage {
	if len(items) == 0 {
		return nil
	}
	b, _ := json.Marshal(items) // strings, times and already-valid JSON: cannot fail
	return b
}

// Exception is an event recorded under OpenTelemetry's exception convention:
// named "exception", with exception.type or .message, and maybe a stack trace.
type Exception struct {
	Type, Message, Stacktrace string
}

// ExceptionOf reads e as an exception; ok is false for any other event.
func ExceptionOf(e store.SpanEvent) (ex Exception, ok bool) {
	if e.Name != "exception" {
		return ex, false
	}
	var attrs map[string]any
	_ = json.Unmarshal(e.Attributes, &attrs) // no attributes: an exception with nothing to say
	ex.Type, _ = attrs["exception.type"].(string)
	ex.Message, _ = attrs["exception.message"].(string)
	ex.Stacktrace, _ = attrs["exception.stacktrace"].(string)
	return ex, true
}
