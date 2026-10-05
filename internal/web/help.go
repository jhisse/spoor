package web

import (
	"net"
	"net/http"
	"net/url"
)

type helpPageData struct {
	shell
	IngestBase, IngestURL, UIURL, Version string
	Prices, RetentionDays                 int
}

// ingestBase is the ingest server as this request reached the UI: the UI's
// host with the ingest server's port. An OTLP/HTTP exporter given this adds
// /v1/traces itself.
func (h *Handlers) ingestBase(r *http.Request) string {
	return "http://" + net.JoinHostPort((&url.URL{Host: r.Host}).Hostname(), (&url.URL{Host: h.IngestAddr}).Port())
}

func (h *Handlers) ingestURL(r *http.Request) string { return h.ingestBase(r) + "/v1/traces" }

// Help renders GET /help: how to send traces, what the marks mean, the
// search, the keyboard shortcuts, the MCP endpoint, the commands and this
// build. The addresses are the ones this request reached.
func (h *Handlers) Help(w http.ResponseWriter, r *http.Request) {
	prices, err := h.Store.ListModelPrices(r.Context())
	if err != nil {
		fail(w, r, err)
		return
	}
	h.renderPage(w, r, helpTmpl, "", &helpPageData{IngestBase: h.ingestBase(r), IngestURL: h.ingestURL(r),
		UIURL: "http://" + r.Host, Version: h.Version, Prices: len(prices), RetentionDays: h.RetentionDays})
}
