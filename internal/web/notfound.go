package web

import (
	"fmt"
	"net/http"
	"strings"
)

type notFoundPageData struct {
	shell
	Title, Why string
}

// notFoundWriter makes the first byte written carry the 404 status.
type notFoundWriter struct {
	http.ResponseWriter
	sent bool
}

func (n *notFoundWriter) WriteHeader(code int) { n.sent = true; n.ResponseWriter.WriteHeader(code) }

func (n *notFoundWriter) Write(b []byte) (int, error) {
	if !n.sent {
		n.WriteHeader(http.StatusNotFound)
	}
	return n.ResponseWriter.Write(b)
}

// NotFound answers 404 with the page's header and a sentence saying what is
// missing. It is also the UI server's catch-all route.
func (h *Handlers) NotFound(w http.ResponseWriter, r *http.Request) {
	d := notFoundPageData{Title: "Page not found", Why: "There is no page at this address."}
	what := ""
	switch {
	case strings.HasPrefix(r.URL.Path, "/traces/"):
		d.Title, what = "Trace not found", "trace"
	case r.URL.Path == "/sessions", r.URL.Path == "/sessions/export": // the routes exist: only ?session= can miss
		d.Title, what = "Session not found", "session"
	}
	if what != "" {
		d.Why = "This database has no " + what + " with this id."
		if h.RetentionDays > 0 {
			d.Why += fmt.Sprintf(" Traces first stored more than %d days ago are deleted, so it may be one of those.", h.RetentionDays)
		} else {
			d.Why += " The link may come from another database."
		}
	}
	h.renderPage(&notFoundWriter{ResponseWriter: w}, r, notFoundTmpl, "", &d)
}
