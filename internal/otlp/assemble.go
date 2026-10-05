package otlp

import (
	"encoding/json"
	"time"

	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/jhisse/spoor/internal/store"
)

// assembleTrace builds one batch's view of a trace row: OTLP has no "trace"
// on the wire, so the row is an aggregate over the spans sharing a trace_id.
// Store.MergeTrace folds it onto what earlier batches stored. Without a root
// span in the batch (hasRoot), the oldest span stands in for the name and
// the oldest span carrying a session id for the session; a later batch's
// older non-root span does not replace that placeholder name (accepted gap).
func assembleTrace(spans []*tracev1.Span, resourceMetadata json.RawMessage, now time.Time, traceID string) (t store.Trace, hasRoot bool) {
	t = store.Trace{ID: traceID, Status: store.StatusUnset, Metadata: resourceMetadata, CreatedAt: now}
	var anyError bool
	var oldest, sessionAt time.Time
	for _, span := range spans {
		started, ended := unixNanoTime(span.StartTimeUnixNano), unixNanoTime(span.EndTimeUnixNano)
		if t.StartedAt.IsZero() || started.Before(t.StartedAt) {
			t.StartedAt = started
		}
		if ended.After(t.EndedAt) {
			t.EndedAt = ended
		}
		status := translateStatus(span.Status)
		anyError = anyError || status == store.StatusError
		sid := extractSessionID(span)
		switch {
		case len(span.ParentSpanId) == 0:
			hasRoot, t.Name, t.Status = true, span.Name, status
			if sid != nil {
				t.SessionID, sessionAt = sid, time.Time{} // nothing is older than the zero time
			}
		case !hasRoot && (oldest.IsZero() || started.Before(oldest)):
			t.Name, oldest = span.Name, started
		}
		if sid != nil && (t.SessionID == nil || started.Before(sessionAt)) {
			t.SessionID, sessionAt = sid, started
		}
	}
	if anyError {
		t.Status = store.StatusError
	}
	return t, hasRoot
}

// serviceName is the resource's service.name, the one grouping spoor has
// above the trace; nil when the sender set none.
func serviceName(resource *resourcev1.Resource) *string {
	if v, ok := firstPresent(stringAttrs(resource.GetAttributes()), "service.name"); ok {
		return &v
	}
	return nil
}

// resourceMetadataJSON builds traces.metadata from the batch's Resource
// attributes, under a "resource" key: they describe the producing service,
// so they are stored once on the trace, not on every span.
func resourceMetadataJSON(resource *resourcev1.Resource, scope *commonv1.InstrumentationScope) json.RawMessage {
	metadata := map[string]any{}
	if resource != nil && len(resource.Attributes) > 0 {
		metadata["resource"] = jsonAttrs(resource.Attributes)
	}
	if scope != nil {
		scopeMetadata := map[string]any{"name": scope.Name, "version": scope.Version}
		if len(scope.Attributes) > 0 {
			scopeMetadata["attributes"] = jsonAttrs(scope.Attributes)
		}
		metadata["scope"] = scopeMetadata
	}
	return marshalAttrs(metadata)
}
