package web

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// The one search box filters the trace list itself by what spans hold, and
// each row says why it is there: the span, a marked snippet, how many more.
func TestListSearchFiltersTracesAndSaysWhy(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "openllmetry-anthropic-tool-use.pb")
	tr, _ := onlyTrace(t, s)
	ingestCapture(t, s, "openllmetry-anthropic-simple.pb")

	body := doList(t, h, "q=get_weather").Body.String()
	for _, want := range []string{
		`href="/traces/` + tr.ID + `?q=get_weather"`, // the trace opens on its match
		`<tr class="why">`,
		`<mark>get_weather</mark>`,
		`title="Remove this filter">“get_weather”<`,
		"1 of 1 traces",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, store.HitStart) {
		t.Error("a raw hit marker reached the page")
	}
	if all := doList(t, h, "").Body.String(); strings.Contains(all, `class="why"`) || !strings.Contains(all, "2 of 2 traces") {
		t.Error("without a search every trace is listed and no row explains itself")
	}

	none := doList(t, h, "q=zzzunseen").Body.String()
	if !strings.Contains(none, "No trace matches this filter") || !strings.Contains(none, "<b>2</b> traces without") || strings.Contains(none, "No traces yet") {
		t.Errorf("a search without hits must say so and what dropping it lists, never the onboarding page: %s", none)
	}
	if only := doList(t, h, "q=-weather").Body.String(); !strings.Contains(only, "names nothing to look for") {
		t.Error("a search of exclusions only must say it has nothing to look for")
	}
	// Model and kind describe the same span as the words: get_weather is in a claude-haiku generation.
	if body := doList(t, h, "q=get_weather&kind=generation").Body.String(); !strings.Contains(body, "1 of 1 traces") {
		t.Error("kind=generation with the word should still list the trace")
	}
	if body := doList(t, h, "q=get_weather&kind=tool").Body.String(); !strings.Contains(body, "No trace matches these 2 filters together") {
		t.Error("no tool span holds the word: nothing to list")
	}
}

// Filtered pagination: the cursor walks only the matching traces, each once,
// and every page explains its rows.
func TestListSearchPaginates(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now()
	for i, text := range []string{"needle", "hay", "needle again", "hay", "the needle", "hay"} {
		tr := seedTrace(t, s, "trace-"+string(rune('a'+i)), now.Add(time.Duration(i)*time.Second))
		sp := seedSpan(t, s, tr.ID, "s", nil, tr.StartedAt)
		sp.ID, sp.Input = "body", &text
		if err := s.InsertSpan(t.Context(), sp); err != nil {
			t.Fatal(err)
		}
	}
	page1 := doList(t, h, "q=needle&limit=2").Body.String()
	if !strings.Contains(page1, "trace-e") || !strings.Contains(page1, "trace-c") || strings.Contains(page1, "trace-a") || strings.Contains(page1, "trace-d") {
		t.Fatalf("first page should hold the two newest matches only: %s", page1)
	}
	if !strings.Contains(page1, "2 of 3 traces") {
		t.Error("the count is of the whole filtered set")
	}
	next := extractHref(page1, "Next page")
	page2 := doList(t, h, strings.TrimPrefix(strings.ReplaceAll(next, "&amp;", "&"), "/?")).Body.String()
	if !strings.Contains(page2, "trace-a") || strings.Contains(page2, "trace-c") || strings.Contains(page2, "Next page") || strings.Count(page2, `class="why"`) != 1 {
		t.Errorf("second page should hold trace-a alone, explained: %s", page2)
	}
}

// Opening a trace from a search selects its first matching span, marks the
// words in it, and steps through the other matches.
func TestDetailOpensOnTheMatchAndStepsThroughMatches(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "claude-code-tool-read.pb")
	tr, spans := onlyTrace(t, s)
	var tool store.Span
	for _, sp := range spans {
		if sp.Kind == store.SpanKindTool {
			tool = sp
		}
	}

	get := func(query string) string {
		r := httptest.NewRequest("GET", "/traces/"+tr.ID+"?"+query, nil)
		r.SetPathValue("trace_id", tr.ID)
		w := httptest.NewRecorder()
		h.Detail(w, r)
		return w.Body.String()
	}
	body := get("kind=tool")
	for _, want := range []string{
		`<h2>` + tool.Name + `</h2>`, // selected without ?span=
		"Span <b>1</b> of 1 that match",
		`<span class="chip">kind tool</span>`,
		`class="trow match" id="sel"`,
		`>Search results</a>`,
		`href="/?kind=tool"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("kind=tool: body missing %q", want)
		}
	}
	if strings.Contains(body, "<mark>") {
		t.Error("no search words: nothing to mark")
	}

	body = get("q=claude")
	if !strings.Contains(body, " that match") || !strings.Contains(body, "<mark>") || !strings.Contains(body, `&amp;q=claude" hx-get=`) {
		t.Errorf("q=claude: want marks, a position and span links that keep the search: %s", body)
	}
	if strings.Count(body, "<title>") != 0 && strings.Contains(body[:strings.Index(body, `id="span-detail"`)], "<mark>") {
		t.Error("marks belong to the selected span's pane only")
	}
	if other := get("q=zzzunseen"); !strings.Contains(other, "No span of this trace matches.") {
		t.Error("a trace opened with a search it does not match must say so")
	}
}

