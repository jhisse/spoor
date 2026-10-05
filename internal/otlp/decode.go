// Package otlp translates OTLP into the internal schema. It is the one
// place that reads gen_ai.* (or any other) attribute names, so a change in
// a semantic convention is a change here only.
package otlp

import (
	"compress/gzip"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

const maxRequestBytes = 64 << 20 // 64 MiB; guards against an unbounded gzip bomb or body
// OTLP/JSON is decoded into a generic tree and re-encoded before protojson
// reads it, several times the body in memory, so it gets a smaller limit.
const maxJSONBytes = 8 << 20

// decodeRequest reads an OTLP body: protobuf or JSON, gzip or not.
func decodeRequest(r *http.Request) (*collectortrace.ExportTraceServiceRequest, error) {
	body := io.Reader(r.Body)
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(body)
		if err != nil {
			return nil, fmt.Errorf("decompressing gzip body: %w", err)
		}
		defer func() { _ = gz.Close() }()
		body = gz
	}

	data, err := io.ReadAll(io.LimitReader(body, maxRequestBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading request body: %w", err)
	}
	if len(data) > maxRequestBytes {
		return nil, fmt.Errorf("request body exceeds %d bytes", maxRequestBytes)
	}

	req := &collectortrace.ExportTraceServiceRequest{}
	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "application/json") {
		if len(data) > maxJSONBytes {
			return nil, fmt.Errorf("JSON request body exceeds %d bytes", maxJSONBytes)
		}
		if err := unmarshalOTLPJSON(data, req); err != nil {
			return nil, fmt.Errorf("decoding OTLP JSON: %w", err)
		}
		return req, nil
	}
	// No content type means protobuf, every OTLP exporter's default.
	if err := proto.Unmarshal(data, req); err != nil {
		return nil, fmt.Errorf("decoding OTLP protobuf: %w", err)
	}
	return req, nil
}

// unmarshalOTLPJSON decodes OTLP/JSON, which writes traceId, spanId and
// parentSpanId as hex. protojson expects base64 for bytes fields and would
// decode the hex into wrong-length garbage with no error, so those three
// fields are converted to base64 first.
func unmarshalOTLPJSON(data []byte, req *collectortrace.ExportTraceServiceRequest) error {
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return err
	}
	hexIDsToBase64(generic)
	fixed, err := json.Marshal(generic)
	if err != nil {
		return err
	}
	return protojson.Unmarshal(fixed, req)
}

func hexIDsToBase64(v any) {
	switch val := v.(type) {
	case map[string]any:
		for k, sub := range val {
			if k == "traceId" || k == "spanId" || k == "parentSpanId" {
				if s, ok := sub.(string); ok && s != "" {
					if b, err := hex.DecodeString(s); err == nil {
						val[k] = base64.StdEncoding.EncodeToString(b)
					}
				}
				continue
			}
			hexIDsToBase64(sub)
		}
	case []any:
		for _, item := range val {
			hexIDsToBase64(item)
		}
	}
}
