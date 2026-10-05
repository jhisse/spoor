package web

import (
	"html/template"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// The store clamps the limit too; this one shapes what a caller asks for.
const (
	defaultLimit = 50
	maxLimit     = 200
)

type listParams struct {
	Service string
	From    *time.Time
	To      *time.Time
	Status  *store.Status
	Span    store.SpanFilter
	Ranges  map[store.Measure]store.Range
	Heat    store.Measure // the heat map's measure; "" = not drawn
	Cursor  *store.Cursor
	Limit   int
	Live    bool
	Odd     bool // ?sort=odd: the newest maxLimit traces, the furthest from typical first; no pages
}

// parseListParams reads the list's query string. Parsing is lenient: a
// malformed or stale bookmarked value is dropped or reset to a default,
// never a 400.
func parseListParams(q url.Values) listParams {
	p := listParams{Service: q.Get("service"), Span: parseSpanFilter(q), Limit: defaultLimit, Live: q.Get("live") == "1", Odd: q.Get("sort") == "odd", Ranges: map[store.Measure]store.Range{}}
	if from, ok := parseDateTime(q.Get("from")); ok {
		p.From = &from
	}
	if to, ok := parseDateTime(q.Get("to")); ok {
		p.To = &to
	}
	if status := store.Status(q.Get("status")); slices.Contains(statuses, status) {
		p.Status = &status
	}
	for m, sc := range heatScales {
		lo, _ := strconv.ParseFloat(q.Get(sc.param+"_min"), 64)
		hi, _ := strconv.ParseFloat(q.Get(sc.param+"_max"), 64)
		if lo > 0 || hi > 0 {
			p.Ranges[m] = store.Range{Min: lo, Max: hi}
		}
	}
	if m := store.Measure(q.Get("y")); heatScales[m].param != "" {
		p.Heat = m
	}
	if cursor, err := store.DecodeCursor(q.Get("cursor")); err == nil {
		p.Cursor = &cursor
	}
	if limit, err := strconv.Atoi(q.Get("limit")); err == nil && limit > 0 && limit <= maxLimit {
		p.Limit = limit
	}
	return p
}

// more says a filter of the form's "More filters" group is set.
func (p listParams) more() bool {
	return p.Span.Kind != "" || p.Status != nil || len(p.Ranges) > 0 || p.From != nil || p.To != nil
}

func (p listParams) query() store.TraceQuery {
	q := store.TraceQuery{Service: p.Service, From: p.From, To: p.To, Status: p.Status, Span: p.Span, Ranges: p.Ranges, Cursor: p.Cursor, Limit: p.Limit}
	if p.Odd {
		q.Cursor, q.Limit = nil, maxLimit
	}
	return q
}

// parseSpanFilter reads the parameters that describe a span: ?q=, ?kind=,
// ?tool=, ?model= and ?failed=1. The list reads them as "traces with such a
// span"; the trace page, handed the same ones, as "these spans of this
// trace". ?tool= is a tool span's name, so it implies the kind.
func parseSpanFilter(q url.Values) store.SpanFilter {
	f := store.SpanFilter{Text: strings.TrimSpace(q.Get("q")), Model: q.Get("model"), Failed: q.Get("failed") == "1"}
	if kind := store.SpanKind(q.Get("kind")); kindLetters[kind] != "" {
		f.Kind = kind
	}
	if f.Name = q.Get("tool"); f.Name != "" {
		f.Kind = store.SpanKindTool
	}
	return f
}

// spanQuery is the span parameters of q as "&k=v…", for a trace link.
func spanQuery(q url.Values) template.URL {
	keep := url.Values{}
	for _, k := range []string{"q", "kind", "tool", "model", "failed"} {
		if v := q.Get(k); v != "" {
			keep.Set(k, v)
		}
	}
	if len(keep) == 0 {
		return ""
	}
	return template.URL("&" + keep.Encode()) // #nosec G203 -- Encode escapes every value.
}

// chip is one filter in force: what it says and the URL that drops it.
// Without is how many traces match once it is dropped; it is counted only
// when nothing matches.
type chip struct{ Label, URL, Without string }

// spanChips names each part of f; the list's chips and the trace page's
// search bar both say it this way.
func spanChips(f store.SpanFilter, add func(label string, params ...string)) {
	if f.Text != "" {
		add("“"+f.Text+"”", "q")
	}
	if f.Model != "" {
		add("model "+f.Model, "model")
	}
	if f.Name != "" {
		add("tool "+f.Name, "tool", "kind")
	} else if f.Kind != "" {
		add("kind "+string(f.Kind), "kind")
	}
	if f.Failed {
		add("failed", "failed")
	}
}

// chips lists every filter of the URL, span filters first.
func (p listParams) chips(u *url.URL) (chips []chip) {
	add := func(label string, params ...string) {
		drop := map[string]string{"cursor": ""}
		for _, k := range params {
			drop[k] = ""
		}
		chips = append(chips, chip{Label: label, URL: withQuery(u, drop)})
	}
	spanChips(p.Span, add)
	if p.Status != nil {
		add("status "+string(*p.Status), "status")
	}
	if p.From != nil {
		add("from "+p.From.Format("Jan 2, 15:04")+" UTC", "from")
	}
	if p.To != nil {
		add("to "+p.To.Format("Jan 2, 15:04")+" UTC", "to")
	}
	for _, m := range measures {
		if r, ok := p.Ranges[m]; ok {
			add(string(m)+" "+heatScales[m].rangeText(r.Min, r.Max), heatScales[m].param+"_min", heatScales[m].param+"_max")
		}
	}
	if p.Service != "" {
		add("service "+p.Service, "service")
	}
	return chips
}

// dateTimeLocalLayout is what an <input type="datetime-local"> submits (no
// seconds, no offset). It is read as UTC, never the server's local time.
const dateTimeLocalLayout = "2006-01-02T15:04"

// parseDateTime accepts RFC3339 (a hand-built or bookmarked URL) or the
// datetime-local layout of the filter form. Malformed input is ok=false.
func parseDateTime(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	t, err := time.ParseInLocation(dateTimeLocalLayout, s, time.UTC)
	return t, err == nil
}

// withQuery returns u with each param of set applied; "" removes it, and so
// does an empty value a form submitted, so a URL names only what is set.
func withQuery(u *url.URL, set map[string]string) string {
	c, q := *u, u.Query()
	for k, v := range set {
		q.Set(k, v)
	}
	for k, v := range q {
		if len(v) == 0 || v[0] == "" {
			q.Del(k)
		}
	}
	c.RawQuery = q.Encode()
	return c.String()
}

// buildNextURL returns the next page's link, or "" if there isn't one.
// live is dropped: a further page is never the live tail.
func buildNextURL(current *url.URL, next *store.Cursor) string {
	if next == nil {
		return ""
	}
	return withQuery(current, map[string]string{"cursor": store.EncodeCursor(*next), "live": ""})
}

// toggleURL turns a switch of the list on (key=value) or off. Turning it
// on strips cursor: the odd-first order has no pages, and a live tail only
// makes sense on the newest page.
func toggleURL(current *url.URL, key, value string, on bool) string {
	if !on {
		return withQuery(current, map[string]string{key: ""})
	}
	return withQuery(current, map[string]string{key: value, "cursor": ""})
}
