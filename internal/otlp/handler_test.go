package otlp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	resourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/jhisse/spoor/internal/store"
	"github.com/jhisse/spoor/internal/store/sqlite"
)

func newHandlerTestStore(t *testing.T) store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spoor.db")
	if err := sqlite.Migrate(path); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	s, err := sqlite.Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newRequestBody(t *testing.T, spans ...*tracev1.Span) []byte {
	t.Helper()
	req := &collectortrace.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{
			{ScopeSpans: []*tracev1.ScopeSpans{{Spans: spans}}},
		},
	}
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling request: %v", err)
	}
	return data
}

// postTraces sends body with no credential, plus any extra headers given
// as name, value pairs.
func postTraces(h *Handler, body []byte, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/v1/traces", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/x-protobuf")
	for i := 0; i < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

// Ingest has no authentication: a request is stored with or without a
// credential header, whatever it holds.
func TestHandlerStoresWithoutAKeyAndIgnoresOne(t *testing.T) {
	st := newHandlerTestStore(t)
	h := &Handler{Store: st}
	for i, headers := range [][]string{
		nil,
		{"Authorization", "Bearer spoor_deadbeef_example-key"},
		{"x-api-key", "anything"},
		{"Authorization", "Bearer one", "x-api-key", "another"},
		{"Authorization", "Basic !!not-base64!!"},
	} {
		span := newTestSpan(testTraceID, fmt.Sprintf("010203040506070%d", i), "", 1, 2, tracev1.Status_STATUS_CODE_UNSET)
		if w := postTraces(h, newRequestBody(t, span), headers...); w.Code != http.StatusOK {
			t.Errorf("headers %v: status = %d, body = %s; want 200", headers, w.Code, w.Body)
		}
	}
	if _, spans, err := st.GetTraceByID(context.Background(), testTraceID); err != nil || len(spans) != 5 {
		t.Errorf("%d spans stored (err %v), want all 5", len(spans), err)
	}
}

// serviceBody is one request whose spans come from the named services, in
// order; "" is a resource that names none.
func serviceBody(t *testing.T, services []string, spans ...*tracev1.Span) []byte {
	t.Helper()
	req := &collectortrace.ExportTraceServiceRequest{}
	for i, service := range services {
		rs := &tracev1.ResourceSpans{Resource: &resourcev1.Resource{}, ScopeSpans: []*tracev1.ScopeSpans{{Spans: spans[i : i+1]}}}
		if service != "" {
			rs.Resource.Attributes = []*commonv1.KeyValue{{Key: "service.name", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: service}}}}
		}
		req.ResourceSpans = append(req.ResourceSpans, rs)
	}
	data, err := proto.Marshal(req)
	if err != nil {
		t.Fatalf("marshaling request: %v", err)
	}
	return data
}

// A trace's service is the resource's service.name: absent until a batch
// names one, then the first one named, also when the root arrives last
// under another service (an agent whose tool runs in a second process).
func TestHandlerTraceServiceAcrossBatches(t *testing.T) {
	st := newHandlerTestStore(t)
	h := &Handler{Store: st}
	child := func(id string) *tracev1.Span {
		return newTestSpan(testTraceID, id, "1112131415161718", 10, 20, tracev1.Status_STATUS_CODE_UNSET)
	}
	root := newTestSpan(testTraceID, "1112131415161718", "", 5, 30, tracev1.Status_STATUS_CODE_OK)
	root.Name = "root"
	for _, step := range []struct {
		body []byte
		want string
	}{
		{serviceBody(t, []string{""}, child("0102030405060701")), "<nil>"},
		{serviceBody(t, []string{"", "worker"}, child("0102030405060702"), child("0102030405060703")), "worker"},
		{serviceBody(t, []string{"gateway"}, root), "worker"},
	} {
		if w := postTraces(h, step.body); w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body)
		}
		tr, _, err := st.GetTraceByID(context.Background(), testTraceID)
		if err != nil {
			t.Fatalf("GetTraceByID: %v", err)
		}
		got := "<nil>"
		if tr.Service != nil {
			got = *tr.Service
		}
		if got != step.want {
			t.Errorf("service = %s, want %s", got, step.want)
		}
	}
	if tr, spans, _ := st.GetTraceByID(context.Background(), testTraceID); tr.Name != "root" || len(spans) != 4 {
		t.Errorf("trace %q with %d spans, want root with 4", tr.Name, len(spans))
	}
	if services, err := st.ListServices(context.Background()); err != nil || fmt.Sprint(services) != "[worker]" {
		t.Errorf("ListServices = %v, %v; want [worker]", services, err)
	}
}

