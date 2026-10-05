package otlp

import (
	"context"
	"crypto/rand"
	"fmt"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/jhisse/spoor/testdata"
)

// LoadDemo ingests every embedded capture through the same path as POST
// /v1/traces. The captures are adjusted here, never in the translation
// layer: traces end 4 minutes apart up to now, and each multi-turn pair gets
// a shared session id (the SDK emitted none).
func (h *Handler) LoadDemo(ctx context.Context, now time.Time) (traces int, err error) {
	names, _ := fs.Glob(testdata.FS, "*.pb")
	for i, name := range names {
		req, spans, err := freshCapture(name, now.Add(-time.Duration(len(names)-1-i)*4*time.Minute))
		if err != nil {
			return 0, err
		}
		if session, _, multiTurn := strings.Cut(name, "-multiturn"); multiTurn {
			for _, s := range spans {
				s.Attributes = append(s.Attributes, &commonv1.KeyValue{Key: sessionIDKeys[0], Value: &commonv1.AnyValue{
					Value: &commonv1.AnyValue_StringValue{StringValue: "demo-" + session},
				}})
			}
		}
		if _, err := h.ingestRequest(ctx, req); err != nil {
			return 0, fmt.Errorf("ingesting %s: %w", name, err)
		}
	}
	return len(names), nil
}

// Sample serves one capture re-stamped to now under a new trace id, so the
// curl the UI prints yields a new, visible trace each time it is pasted.
func Sample(w http.ResponseWriter, _ *http.Request) {
	req, spans, _ := freshCapture("openllmetry-anthropic-simple.pb", time.Now())
	id := make([]byte, 16)
	_, _ = rand.Read(id)
	for _, s := range spans {
		s.TraceId = id
	}
	body, _ := proto.Marshal(req)
	_, _ = w.Write(body)
}

// freshCapture decodes an embedded capture and shifts its timestamps so
// its last span ends at end.
func freshCapture(name string, end time.Time) (*collectortrace.ExportTraceServiceRequest, []*tracev1.Span, error) {
	raw, _ := testdata.FS.ReadFile(name)
	req := &collectortrace.ExportTraceServiceRequest{}
	if err := proto.Unmarshal(raw, req); err != nil {
		return nil, nil, fmt.Errorf("decoding %s: %w", name, err)
	}
	var spans []*tracev1.Span
	var last uint64
	for _, rs := range req.ResourceSpans {
		for _, ss := range rs.ScopeSpans {
			spans = append(spans, ss.Spans...)
		}
	}
	for _, s := range spans {
		last = max(last, s.EndTimeUnixNano)
	}
	shift := uint64(end.UnixNano()) - last // #nosec G115 -- the current time is positive
	for _, s := range spans {
		s.StartTimeUnixNano += shift
		s.EndTimeUnixNano += shift
		for _, e := range s.Events {
			e.TimeUnixNano += shift
		}
	}
	return req, spans, nil
}
