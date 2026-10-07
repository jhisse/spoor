package web

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// The tab icon every page links to is served by the embedded assets.
func TestFaviconIsServed(t *testing.T) {
	w := httptest.NewRecorder()
	StaticHandler().ServeHTTP(w, httptest.NewRequest("GET", "/static/favicon.svg", nil))
	if w.Code != 200 {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	if !strings.Contains(w.Body.String(), `<svg xmlns="http://www.w3.org/2000/svg"`) {
		t.Error("favicon.svg is not a standalone SVG: it needs the xmlns")
	}
}
