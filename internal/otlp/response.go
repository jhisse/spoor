package otlp

import (
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// writeResponse encodes ExportTraceServiceResponse in the request's own
// format, with partial_success only when spans were rejected.
func writeResponse(w http.ResponseWriter, r *http.Request, rejectedSpans int64) {
	resp := &collectortrace.ExportTraceServiceResponse{}
	if rejectedSpans > 0 {
		resp.PartialSuccess = &collectortrace.ExportTracePartialSuccess{
			RejectedSpans: rejectedSpans,
			ErrorMessage:  fmt.Sprintf("%d span(s) rejected: missing or malformed trace_id/span_id", rejectedSpans),
		}
	}
	marshal, contentType := proto.Marshal, "application/x-protobuf"
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		marshal, contentType = protojson.Marshal, "application/json"
	}
	data, err := marshal(resp)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", contentType)
	_, _ = w.Write(data)
}
