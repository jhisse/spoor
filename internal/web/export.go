package web

import (
	"bytes"
	"errors"
	"html/template"
	"io"
	"net/http"
	"strings"

	"github.com/jhisse/spoor/internal/store"
)

type exportTrace struct {
	Trace  store.Trace
	Totals traceTotals
	Spans  []exportSpan
}

// exportSpan is what the detail page's own "span_detail" template reads
// (detailPageData), plus the span's depth in the tree.
type exportSpan struct {
	detailPageData
	Depth int
}

// The static page has no split view: with no server to ask for another
// span, every span is rendered in full, in tree order, indented by depth.
var exportTmpl = template.Must(template.Must(detailTmpl.Clone()).New("export").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="color-scheme" content="light dark">
<title>spoor export</title>
<style>{{.CSS}}</style>
</head>
<body>
{{template "icons"}}
<main class="page">
{{range .Traces}}
<section class="export stack">
<div class="head"><h1>{{template "status_glyph" .Trace.Status}} {{.Trace.Name}} <span class="sub">{{.Trace.ID}}</span></h1><span class="cap">spoor export · static page</span></div>
<div class="panel"><div class="band"><dl class="stats grow">
<div><dt>Status</dt><dd{{if eq .Trace.Status "error"}} class="err-t"{{end}}>{{.Trace.Status}}</dd></div>
<div><dt>Started</dt><dd>{{.Trace.StartedAt.Format "2006-01-02 15:04:05"}} <small>UTC</small></dd></div>
<div><dt>Duration</dt><dd>{{fmtDuration (.Trace.EndedAt.Sub .Trace.StartedAt)}}</dd></div>
<div><dt>Cost</dt><dd>{{costVal .Totals.CostUSD}}</dd></div>
<div><dt>Tokens</dt><dd class="row">{{tokenPair .Totals.InputTokens .Totals.OutputTokens}}{{tokenBar .Totals.Tokens 0 120 8}}</dd></div>
<div><dt>Spans</dt><dd>{{.Totals.SpanCount}}</dd></div>
</dl><div class="meta">{{template "metadata_block" .Trace.Metadata}}</div></div></div>
{{range .Spans}}<div class="panel" style="--depth:{{.Depth}}">{{template "span_detail" .}}</div>{{end}}
</section>
{{end}}
</main>
</body>
</html>
`))

// ExportHTML writes traces (spans[i] belonging to traces[i]) as one
// self-contained page: the stylesheet is inlined and there is no script,
// link or image, so the file makes no request when opened.
func ExportHTML(w io.Writer, traces []store.Trace, spans [][]store.Span) error {
	css, err := staticFS.ReadFile("static/spoor.css")
	if err != nil {
		return err
	}
	data := struct {
		CSS    template.CSS
		Traces []exportTrace
	}{CSS: template.CSS(css)} // #nosec G203 -- our own embedded stylesheet, not request data
	for i, t := range traces {
		roots := BuildSpanTree(spans[i])
		et := exportTrace{Trace: t, Totals: computeTraceTotals(spans[i], roots)}
		appendExportSpans(&et.Spans, roots, 0)
		data.Traces = append(data.Traces, et)
	}
	return exportTmpl.Execute(w, data)
}

func appendExportSpans(out *[]exportSpan, nodes []*SpanNode, depth int) {
	for _, n := range nodes {
		var d detailPageData
		d.showSpan(&n.Span)
		*out = append(*out, exportSpan{detailPageData: d, Depth: depth})
		appendExportSpans(out, n.Children, depth+1)
	}
}

// Export answers GET /traces/{trace_id}/export and GET /sessions/export
// (?session=) with the page `spoor export --html` writes, as a download.
func (h *Handlers) Export(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	kind, id := "trace", r.PathValue("trace_id")
	ids := []string{id}
	var err error
	if id == "" {
		kind, id, ids = "session", r.URL.Query().Get("session"), nil
		var turns []store.TraceSummary
		_, turns, err = h.Store.GetSession(ctx, id)
		for _, t := range turns {
			ids = append(ids, t.ID)
		}
	}
	traces, spans := make([]store.Trace, len(ids)), make([][]store.Span, len(ids))
	for i := 0; i < len(ids) && err == nil; i++ {
		traces[i], spans[i], err = h.Store.GetTraceByID(ctx, ids[i])
	}
	var page bytes.Buffer // held back, so that a failure is not half a file
	if err == nil {
		err = ExportHTML(&page, traces, spans)
	}
	if errors.Is(err, store.ErrNotFound) {
		h.NotFound(w, r)
		return
	}
	if err != nil {
		fail(w, r, err)
		return
	}
	// An id is any string the sender chose; the file name keeps what is safe in one.
	name := strings.Map(func(c rune) rune {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", c) {
			return c
		}
		return '_'
	}, id)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="spoor-`+kind+"-"+name+`.html"`)
	_, _ = page.WriteTo(w)
}
