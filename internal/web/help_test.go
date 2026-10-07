package web

import (
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// The help page names the addresses this request reached, the shortcuts,
// the MCP tools and the commands, and its tab is the current one.
func TestHelpPage(t *testing.T) {
	h := newTestHandlers(t, newTestStore(t))
	h.IngestAddr = "127.0.0.1:4318"
	h.Version = "1.2.3"
	req := httptest.NewRequest("GET", "/help", nil)
	req.Host = "spoor.local:8080"
	w := httptest.NewRecorder()
	h.Help(w, req)

	body := w.Body.String()
	for _, want := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT=http://spoor.local:4318\n", // an exporter adds /v1/traces itself
		"takes the full address: <code>http://spoor.local:4318/v1/traces</code>",
		"claude mcp add --transport http spoor http://spoor.local:8080/mcp",
		"<kbd>←</kbd>", "<code>search_spans</code>", "<code>spoor export --trace &lt;id&gt;</code>",
		`aria-current="page">Help</a>`,
		"<dd class=\"n\">1.2.3</dd>", "160 models", "forever", // this build, the catalog it ships, the retention
		"<b>~</b> before a cost", "<kbd>p</kbd> and <kbd>n</kbd>",
		"The host in the addresses below", "<code>SPOOR_INGEST_ADDR</code>", // the port is the server's, not the request's
	} {
		if !strings.Contains(body, want) {
			t.Errorf("help page missing %q", want)
		}
	}
	if strings.Contains(body, "OTEL_EXPORTER_OTLP_ENDPOINT=http://spoor.local:4318/v1/traces") {
		t.Error("the generic endpoint variable takes no path")
	}

	// every jump link at the top lands on a heading of the page
	nav := regexp.MustCompile(`(?s)<nav [^>]*aria-label="On this page">(.*?)</nav>`).FindStringSubmatch(body)
	if nav == nil {
		t.Fatal("help page has no jump links")
	}
	links := regexp.MustCompile(`href="#([a-z-]+)"`).FindAllStringSubmatch(nav[1], -1)
	if len(links) != 7 {
		t.Errorf("jump links = %d, want 7 (one per section)", len(links))
	}
	for _, l := range links {
		if !strings.Contains(body, `id="`+l[1]+`"`) {
			t.Errorf("jump link #%s has no target", l[1])
		}
	}
}
