package otlp

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/hex"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"

	"github.com/jhisse/spoor/internal/store"
)

func loadTestdata(t testing.TB, name, ext string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../testdata/" + name + "." + ext) // #nosec G304 -- test-only, name/ext come from call sites in this file, never external input
	if err != nil {
		t.Fatalf("reading testdata: %v", err)
	}
	return data
}

func newDecodeRequest(t *testing.T, body []byte, contentType string, gzipEncode bool) *http.Request {
	t.Helper()
	if gzipEncode {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		if _, err := gz.Write(body); err != nil {
			t.Fatalf("gzip write: %v", err)
		}
		if err := gz.Close(); err != nil {
			t.Fatalf("gzip close: %v", err)
		}
		body = buf.Bytes()
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
	r.Header.Set("Content-Type", contentType)
	if gzipEncode {
		r.Header.Set("Content-Encoding", "gzip")
	}
	return r
}

// The JSON path must recover the same trace_id/span_id as the protobuf
// capture, not protojson's base64 reading of the hex ids.
func TestDecodeRequestJSONMatchesProtobuf(t *testing.T) {
	jsonBody := loadTestdata(t, "openllmetry-anthropic-tool-use", "json")
	pbBody := loadTestdata(t, "openllmetry-anthropic-tool-use", "pb")

	var want collectortrace.ExportTraceServiceRequest
	if err := proto.Unmarshal(pbBody, &want); err != nil {
		t.Fatalf("unmarshaling reference protobuf: %v", err)
	}
	wantSpan := want.ResourceSpans[0].ScopeSpans[0].Spans[0]

	r := newDecodeRequest(t, jsonBody, "application/json", false)
	got, err := decodeRequest(r)
	if err != nil {
		t.Fatalf("decodeRequest: %v", err)
	}
	gotSpan := got.ResourceSpans[0].ScopeSpans[0].Spans[0]

	if hex.EncodeToString(gotSpan.TraceId) != hex.EncodeToString(wantSpan.TraceId) {
		t.Errorf("TraceId = %x, want %x", gotSpan.TraceId, wantSpan.TraceId)
	}
	if hex.EncodeToString(gotSpan.SpanId) != hex.EncodeToString(wantSpan.SpanId) {
		t.Errorf("SpanId = %x, want %x", gotSpan.SpanId, wantSpan.SpanId)
	}
	if len(gotSpan.TraceId) != 16 {
		t.Errorf("TraceId length = %d, want 16", len(gotSpan.TraceId))
	}
	if len(gotSpan.SpanId) != 8 {
		t.Errorf("SpanId length = %d, want 8", len(gotSpan.SpanId))
	}
}

// parentSpanId is hex in OTLP/JSON like the other two ids: a capture with a
// real tree must decode to the same parents as its protobuf twin.
func TestDecodeRequestJSONParentSpanIDsMatchProtobuf(t *testing.T) {
	const name = "openinference-crewai-workflow"
	var want collectortrace.ExportTraceServiceRequest
	if err := proto.Unmarshal(loadTestdata(t, name, "pb"), &want); err != nil {
		t.Fatalf("unmarshaling reference protobuf: %v", err)
	}
	got, err := decodeRequest(newDecodeRequest(t, loadTestdata(t, name, "json"), "application/json", false))
	if err != nil {
		t.Fatalf("decodeRequest: %v", err)
	}
	parents := func(req *collectortrace.ExportTraceServiceRequest) map[string]string {
		m := map[string]string{}
		for _, rs := range req.ResourceSpans {
			for _, ss := range rs.ScopeSpans {
				for _, sp := range ss.Spans {
					m[hex.EncodeToString(sp.SpanId)] = hex.EncodeToString(sp.ParentSpanId)
				}
			}
		}
		return m
	}
	gotParents, wantParents := parents(got), parents(&want)
	if len(wantParents) != 5 || !maps.Equal(gotParents, wantParents) {
		t.Errorf("parents by span id = %v, want %v", gotParents, wantParents)
	}
}

// A small gzip body that inflates past the limit is refused as too large,
// not read whole or parsed cut short.
func TestDecodeRequestRefusesOversizedBody(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	chunk := make([]byte, 1<<20)
	for range maxRequestBytes>>20 + 1 {
		if _, err := gz.Write(chunk); err != nil {
			t.Fatalf("gzip write: %v", err)
		}
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/traces", &buf)
	r.Header.Set("Content-Encoding", "gzip")
	if _, err := decodeRequest(r); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want the body refused for exceeding the limit", err)
	}
}

func TestDecodeRequestProtobuf(t *testing.T) {
	pbBody := loadTestdata(t, "openllmetry-anthropic-simple", "pb")
	r := newDecodeRequest(t, pbBody, "application/x-protobuf", false)

	got, err := decodeRequest(r)
	if err != nil {
		t.Fatalf("decodeRequest: %v", err)
	}
	if len(got.ResourceSpans) == 0 {
		t.Fatal("no resource spans decoded")
	}
}

func TestDecodeRequestGzipProtobuf(t *testing.T) {
	pbBody := loadTestdata(t, "openllmetry-anthropic-simple", "pb")
	r := newDecodeRequest(t, pbBody, "application/x-protobuf", true)

	got, err := decodeRequest(r)
	if err != nil {
		t.Fatalf("decodeRequest: %v", err)
	}
	if len(got.ResourceSpans) == 0 {
		t.Fatal("no resource spans decoded")
	}
}

func TestDecodeRequestGzipJSON(t *testing.T) {
	jsonBody := loadTestdata(t, "openllmetry-anthropic-simple", "json")
	r := newDecodeRequest(t, jsonBody, "application/json", true)

	got, err := decodeRequest(r)
	if err != nil {
		t.Fatalf("decodeRequest: %v", err)
	}
	if len(got.ResourceSpans) == 0 {
		t.Fatal("no resource spans decoded")
	}
}

// OTLP/JSON receivers must ignore a field they do not know: a sender built on
// a newer protocol than this binary still has its spans stored.
func TestDecodeRequestJSONIgnoresUnknownFields(t *testing.T) {
	jsonBody := loadTestdata(t, "openllmetry-anthropic-simple", "json")
	want, err := decodeRequest(newDecodeRequest(t, jsonBody, "application/json", false))
	if err != nil {
		t.Fatalf("decodeRequest: %v", err)
	}

	var tree map[string]any
	if err := json.Unmarshal(jsonBody, &tree); err != nil {
		t.Fatalf("reading the capture: %v", err)
	}
	tree["futureField"] = "x"
	rs := tree["resourceSpans"].([]any)[0].(map[string]any)
	rs["futureField"] = map[string]any{"nested": 1}
	span := rs["scopeSpans"].([]any)[0].(map[string]any)["spans"].([]any)[0].(map[string]any)
	span["futureField"] = []any{1, "two"}
	withUnknown, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("writing the body: %v", err)
	}

	got, err := decodeRequest(newDecodeRequest(t, withUnknown, "application/json", false))
	if err != nil {
		t.Fatalf("decodeRequest with unknown fields: %v", err)
	}
	if !proto.Equal(got, want) {
		t.Error("the request decoded differently with unknown fields in it")
	}
}