func TestHandlerOrphanSpanThenRootUpdatesTraceName(t *testing.T) {
	st := newHandlerTestStore(t)
	h := &Handler{Store: st}

	now := time.Now()
	child := newTestSpan(testTraceID, "0102030405060708", "1112131415161718",
		uint64(now.UnixNano()), uint64(now.Add(time.Second).UnixNano()), tracev1.Status_STATUS_CODE_UNSET)
	child.Name = "child"
	if w := postTraces(h, newRequestBody(t, child)); w.Code != http.StatusOK {
		t.Fatalf("posting child: status = %d, body = %s", w.Code, w.Body.String())
	}

	trace, spans, err := st.GetTraceByID(context.Background(), testTraceID)
	if err != nil {
		t.Fatalf("GetTraceByID after child: %v", err)
	}
	if trace.Name != "child" || len(spans) != 1 {
		t.Fatalf("after child: Name=%q spans=%d, want child/1", trace.Name, len(spans))
	}

	root := newTestSpan(testTraceID, "1112131415161718", "",
		uint64(now.Add(-time.Second).UnixNano()), uint64(now.Add(2*time.Second).UnixNano()), tracev1.Status_STATUS_CODE_OK)
	root.Name = "root"
	if w := postTraces(h, newRequestBody(t, root)); w.Code != http.StatusOK {
		t.Fatalf("posting root: status = %d, body = %s", w.Code, w.Body.String())
	}

	trace, spans, err = st.GetTraceByID(context.Background(), testTraceID)
	if err != nil {
		t.Fatalf("GetTraceByID after root: %v", err)
	}
	if trace.Name != "root" {
		t.Errorf("Name = %q, want root", trace.Name)
	}
	if len(spans) != 2 {
		t.Errorf("got %d spans, want 2 (child kept, not replaced)", len(spans))
	}
}

// A cost the sender reports is stored as sent, for a model spoor could have
// priced itself, and carries no price row: spoor did not calculate it.
func TestHandlerKeepsReportedCostOfAPricedModel(t *testing.T) {
	st := newHandlerTestStore(t)
	h := &Handler{Store: st}

	span := newTestSpan(testTraceID, "0102030405060708", "", 1, 2, tracev1.Status_STATUS_CODE_OK,
		stringAttr("gen_ai.response.model", "claude-haiku-4-5"),
		stringAttr("gen_ai.usage.input_tokens", "1000"),
		stringAttr("gen_ai.usage.output_tokens", "500"),
		spoorCostAttr(0.5),
	)
	if w := postTraces(h, newRequestBody(t, span)); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	_, spans, err := st.GetTraceByID(context.Background(), testTraceID)
	if err != nil || len(spans) != 1 {
		t.Fatalf("GetTraceByID: %v, %d spans, want 1", err, len(spans))
	}
	if got := spans[0]; got.CostUSD == nil || *got.CostUSD != 0.5 || got.PricePattern != nil || got.PriceFingerprint != nil {
		t.Errorf("cost %v, price row %v; want the reported 0.5 and no price row", got.CostUSD, got.PricePattern)
	}
}

