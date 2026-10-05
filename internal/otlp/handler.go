package otlp

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/jhisse/spoor/internal/store"
)

// Handler is the POST /v1/traces receiver. There is no authentication:
// whoever reaches the port can write, and a credential header is ignored.
type Handler struct {
	Store store.Store

	// Rejected counts the spans refused for a malformed id and Discarded
	// the log and metric requests dropped, both since the process started:
	// the blind-spots page says what never reached storage.
	Rejected, Discarded atomic.Int64
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	req, err := decodeRequest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf("decoding request: %v", err), http.StatusBadRequest)
		return
	}

	rejected, err := h.ingestRequest(r.Context(), req)
	if err != nil {
		// 503, not 500: OTLP/HTTP senders retry only 429, 502, 503 and 504, and
		// nothing was written, so resending the whole request is safe.
		slog.ErrorContext(r.Context(), "storage unavailable", "path", r.URL.Path, "err", err)
		http.Error(w, "storage unavailable", http.StatusServiceUnavailable)
		return
	}

	h.Rejected.Add(rejected)
	writeResponse(w, r, rejected)
}

// Discard answers POST /v1/logs and /v1/metrics with an empty OTLP success
// (the same bytes for every signal), storing nothing: an SDK exporting all
// three signals must not log errors.
func (h *Handler) Discard(w http.ResponseWriter, r *http.Request) {
	h.Discarded.Add(1)
	writeResponse(w, r, 0)
}

func (h *Handler) ingestRequest(ctx context.Context, req *collectortrace.ExportTraceServiceRequest) (rejected int64, err error) {
	groups := map[string]*spanGroup{}
	for _, rs := range req.ResourceSpans {
		service := serviceName(rs.Resource)
		for _, ss := range rs.ScopeSpans {
			resourceMeta := resourceMetadataJSON(rs.Resource, ss.Scope)
			for _, span := range ss.Spans {
				if len(span.TraceId) != 16 || len(span.SpanId) != 8 {
					rejected++
					continue
				}
				traceID := hex.EncodeToString(span.TraceId)
				g := groups[traceID]
				if g == nil {
					g = &spanGroup{resourceMeta: resourceMeta}
					groups[traceID] = g
				}
				if g.service == nil {
					g.service = service
				}
				g.spans = append(g.spans, span)
			}
		}
	}
	if len(groups) == 0 {
		return rejected, nil
	}
	prices, err := h.Store.ListModelPrices(ctx)
	if err != nil {
		return rejected, fmt.Errorf("listing model prices: %w", err)
	}

	now := time.Now().UTC()
	var traces []store.TraceMerge
	var spans []store.Span
	for traceID, g := range groups {
		for _, span := range g.spans {
			domainSpan := translateSpan(span, now)
			if domainSpan.CostUSD == nil {
				priceSpan(prices, &domainSpan)
			}
			spans = append(spans, domainSpan)
		}

		t, hasRoot := assembleTrace(g.spans, g.resourceMeta, now, traceID)
		t.Service = g.service
		traces = append(traces, store.TraceMerge{Trace: t, HasRoot: hasRoot})
	}
	return rejected, h.Store.IngestBatch(ctx, traces, spans)
}

type spanGroup struct {
	spans        []*tracev1.Span
	resourceMeta json.RawMessage
	service      *string // the first service.name among the trace's resources
}
