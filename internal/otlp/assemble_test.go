package otlp

import (
	"testing"
	"time"

	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/jhisse/spoor/internal/store"
)

const testTraceID = "0102030405060708090a0b0c0d0e0f10"

func TestAssembleTraceAnyErrorPropagates(t *testing.T) {
	now := time.Now().UTC()

	root := newTestSpan(testTraceID, "1112131415161718", "",
		uint64(now.UnixNano()), uint64(now.Add(time.Second).UnixNano()), tracev1.Status_STATUS_CODE_OK)
	child := newTestSpan(testTraceID, "0102030405060708", "1112131415161718",
		uint64(now.UnixNano()), uint64(now.Add(time.Second).UnixNano()), tracev1.Status_STATUS_CODE_ERROR)

	got, _ := assembleTrace([]*tracev1.Span{root, child}, nil, now, testTraceID)
	if got.Status != store.StatusError {
		t.Errorf("Status = %q, want error (a child errored even though root is ok)", got.Status)
	}
}

func TestAssembleTraceMinMaxTimestamps(t *testing.T) {
	now := time.Now().UTC()

	early := newTestSpan(testTraceID, "0102030405060708", "",
		uint64(now.UnixNano()), uint64(now.Add(time.Second).UnixNano()), tracev1.Status_STATUS_CODE_UNSET)
	late := newTestSpan(testTraceID, "1112131415161718", "0102030405060708",
		uint64(now.Add(5*time.Second).UnixNano()), uint64(now.Add(10*time.Second).UnixNano()), tracev1.Status_STATUS_CODE_UNSET)

	wantEnded := now.Add(10 * time.Second)
	// In either order: a batch does not arrive sorted by time.
	for _, spans := range [][]*tracev1.Span{{early, late}, {late, early}} {
		got, _ := assembleTrace(spans, nil, now, testTraceID)
		if !got.StartedAt.Equal(now) {
			t.Errorf("StartedAt = %v, want %v (earliest span)", got.StartedAt, now)
		}
		if !got.EndedAt.Equal(wantEnded) {
			t.Errorf("EndedAt = %v, want %v (latest span)", got.EndedAt, wantEnded)
		}
	}
}

func TestAssembleTraceSessionIDPromotedFromAttribute(t *testing.T) {
	now := time.Now().UTC()
	span := newTestSpan(testTraceID, "0102030405060708", "",
		uint64(now.UnixNano()), uint64(now.Add(time.Second).UnixNano()), tracev1.Status_STATUS_CODE_UNSET,
		stringAttr("gen_ai.conversation.id", "conv-123"))

	got, _ := assembleTrace([]*tracev1.Span{span}, nil, now, testTraceID)
	if got.SessionID == nil || *got.SessionID != "conv-123" {
		t.Errorf("SessionID = %v, want conv-123", got.SessionID)
	}
}

func TestAssembleTraceRootSessionIDWinsOverOlderChild(t *testing.T) {
	now := time.Now().UTC()
	child := newTestSpan(testTraceID, "1112131415161718", "0102030405060708",
		uint64(now.UnixNano()), uint64(now.Add(time.Second).UnixNano()), tracev1.Status_STATUS_CODE_UNSET,
		stringAttr("thread_id", "from-child"))
	root := newTestSpan(testTraceID, "0102030405060708", "",
		uint64(now.Add(time.Second).UnixNano()), uint64(now.Add(2*time.Second).UnixNano()), tracev1.Status_STATUS_CODE_UNSET,
		stringAttr("session.id", "from-root"))

	for _, spans := range [][]*tracev1.Span{{child, root}, {root, child}} {
		got, _ := assembleTrace(spans, nil, now, testTraceID)
		if got.SessionID == nil || *got.SessionID != "from-root" {
			t.Errorf("SessionID = %v, want from-root", got.SessionID)
		}
	}
}

func TestAssembleTraceSessionIDFromOldestSpanWithoutRoot(t *testing.T) {
	now := time.Now().UTC()
	newer := newTestSpan(testTraceID, "1112131415161718", "0102030405060708",
		uint64(now.Add(time.Second).UnixNano()), uint64(now.Add(2*time.Second).UnixNano()), tracev1.Status_STATUS_CODE_UNSET,
		stringAttr("thread_id", "newer"))
	older := newTestSpan(testTraceID, "2122232425262728", "0102030405060708",
		uint64(now.UnixNano()), uint64(now.Add(time.Second).UnixNano()), tracev1.Status_STATUS_CODE_UNSET,
		stringAttr("thread_id", "older"))

	got, _ := assembleTrace([]*tracev1.Span{newer, older}, nil, now, testTraceID)
	if got.SessionID == nil || *got.SessionID != "older" {
		t.Errorf("SessionID = %v, want older", got.SessionID)
	}
}

// A batch reports whether it carried the root, and names itself after the
// root when it did, after its oldest span when it did not.
func TestAssembleTraceReportsRoot(t *testing.T) {
	now := time.Now().UTC()
	child := newTestSpan(testTraceID, "0102030405060708", "1112131415161718",
		uint64(now.UnixNano()), uint64(now.Add(time.Second).UnixNano()), tracev1.Status_STATUS_CODE_UNSET)
	child.Name = "child"
	root := newTestSpan(testTraceID, "1112131415161718", "",
		uint64(now.Add(time.Second).UnixNano()), uint64(now.Add(2*time.Second).UnixNano()), tracev1.Status_STATUS_CODE_OK)
	root.Name = "root"

	got, hasRoot := assembleTrace([]*tracev1.Span{child}, nil, now, testTraceID)
	if hasRoot || got.Name != "child" || got.Status != store.StatusUnset {
		t.Errorf("child only: hasRoot=%v name=%q status=%s, want false, child, unset", hasRoot, got.Name, got.Status)
	}
	for _, spans := range [][]*tracev1.Span{{child, root}, {root, child}} {
		got, hasRoot = assembleTrace(spans, nil, now, testTraceID)
		if !hasRoot || got.Name != "root" || got.Status != store.StatusOK {
			t.Errorf("with root: hasRoot=%v name=%q status=%s, want true, root, ok", hasRoot, got.Name, got.Status)
		}
	}
}

// Without a root the oldest span names the trace, wherever it sits in the batch.
func TestAssembleTraceRootlessNameIsTheOldestSpans(t *testing.T) {
	now := time.Now().UTC()
	newer := newTestSpan(testTraceID, "1112131415161718", "0102030405060708",
		uint64(now.Add(time.Second).UnixNano()), uint64(now.Add(2*time.Second).UnixNano()), tracev1.Status_STATUS_CODE_UNSET)
	newer.Name = "newer"
	older := newTestSpan(testTraceID, "2122232425262728", "0102030405060708",
		uint64(now.UnixNano()), uint64(now.Add(time.Second).UnixNano()), tracev1.Status_STATUS_CODE_UNSET)
	older.Name = "older"

	for _, spans := range [][]*tracev1.Span{{newer, older}, {older, newer}} {
		if got, hasRoot := assembleTrace(spans, nil, now, testTraceID); hasRoot || got.Name != "older" {
			t.Errorf("hasRoot=%v name=%q, want false, older", hasRoot, got.Name)
		}
	}
}
