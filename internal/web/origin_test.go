package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSameOrigin(t *testing.T) {
	var reached bool
	h := SameOrigin([]string{"spoor.internal", ""}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	for _, c := range []struct {
		name, method, host, origin, referer string
		want                                int
	}{
		{"same-origin post on loopback", "POST", "localhost:8090", "http://localhost:8090", "", 200},
		{"same-origin post on 127.0.0.1", "POST", "127.0.0.1:8090", "http://127.0.0.1:8090", "", 200},
		{"same-origin post on ipv6 loopback", "POST", "[::1]:8090", "http://[::1]:8090", "", 200},
		{"referer stands in for a missing origin", "POST", "localhost:8090", "", "http://localhost:8090/blind-spots", 200},
		{"allowed host", "POST", "spoor.internal", "https://spoor.internal", "", 200},
		{"cross-site post", "POST", "localhost:8090", "https://evil.example", "", 403},
		{"another port on the same machine", "POST", "localhost:8090", "http://localhost:3000", "", 403},
		{"cross-site referer", "POST", "localhost:8090", "", "https://evil.example/page", 403},
		{"origin wins over a matching referer", "POST", "localhost:8090", "https://evil.example", "http://localhost:8090/", 403},
		{"opaque origin", "POST", "localhost:8090", "null", "", 403},
		{"no origin and no referer", "POST", "localhost:8090", "", "", 403},
		{"rebound host, origin matching it", "POST", "evil.example:8090", "http://evil.example:8090", "", 403},
		{"lan host not allowed", "POST", "192.168.1.5:8090", "http://192.168.1.5:8090", "", 403},
		{"empty host", "POST", "", "null", "", 403},
		{"delete is mutating too", "DELETE", "localhost:8090", "https://evil.example", "", 403},
		{"read on loopback needs no origin", "GET", "localhost:8090", "", "", 200},
		{"read under an allowed host", "GET", "spoor.internal", "", "", 200},
		{"read under a rebound host", "GET", "evil.example:8090", "", "", 403},
	} {
		r := httptest.NewRequest(c.method, "/theme", nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if c.referer != "" {
			r.Header.Set("Referer", c.referer)
		}
		w := httptest.NewRecorder()
		reached = false
		h.ServeHTTP(w, r)
		if w.Code != c.want || reached != (c.want == 200) {
			t.Errorf("%s: status %d (handler reached: %v), want %d", c.name, w.Code, reached, c.want)
		}
	}
}

// An orchestrator probes /healthz by IP address; it carries no data.
func TestSameOriginLetsHealthzThrough(t *testing.T) {
	h := SameOrigin(nil, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	r := httptest.NewRequest("GET", "/healthz", nil)
	r.Host = "10.0.0.7:8080"
	w := httptest.NewRecorder()
	if h.ServeHTTP(w, r); w.Code != 200 {
		t.Errorf("status %d, want 200", w.Code)
	}
}

func TestLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{"127.0.0.1": true, "::1": true, "localhost": true, "": false, "0.0.0.0": false, "192.168.1.5": false, "spoor.internal": false} {
		if got := LoopbackHost(host); got != want {
			t.Errorf("LoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}
