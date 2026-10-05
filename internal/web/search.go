package web

import (
	"html/template"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"

	"github.com/jhisse/spoor/internal/store"
)

var hitMarks = strings.NewReplacer(store.HitEnd, `</mark>`, store.HitStart, `<mark>`)

// jsonNoise is the escaping of JSON stored inside JSON: a snippet is cut out
// of the stored text, so it shows \" and \n where the reader wrote " and a
// line break.
var jsonNoise = strings.NewReplacer(`\\\"`, `"`, `\"`, `"`, `\\n`, " ", `\n`, " ")

// hitSnippet cleans and escapes a snippet, then turns its two markers into a
// tag (spoor.css sets it bold on a tint, not colour alone).
func hitSnippet(s string) template.HTML {
	return template.HTML(hitMarks.Replace(template.HTMLEscapeString(jsonNoise.Replace(s)))) // #nosec G203 -- escaped first; only the markers become tags.
}

// traceSearch is the search the reader arrived with, on the trace page: the
// spans of this trace it describes, and where the selected one is among them.
type traceSearch struct {
	Labels           []string
	Total, At        int // At is the selected span's place among the matches, 0 when it is not one
	PrevURL, NextURL string
	ClearURL         string
	ids              map[string]bool
	order            []store.SpanHit
}

// buildTraceSearch reads hits: this trace's, oldest first.
func buildTraceSearch(u *url.URL, f store.SpanFilter, hits []store.SpanHit, selectedID string) *traceSearch {
	ts := &traceSearch{Total: len(hits), ids: map[string]bool{}, order: hits, ClearURL: withQuery(u, map[string]string{"q": "", "kind": "", "tool": "", "model": "", "failed": ""})}
	spanChips(f, func(label string, _ ...string) { ts.Labels = append(ts.Labels, label) })
	for i, h := range hits {
		ts.ids[h.SpanID] = true
		if h.SpanID == selectedID {
			ts.At = i + 1
		}
	}
	return ts
}

// steps sets the previous and next match. They wrap around; from a span
// that is no match they are the last and the first.
func (ts *traceSearch) steps(traceID string, keep template.URL) {
	if n := len(ts.order); n > 0 {
		at := max(ts.At, 1) - 1
		ts.PrevURL = spanHref(traceID, ts.order[(at+n-1)%n].SpanID, keep)
		ts.NextURL = spanHref(traceID, ts.order[(at+min(ts.At, 1))%n].SpanID, keep)
	}
}

// fold is how the index compares text: case and accents ignored.
func fold(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Mn, r) {
			return -1
		}
		return unicode.ToLower(r)
	}, norm.NFD.String(s))
}

// word is one run of letters and digits in a text, folded, and where it is.
type word struct {
	text       string
	start, end int
}

// words splits escaped HTML text the way the index splits stored text:
// runs of letters and digits. A character reference is a separator, never
// part of a word (every one html/template writes stands for punctuation).
func words(s string) (out []word) {
	start := -1
	flush := func(end int) {
		if start >= 0 {
			out = append(out, word{fold(s[start:end]), start, end})
		}
		start = -1
	}
	for i := 0; i < len(s); {
		if s[i] == '&' {
			if end := strings.IndexByte(s[i:min(i+8, len(s))], ';'); end > 0 {
				flush(i)
				i += end + 1
				continue
			}
		}
		r, size := utf8.DecodeRuneInString(s[i:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.Is(unicode.Mn, r) {
			if start < 0 {
				start = i
			}
		} else {
			flush(i)
		}
		i += size
	}
	flush(len(s))
	return out
}

// hasTerm: the text asks for at least one thing a span must hold.
func hasTerm(text string) bool { return len(searchTerms(text)) > 0 }

// searchTerms is each positive term of text as the folded words it must
// match, in order; prefix says the last one may be the start of a word.
type searchTerm struct {
	words  []string
	prefix bool
}

func searchTerms(text string) (terms []searchTerm) {
	for _, t := range store.ParseSearch(text) {
		if t.Not {
			continue
		}
		term := searchTerm{prefix: !t.Phrase}
		for _, w := range words(t.Text) {
			term.words = append(term.words, w.text)
		}
		terms = append(terms, term)
	}
	return terms
}

// matchAt is how many of ws, from the first, t matches; 0 for none.
func (t searchTerm) matchAt(ws []word) int {
	if len(ws) < len(t.words) {
		return 0
	}
	for i, w := range t.words {
		last := i == len(t.words)-1
		if ws[i].text != w && (!last || !t.prefix || !strings.HasPrefix(ws[i].text, w)) {
			return 0
		}
	}
	return len(t.words)
}

// markText wraps every match in escaped HTML text in <mark>.
func markText(b *strings.Builder, s string, terms []searchTerm) (marked bool) {
	ws, done := words(s), 0
	for i := 0; i < len(ws); i++ {
		n := 0
		for _, t := range terms {
			n = max(n, t.matchAt(ws[i:]))
		}
		if n == 0 {
			continue
		}
		b.WriteString(s[done:ws[i].start] + "<mark>" + s[ws[i].start:ws[i+n-1].end] + "</mark>")
		done, i, marked = ws[i+n-1].end, i+n-1, true
	}
	b.WriteString(s[done:])
	return marked
}

// markPage marks the search's words in the selected span's pane of a
// rendered trace page, and opens every <details> that holds a mark, so a
// match in collapsed text is on screen. It works on the HTML the templates
// wrote: text is already escaped, and an attribute cannot hold a raw ">".
// An <svg> is copied whole.
func markPage(page, text string) string {
	terms := searchTerms(text)
	from, to := strings.Index(page, `id="span-detail"`), strings.Index(page, `<i id="span-end"`)
	if len(terms) == 0 || from < 0 || to < from {
		return page
	}
	from += strings.IndexByte(page[from:], '>') + 1
	var b strings.Builder
	b.WriteString(page[:from])
	var open []int     // where in b each <details not yet closed takes its attribute; -1 once it holds a mark
	var mustOpen []int // those that hold one, ascending
	for rest := page[from:to]; rest != ""; {
		text, tag, found := strings.Cut(rest, "<")
		if markText(&b, text, terms) {
			for i, at := range open {
				if at >= 0 {
					mustOpen, open[i] = append(mustOpen, at), -1
				}
			}
		}
		if !found {
			break
		}
		end := strings.IndexByte(tag, '>') + 1
		switch {
		case strings.HasPrefix(tag, "svg"):
			end = strings.Index(tag, "</svg>") + len("</svg>")
		case strings.HasPrefix(tag, "details"):
			open = append(open, b.Len()+len("<details"))
		case strings.HasPrefix(tag, "/details"):
			open = open[:len(open)-1]
		}
		b.WriteString("<" + tag[:end])
		rest = tag[end:]
	}
	out := b.String()
	for i := len(mustOpen) - 1; i >= 0; i-- { // from the end, so earlier offsets stay valid
		out = out[:mustOpen[i]] + " open" + out[mustOpen[i]:]
	}
	return out + page[to:]
}
