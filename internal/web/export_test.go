package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func doExport(t *testing.T, h *Handlers, target string) *httptest.ResponseRecorder {
	t.Helper()
	mux := http.NewServeMux() // the patterns of cmd/spoor's httpMux
	mux.HandleFunc("GET /traces/{trace_id}/export", h.Export)
	mux.HandleFunc("GET /sessions/export", h.Export)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
	return w
}

// The download is the static page, named after what it holds.
func TestExportDownloadsTheStaticPage(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	seedSessionTurn(t, s, "trace-1", "conv/1", time.Now().UTC())
	seedSessionTurn(t, s, "trace-2", "conv/1", time.Now().UTC().Add(time.Minute))

	for _, c := range []struct{ target, file, absent string }{
		{"/traces/trace-1/export", "spoor-trace-trace-1.html", "trace-2"},
		{"/sessions/export?session=conv%2F1", "spoor-session-conv_1.html", ""},
	} {
		w := doExport(t, h, c.target)
		if w.Code != 200 {
			t.Fatalf("%s: status = %d, want 200, body: %s", c.target, w.Code, w.Body.String())
		}
		if got, want := w.Header().Get("Content-Disposition"), `attachment; filename="`+c.file+`"`; got != want {
			t.Errorf("%s: Content-Disposition = %q, want %q", c.target, got, want)
		}
		if got := w.Header().Get("Content-Type"); got != "text/html; charset=utf-8" {
			t.Errorf("%s: Content-Type = %q", c.target, got)
		}
		body := w.Body.String()
		for _, want := range []string{"<!doctype html>", "<style>", "spoor export · static page", `<span class="sub">trace-1</span>`, "qual a capital?", "Brasília", "</html>"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: page is missing %q", c.target, want)
			}
		}
		if strings.Contains(body, "<script") || strings.Contains(body, "<link") {
			t.Errorf("%s: the page must work as a lone file, got a script or a link", c.target)
		}
		// A session is every turn; a trace is that trace alone.
		if (c.absent == "") != strings.Contains(body, "trace-2") {
			t.Errorf("%s: wrong turns in the page (second turn present = %v)", c.target, c.absent != "")
		}
	}
}

func TestExportNotFoundForUnknownID(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	seedSessionTurn(t, s, "trace-1", "conv-1", time.Now().UTC())

	for target, title := range map[string]string{
		"/traces/does-not-exist/export": "Trace not found",
		"/sessions/export?session=nope": "Session not found",
		"/sessions/export":              "Session not found",
	} {
		w := doExport(t, h, target)
		if w.Code != 404 || !strings.Contains(w.Body.String(), title) {
			t.Errorf("%s: status = %d, want 404 and %q", target, w.Code, title)
		}
		if got := w.Header().Get("Content-Disposition"); got != "" {
			t.Errorf("%s: a miss must not be a download, got Content-Disposition %q", target, got)
		}
	}
}
