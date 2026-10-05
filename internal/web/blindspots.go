package web

import (
	"net/http"

	"github.com/jhisse/spoor/internal/store"
)

type blindSpotsPageData struct {
	shell
	store.BlindSpots

	Unpriced      []store.ModelCount // the models on generation spans that match no model_prices row
	UnpricedSpans int64
	RetentionDays int // 0 = keep forever

	// Counters of the ingest server, since the process started; HasIngest
	// is false when this process runs none.
	HasIngest           bool
	Rejected, Discarded int64
}

// BlindSpots renders GET /blind-spots: what is stored but cannot be read,
// and what never reached storage. Read-only; configuration is env vars.
func (h *Handlers) BlindSpots(w http.ResponseWriter, r *http.Request) {
	b, err := h.Store.BlindSpots(r.Context())
	var prices []store.ModelPrice
	if err == nil {
		prices, err = h.Store.ListModelPrices(r.Context())
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	data := blindSpotsPageData{BlindSpots: b, RetentionDays: h.RetentionDays}
	for _, m := range b.ModelSpans {
		if _, priced := store.MatchModelPrice(prices, m.Model); !priced {
			data.Unpriced = append(data.Unpriced, m)
			data.UnpricedSpans += m.Spans
		}
	}
	if h.Ingest != nil {
		data.HasIngest, data.Rejected, data.Discarded = true, h.Ingest.Rejected.Load(), h.Ingest.Discarded.Load()
	}
	h.renderPage(w, r, blindSpotsTmpl, "", &data)
}
