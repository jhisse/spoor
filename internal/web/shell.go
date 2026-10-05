package web

import (
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
)

// shell is what layout.html's header reads. Every page model embeds it and
// renderPage fills it, so a handler cannot leave the header without it.
type shell struct {
	Theme   string   // "light" or "dark"; "" follows the system
	Menu    []string // the service menu: every service a trace was stored under
	Current string   // the service the page is narrowed to; "" = all
	Tab     string   // "traces", "sessions", "blindspots" or "help"; "" on the not-found page
}

func (s *shell) setShell(v shell) { *s = v }

// URL is where tab leads for a service ("" = all): the menu keeps the
// reader on the tab they are on, the tabs keep them on the service.
// Blind spots are not per service, so from there the menu leads to the traces.
func (shell) URL(tab, service string) string {
	u := "/"
	if tab == "sessions" {
		u = "/sessions"
	}
	if service != "" {
		u += "?service=" + url.QueryEscape(service)
	}
	return u
}

// renderPage executes "layout" (the full page), or only its "content" when
// the request carries htmx's HX-Request header: one handler serves both.
// service is the service the page is narrowed to, "" for all of them; the
// tab follows from the template.
func (h *Handlers) renderPage(w http.ResponseWriter, r *http.Request, tmpl *template.Template, service string, data interface{ setShell(shell) }) {
	name := "content"
	if r.Header.Get("HX-Request") != "true" { // a fragment has no header to fill
		name = "layout"
		s := shell{Theme: themeOf(r), Current: service, Tab: "traces"}
		switch tmpl {
		case sessionsTmpl:
			s.Tab = "sessions"
		case blindSpotsTmpl:
			s.Tab = "blindspots"
		case helpTmpl:
			s.Tab = "help"
		case notFoundTmpl:
			s.Tab = ""
		}
		var err error
		if s.Menu, err = h.Store.ListServices(r.Context()); err != nil {
			fail(w, r, err)
			return
		}
		data.setShell(s)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := tmpl.ExecuteTemplate(w, name, data); err != nil {
		fail(w, r, err)
	}
}

// fail answers 500 and logs why: the page says only "internal error", so the
// server log is the one place the cause can be read.
func fail(w http.ResponseWriter, r *http.Request, err error) {
	slog.ErrorContext(r.Context(), "internal error", "method", r.Method, "path", r.URL.Path, "err", err)
	http.Error(w, "internal error", http.StatusInternalServerError)
}
