package web

import (
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/jhisse/spoor/internal/otlp"
	"github.com/jhisse/spoor/internal/store"
)

//go:embed templates/*.html
var templatesFS embed.FS

// usd4 is a dollar amount to four decimals. A real amount too small for them
// is said to be small: "$0.0000" would read as free.
func usd4(v float64) string {
	if v > 0 && v < 0.00005 {
		return "<$0.0001"
	}
	return fmt.Sprintf("$%.4f", v)
}

// strVal, int64Val and costVal exist because printing a pointer to a scalar
// in a template shows its address, not its value.
var funcMap = template.FuncMap{
	"snippet":  hitSnippet,
	"strVal":   store.Deref[string],
	"int64Val": store.Deref[int64],
	"costVal": func(p *float64) string {
		if p == nil {
			return "–"
		}
		return usd4(*p)
	},
	"fmtDuration":  fmtDuration,
	"relTime":      relTime,
	"metaJSON":     metaJSON,
	"metadataTree": buildMetadataTree,
	"excerpt":      excerpt,
	"jsonTree":     func(s string) []metadataNode { return buildMetadataTree(json.RawMessage(s)) },
	"isJSON":       func(s string) bool { return json.Valid([]byte(s)) },
	"legend":       legend,
	"commas":       commas,

	"timeBar":     timeBar,
	"kindClass":   kindClass,
	"kindLetter":  func(k store.SpanKind) string { return kindLetters[k] },
	"spanHref":    spanHref,
	"rowDuration": rowDuration,
	"rowCost":     rowCost,
	"tokenBar":    tokenBar,
	"tokenPair":   tokenPair,
	"tokenSum":    tokenSum,
	"fmtCount":    fmtCount,
	"oddMin":      func() int { return oddMin },
	"usd":         usd,
	"healthGlyph": healthGlyph,
	"sessionURL":  sessionURL,
	"spanEvents":  spanEvents,
	"errorReason": errorReason,
	"purpose":     otlp.Purpose,
	"rootless":    rootless,
}

// metaJSON pretty-prints a metadata bag as it is, with no per-key
// mapping. NULL, "null" and "{}" collapse to "", so a template can gate
// the block with {{with}}.
func metaJSON(m json.RawMessage) string {
	s := strings.TrimSpace(string(m))
	if s == "" || s == "null" || s == "{}" {
		return ""
	}
	return prettyJSON(s)
}

// fmtDuration is the one way a duration is written: a number, a space, a
// unit, never more than two units ("0 ms", "300 µs", "204 ms", "1.2 s",
// "2 min 31 s", "1 h 05 min"). Zero is "0 ms" and anything shorter than a
// millisecond keeps its microseconds, so a fast span does not read as no
// time at all.
func fmtDuration(d time.Duration) string {
	switch {
	case d == 0:
		return "0 ms"
	case d < time.Millisecond:
		return fmt.Sprintf("%d µs", d.Round(time.Microsecond).Microseconds())
	case d < 999500*time.Microsecond:
		return fmt.Sprintf("%d ms", d.Round(time.Millisecond).Milliseconds())
	case d < 59950*time.Millisecond:
		return fmt.Sprintf("%.1f s", d.Seconds())
	}
	sec := int(d.Round(time.Second) / time.Second)
	switch {
	case sec >= 3600:
		return fmt.Sprintf("%d h %02d min", sec/3600, sec%3600/60)
	case sec%60 == 0:
		return fmt.Sprintf("%d min", sec/60)
	}
	return fmt.Sprintf("%d min %d s", sec/60, sec%60)
}

// relTime formats t relative to now, in fmtDuration's units; absolute
// beyond a week. Templates pair it with a title carrying the full UTC
// timestamp.
func relTime(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < 10*time.Second:
		return "now"
	case d < time.Minute:
		return fmt.Sprintf("%d s ago", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%d d ago", int(d.Hours()/24))
	default:
		return t.Format("2006-01-02 15:04")
	}
}

// parsePage parses layout.html with one page's own files as a
// self-contained set: every page defines its own "content", which would
// collide in one shared template namespace.
func parsePage(pages ...string) *template.Template {
	return template.Must(template.New("root").Funcs(funcMap).ParseFS(templatesFS, append(pages, "templates/layout.html")...))
}

var (
	listTmpl       = parsePage("templates/list.html")
	detailTmpl     = parsePage("templates/detail.html", "templates/span.html", "templates/explain.html", "templates/events.html", "templates/treeviews.html")
	blindSpotsTmpl = parsePage("templates/blindspots.html")
	sessionsTmpl   = parsePage("templates/sessions.html")
	notFoundTmpl   = parsePage("templates/notfound.html")
	helpTmpl       = parsePage("templates/help.html")
)