// OTLP/JSON allows a 64-bit integer as a number as well as a string. Neither
// a nanosecond timestamp nor a large intValue fits a float64 exactly, so both
// must come through as sent.
func TestDecodeRequestJSONKeepsLargeIntegers(t *testing.T) {
	body := `{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":"5b8efff798038103d269b633813fc60c","spanId":"eee19b7ec3c1b174",
		"startTimeUnixNano":1762740000123456789,"attributes":[{"key":"n","value":{"intValue":9007199254740993}}]}]}]}]}`
	got, err := decodeRequest(newDecodeRequest(t, []byte(body), "application/json", false))
	if err != nil {
		t.Fatalf("decodeRequest: %v", err)
	}
	span := got.ResourceSpans[0].ScopeSpans[0].Spans[0]
	if span.StartTimeUnixNano != 1762740000123456789 {
		t.Errorf("startTimeUnixNano = %d, want 1762740000123456789", span.StartTimeUnixNano)
	}
	if n := span.Attributes[0].Value.GetIntValue(); n != 9007199254740993 {
		t.Errorf("intValue = %d, want 9007199254740993", n)
	}

	if _, err := decodeRequest(newDecodeRequest(t, []byte(`{} {"resourceSpans":[]}`), "application/json", false)); err == nil {
		t.Error("a body with data after the JSON value was accepted")
	}
}

// Any body, in either encoding, is decoded or refused: never a panic. The
// port is open to every local process.
func FuzzDecodeRequest(f *testing.F) {
	for _, name := range []string{"openllmetry-anthropic-simple", "claude-code-tool-read", "otel-openai-v2-events"} {
		f.Add(loadTestdata(f, name, "json"), true)
		f.Add(loadTestdata(f, name, "pb"), false)
	}
	f.Add([]byte(`{"resourceSpans":[{"scopeSpans":[{"spans":[{"traceId":7,"spanId":"zz","parentSpanId":null}]}]}]}`), true)
	f.Fuzz(func(t *testing.T, body []byte, asJSON bool) {
		contentType := "application/x-protobuf"
		if asJSON {
			contentType = "application/json"
		}
		r := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		req, err := decodeRequest(r)
		if err != nil {
			return
		}
		h := &Handler{Store: discardStore{}}
		if _, err := h.ingestRequest(t.Context(), req); err != nil {
			t.Fatalf("ingesting a decoded request: %v", err)
		}
	})
}

// discardStore prices nothing and stores nothing: the fuzz target runs the
// translation without a database.
type discardStore struct{ store.Store }

func (discardStore) ListModelPrices(context.Context) ([]store.ModelPrice, error) { return nil, nil }

func (discardStore) IngestBatch(context.Context, []store.TraceMerge, []store.Span) error {
	return nil
}