func TestMarkPage(t *testing.T) {
	page := func(pane string) string {
		return `<h1>error here</h1><div id="span-detail">` + pane + `<i id="span-end" hidden></i></div><p>error after</p>`
	}
	for _, c := range []struct{ name, text, pane, want string }{
		{"prefix, case and accents", "cotacao err",
			`<p>Cotação: Errors, not terror</p>`,
			`<p><mark>Cotação</mark>: <mark>Errors</mark>, not terror</p>`},
		{"a phrase is whole words in order", `"connection refused"`,
			`<p>connection refused; refused connection; connections refused</p>`,
			`<p><mark>connection refused</mark>; refused connection; connections refused</p>`},
		{"an identifier's parts stay together", "get_stock_pri",
			`<b>get_stock_price</b> get stock`,
			`<b><mark>get_stock_price</mark></b> get stock`},
		{"excluded terms are not marked", "alpha -beta", `alpha beta`, `<mark>alpha</mark> beta`},
		{"stored text stays escaped, references are not words", "amp lt script",
			`<p>a &amp; b &lt;script&gt; &#34;amp&#34;</p>`,
			`<p>a &amp; b &lt;<mark>script</mark>&gt; &#34;<mark>amp</mark>&#34;</p>`},
		{"attributes and svg are left alone", "bar",
			`<a title="bar" class="bar">bar</a><svg><title>bar</title><text>bar</text></svg>`,
			`<a title="bar" class="bar"><mark>bar</mark></a><svg><title>bar</title><text>bar</text></svg>`},
		{"a closed disclosure holding a match is opened, others are not", "needle",
			`<details class="more"><summary>Show</summary><details>needle</details></details><details>hay</details>`,
			`<details open class="more"><summary>Show</summary><details open><mark>needle</mark></details></details><details>hay</details>`},
	} {
		if got := markPage(page(c.pane), c.text); got != page(c.want) {
			t.Errorf("%s:\n got %s\nwant %s", c.name, got, page(c.want))
		}
	}
	if got := markPage("<p>error</p>", "error"); got != "<p>error</p>" {
		t.Errorf("a page without the span pane (the Read view) is left as it is, got %s", got)
	}
}

// A snippet is stored text: it must be escaped, with only the markers
// turned into tags.
func TestHitSnippetEscapes(t *testing.T) {
	got := string(hitSnippet(`<script>"a" & ` + store.HitStart + `b` + store.HitEnd))
	if strings.Contains(got, "<script>") || !strings.Contains(got, "&lt;script&gt;") || !strings.HasSuffix(got, `>b</mark>`) {
		t.Errorf("hitSnippet = %q", got)
	}
}

// A snippet is cut from stored JSON: its escaping is noise to the reader, and
// what remains is still escaped as HTML.
func TestHitSnippetDropsJSONEscaping(t *testing.T) {
	got := string(hitSnippet(`{\"city\":\\\"` + store.HitStart + `Paris` + store.HitEnd + `\\\"}\n<b>`))
	if want := `{&#34;city&#34;:&#34;<mark>Paris</mark>&#34;} &lt;b&gt;`; got != want {
		t.Errorf("hitSnippet = %q, want %q", got, want)
	}
}

// A character reference is looked for only at an ampersand: a megabyte of
// text without one is split in a single pass.
func TestWordsOnLargeTextWithoutReferences(t *testing.T) {
	text := strings.Repeat("palavra ", 250_000) + "caf&#39;e &amp; fim"
	start := time.Now()
	ws := words(text)
	if took := time.Since(start); took > 2*time.Second {
		t.Errorf("words took %v on 2 MB", took)
	}
	if n := len(ws); n != 250_003 || ws[n-1].text != "fim" || ws[n-3].text != "caf" {
		t.Errorf("got %d words, last %q", n, ws[n-1].text)
	}
}