// A real agent call's token counts (OpenInference names), priced by usage
// type at claude-sonnet-5's catalog row: 2/M input, 0.20/M cache read,
// 2.50/M cache write, 10/M output.
func TestHandlerPricesCacheTokensFromCatalogPrices(t *testing.T) {
	st := newHandlerTestStore(t)
	h := &Handler{Store: st}

	span := newTestSpan(testTraceID, "0102030405060708", "", 1, 2, tracev1.Status_STATUS_CODE_OK,
		stringAttr("openinference.span.kind", "LLM"),
		stringAttr("llm.model_name", "claude-sonnet-5"),
		stringAttr("llm.token_count.prompt", "88752"),
		stringAttr("llm.token_count.prompt_details.cache_read", "87486"),
		stringAttr("llm.token_count.prompt_details.cache_write", "1264"),
		stringAttr("llm.token_count.completion", "633"),
	)
	unpriced := newTestSpan(testTraceID, "0102030405060709", "", 1, 2, tracev1.Status_STATUS_CODE_OK,
		stringAttr("openinference.span.kind", "LLM"),
		stringAttr("llm.model_name", "acme/in-house-model"),
		stringAttr("llm.token_count.prompt", "100"),
		stringAttr("llm.token_count.completion", "10"),
	)
	if w := postTraces(h, newRequestBody(t, span, unpriced)); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}

	_, spans, err := st.GetTraceByID(context.Background(), testTraceID)
	if err != nil {
		t.Fatalf("GetTraceByID: %v", err)
	}
	byID := map[string]store.Span{}
	for _, s := range spans {
		byID[s.ID] = s
	}
	priced := byID["0102030405060708"]
	approx(t, priced.CostUSD, 2*0.000002+87486*0.0000002+1264*0.0000025+633*0.00001)
	if priced.CacheReadTokens == nil || *priced.CacheReadTokens != 87486 {
		t.Errorf("CacheReadTokens = %v, want 87486", priced.CacheReadTokens)
	}
	if got := byID["0102030405060709"].CostUSD; got != nil {
		t.Errorf("unpriced model CostUSD = %v, want nil", *got)
	}
}

// gatedStore holds every batch write until both batches have reached it,
// so each batch has already done whatever reading it does: the interleaving
// that loses an update if the merge is read-modify-write in Go.
type gatedStore struct {
	store.Store
	reached sync.WaitGroup
	release chan struct{}
}

func (g *gatedStore) IngestBatch(ctx context.Context, traces []store.TraceMerge, spans []store.Span) error {
	g.reached.Done()
	<-g.release
	return g.Store.IngestBatch(ctx, traces, spans)
}

// A request with one malformed span stores the valid ones and says how
// many it dropped: 200 with partial_success, which an SDK must not retry.
func TestHandlerPartialSuccessStoresValidSpans(t *testing.T) {
	st := newHandlerTestStore(t)
	h := &Handler{Store: st}

	now := uint64(time.Now().UnixNano())
	good := newTestSpan(testTraceID, "0102030405060708", "", now, now+1, tracev1.Status_STATUS_CODE_OK)
	bad := newTestSpan(testTraceID, "0102030405060709", "", now, now+1, tracev1.Status_STATUS_CODE_OK)
	bad.SpanId = bad.SpanId[:3]

	w := postTraces(h, newRequestBody(t, good, bad))
	var resp collectortrace.ExportTraceServiceResponse
	if err := proto.Unmarshal(w.Body.Bytes(), &resp); w.Code != http.StatusOK || err != nil {
		t.Fatalf("status = %d, decoding the response: %v", w.Code, err)
	}
	if got := resp.GetPartialSuccess().GetRejectedSpans(); got != 1 {
		t.Errorf("rejected_spans = %d, want 1", got)
	}
	if _, spans, err := st.GetTraceByID(context.Background(), testTraceID); err != nil || len(spans) != 1 {
		t.Errorf("%d spans stored (err %v), want the 1 valid span", len(spans), err)
	}
}

