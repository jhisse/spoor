package web

import (
	"net"
	"net/http"
	"net/url"
	"slices"
)

// SameOrigin guards the UI. spoor has no login, so without it any other site
// open in the operator's browser could reach the UI under a hostname of its
// own (DNS rebinding) and read prompts, or POST to it (CSRF). Every request
// must carry a loopback or allowed Host; one that is not GET/HEAD must also
// carry an Origin (failing that, Referer) naming that same Host, which
// browsers always send.
func SameOrigin(allowedHosts []string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := (&url.URL{Host: r.Host}).Hostname()
		// /healthz carries no data and is probed by IP address.
		ok := LoopbackHost(host) || host != "" && slices.Contains(allowedHosts, host) || r.URL.Path == "/healthz"
		if ok && r.Method != http.MethodGet && r.Method != http.MethodHead {
			source := r.Header.Get("Origin")
			if source == "" {
				source = r.Header.Get("Referer")
			}
			from, err := url.Parse(source)
			// An MCP client is not a browser and names no origin; a browser always does.
			ok = err == nil && from.Host == r.Host || source == "" && r.URL.Path == "/mcp"
		}
		if !ok {
			http.Error(w, "forbidden: cross-origin request, or a Host that is neither loopback nor in SPOOR_ALLOWED_HOSTS", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'") // one-click buttons must not be framed
		next.ServeHTTP(w, r)
	})
}

// LoopbackHost reports whether host (no port) can only be reached from this
// machine. An empty host, as in ":8080", is every interface.
func LoopbackHost(host string) bool {
	return host == "localhost" || net.ParseIP(host).IsLoopback()
}
