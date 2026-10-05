package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func postTheme(t *testing.T, value, referer string) *http.Response {
	t.Helper()
	r := httptest.NewRequest("POST", "http://spoor.test/theme", strings.NewReader(url.Values{"theme": {value}}.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if referer != "" {
		r.Header.Set("Referer", referer)
	}
	w := httptest.NewRecorder()
	Theme(w, r)
	if w.Code != http.StatusSeeOther {
		t.Errorf("theme=%q: status = %d, want 303", value, w.Code)
	}
	return w.Result()
}

func TestThemeSetsTheCookie(t *testing.T) {
	for _, value := range []string{"light", "dark"} {
		got := postTheme(t, value, "").Header.Get("Set-Cookie")
		if want := "spoor_theme=" + value + "; Path=/; Max-Age=34560000; HttpOnly; SameSite=Lax"; got != want {
			t.Errorf("theme=%s: Set-Cookie = %q, want %q", value, got, want)
		}
	}
}

func TestThemeSystemDeletesTheCookie(t *testing.T) {
	cookies := postTheme(t, "system", "").Cookies()
	if len(cookies) != 1 || cookies[0].Name != "spoor_theme" || cookies[0].MaxAge >= 0 {
		t.Errorf("theme=system should delete the cookie, got %+v", cookies)
	}
}

func TestThemeIgnoresAnythingElse(t *testing.T) {
	for _, value := range []string{"", "blue", "dark; x=y"} {
		if got := postTheme(t, value, "").Header.Get("Set-Cookie"); got != "" {
			t.Errorf("theme=%q should set no cookie, got %q", value, got)
		}
	}
}

// The Referer is request data, so only the path of one naming this host is
// followed.
func TestThemeRedirectStaysOnThisHost(t *testing.T) {
	for referer, want := range map[string]string{
		"http://spoor.test/traces/abc?span=s1&fold=0": "/traces/abc?span=s1&fold=0",
		"http://spoor.test":                           "/",
		"":                                            "/",
		"http://evil.test/traces/abc":                 "/",
		"http://spoor.test//evil.test/":               "/",
		"//evil.test/x":                               "/",
		"javascript:alert(1)":                         "/",
		"http://spoor.test.evil.test/":                "/",
	} {
		if got := postTheme(t, "dark", referer).Header.Get("Location"); got != want {
			t.Errorf("Referer %q: Location = %q, want %q", referer, got, want)
		}
	}
}

// shellPage is one full page of the UI, as the tests below request it.
type shellPage struct {
	name, target, tab string // tab: the one the header marks current
	handler           http.HandlerFunc
	pathValues        map[string]string
}

func (p shellPage) get(t *testing.T, theme string) string {
	t.Helper()
	r := httptest.NewRequest("GET", p.target, nil)
	for k, v := range p.pathValues {
		r.SetPathValue(k, v)
	}
	if theme != "" {
		r.Header.Set("Cookie", "spoor_theme="+theme)
	}
	w := httptest.NewRecorder()
	p.handler(w, r)
	if w.Code != 200 {
		t.Fatalf("%s: status = %d, body: %s", p.name, w.Code, w.Body.String())
	}
	return w.Body.String()
}

// shellPages seeds service "acme" (with a session) and service "other", and
// returns every page of the UI narrowed to acme where a page can be, plus
// the three tab URLs by name.
func shellPages(t *testing.T) ([]shellPage, map[string]string) {
	t.Helper()
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	seedSessionTurn(t, s, "trace-1", "sess-1", time.Now())
	seedServiceTrace(t, s, "acme", "trace-1", time.Now())
	seedServiceTrace(t, s, "other", "trace-2", time.Now())

	tabs := map[string]string{"Traces": "/?service=acme", "Sessions": "/sessions?service=acme", "Blind spots": "/blind-spots"}
	return []shellPage{
		{"list", tabs["Traces"], "Traces", h.List, nil},
		{"trace", "/traces/trace-1", "Traces", h.Detail, map[string]string{"trace_id": "trace-1"}},
		{"sessions", tabs["Sessions"], "Sessions", h.Sessions, nil},
		{"session", tabs["Sessions"] + "&session=sess-1", "Sessions", h.Sessions, nil},
	}, tabs
}

// The guard against a page forgetting the header: each carries the service
// menu and the three tabs, with its own current.
func TestShellOnEveryPage(t *testing.T) {
	pages, tabs := shellPages(t)
	for _, page := range pages {
		body := page.get(t, "")
		want := []string{
			`<span class="ellip" title="acme">acme</span></summary>`, `aria-current="true">acme</a>`, `>other</a>`, `>All services</a>`,
			`href="#main"`, `<main id="main"`, `id="hatch"`,
		}
		for name, href := range tabs {
			current := ""
			if name == page.tab {
				current = ` aria-current="page"`
			}
			want = append(want, `<a href="`+href+`"`+current+`>`+name+`</a>`)
		}
		for _, w := range want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: page missing %s", page.name, w)
			}
		}
		if strings.Count(body, `aria-current="page"`) != 1 {
			t.Errorf("%s: want exactly one current tab", page.name)
		}
	}
}

// data-theme is on <html> only when the cookie names a theme; otherwise the
// page follows the system.
func TestThemeCookieSetsDataTheme(t *testing.T) {
	pages, _ := shellPages(t)
	for cookie, theme := range map[string]string{"": "system", "blue": "system", "light": "light", "dark": "dark"} {
		for _, page := range pages {
			body := page.get(t, cookie)
			attr := `<html lang="en" data-theme="` + theme + `">`
			if theme == "system" {
				attr = `<html lang="en">`
			}
			if !strings.Contains(body, attr) {
				t.Errorf("%s, cookie %q: want %s", page.name, cookie, attr)
			}
			if !strings.Contains(body, `value="`+theme+`" aria-pressed="true"`) || strings.Count(body, `aria-pressed="true"`) != 1 {
				t.Errorf("%s, cookie %q: the %s button alone should be pressed", page.name, cookie, theme)
			}
		}
	}
}

// With no service chosen every tab still leads somewhere, and from Blind spots
// (which no service narrows) the menu leads to that service's traces.
func TestShellWithoutService(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	seedServiceTrace(t, s, "acme", "trace-1", time.Now())

	body := doList(t, h, "").Body.String()
	for _, want := range []string{
		`<span class="ellip" title="">All services</span></summary>`,
		`<a href="/" aria-current="true">All services</a>`,
		`<a href="/" aria-current="page">Traces</a>`,
		`<a href="/sessions">Sessions</a>`,
		`<a href="/blind-spots">Blind spots</a>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("header missing %s", want)
		}
	}
	body = blindSpotsBody(t, h)
	for _, want := range []string{`<a href="/?service=acme" title="acme">acme</a>`, `<a href="/blind-spots" aria-current="page">Blind spots</a>`, `<a href="/">Traces</a>`} {
		if !strings.Contains(body, want) {
			t.Errorf("blind-spots header missing %s", want)
		}
	}
}

// An htmx swap replaces the page's content only.
func TestFragmentHasNoShell(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)

	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("HX-Request", "true")
	w := httptest.NewRecorder()
	h.List(w, r)
	if body := w.Body.String(); w.Code != 200 || strings.Contains(body, "<header") || strings.Contains(body, "<html") {
		t.Errorf("fragment: status %d, should carry no layout, got: %s", w.Code, body)
	}
}