// failingOnce refuses the first batch, as a full disk or a busy database
// would. That the adapter then leaves nothing half-written is its own test
// (sqlite.TestIngestBatchFailureLeavesNothingWritten).
type failingOnce struct {
	store.Store
	failed bool
}

func (f *failingOnce) IngestBatch(ctx context.Context, traces []store.TraceMerge, spans []store.Span) error {
	if !f.failed {
		f.failed = true
		return errors.New("database or disk is full")
	}
	return f.Store.IngestBatch(ctx, traces, spans)
}

// A storage failure answers a retryable 503; the SDK's retry of the same
// request then stores all of it.
func TestHandlerStorageFailureIsAllOrNothingAndRetryable(t *testing.T) {
	st := newHandlerTestStore(t)
	h := &Handler{Store: &failingOnce{Store: st}}

	now := uint64(time.Now().UnixNano())
	body := newRequestBody(t,
		newTestSpan(testTraceID, "0102030405060708", "", now, now+1, tracev1.Status_STATUS_CODE_OK),
		newTestSpan(testTraceID, "0102030405060709", "0102030405060708", now, now+1, tracev1.Status_STATUS_CODE_OK))

	if w := postTraces(h, body); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", w.Code)
	}
	if _, _, err := st.GetTraceByID(context.Background(), testTraceID); err != store.ErrNotFound {
		t.Errorf("after the failed request: err = %v, want nothing stored", err)
	}
	if spans, err := st.QuerySpans(context.Background(), store.SpanQuery{}); err != nil || len(spans) != 0 {
		t.Errorf("after the failed request: %d spans (err %v), want 0", len(spans), err)
	}

	if w := postTraces(h, body); w.Code != http.StatusOK {
		t.Fatalf("retry: status = %d, want 200", w.Code)
	}
	if _, spans, err := st.GetTraceByID(context.Background(), testTraceID); err != nil || len(spans) != 2 {
		t.Errorf("after the retry: %d spans (err %v), want 2", len(spans), err)
	}
}

func TestConcurrentBatchesOfOneTraceMerge(t *testing.T) {
	st := newHandlerTestStore(t)
	gated := &gatedStore{Store: st, release: make(chan struct{})}
	gated.reached.Add(2)
	h := &Handler{Store: gated}

	t0 := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	root := newTestSpan(testTraceID, "1112131415161718", "",
		uint64(t0.Add(-time.Second).UnixNano()), uint64(t0.Add(2*time.Second).UnixNano()), tracev1.Status_STATUS_CODE_OK)
	root.Name = "root"
	child := newTestSpan(testTraceID, "0102030405060708", "1112131415161718",
		uint64(t0.UnixNano()), uint64(t0.Add(5*time.Second).UnixNano()), tracev1.Status_STATUS_CODE_ERROR)
	child.Name = "child"

	var done sync.WaitGroup
	for _, span := range []*tracev1.Span{root, child} {
		body := newRequestBody(t, span)
		done.Add(1)
		go func() {
			defer done.Done()
			if w := postTraces(h, body); w.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", w.Code)
			}
		}()
	}
	gated.reached.Wait()
	close(gated.release)
	done.Wait()

	got, spans, err := st.GetTraceByID(context.Background(), testTraceID)
	if err != nil {
		t.Fatalf("GetTraceByID: %v", err)
	}
	if len(spans) != 2 {
		t.Errorf("%d spans, want 2", len(spans))
	}
	if got.Name != "root" || got.Status != store.StatusError ||
		!got.StartedAt.Equal(t0.Add(-time.Second)) || !got.EndedAt.Equal(t0.Add(5*time.Second)) {
		t.Errorf("trace = %q %s %v to %v, want root, error, the earliest start and the latest end",
			got.Name, got.Status, got.StartedAt, got.EndedAt)
	}
}
