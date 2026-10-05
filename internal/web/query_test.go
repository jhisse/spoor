package web

import (
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func TestParseListParamsDefaults(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	p := parseListParams(r.URL.Query())

	if p.Limit != defaultLimit {
		t.Errorf("Limit = %d, want %d", p.Limit, defaultLimit)
	}
	if p.From != nil || p.To != nil || p.Status != nil || p.Cursor != nil {
		t.Errorf("expected all filters unset for an empty query string, got %+v", p)
	}
}

func TestParseListParamsMalformedValuesAreLenient(t *testing.T) {
	r := httptest.NewRequest("GET", "/?from=not-a-date&status=bogus&cursor=not-valid-base64!!&limit=abc", nil)
	p := parseListParams(r.URL.Query())

	if p.From != nil {
		t.Errorf("From = %v, want nil for malformed input", p.From)
	}
	if p.Status != nil {
		t.Errorf("Status = %v, want nil for unrecognized value", p.Status)
	}
	if p.Cursor != nil {
		t.Errorf("Cursor = %v, want nil for malformed input", p.Cursor)
	}
	if p.Limit != defaultLimit {
		t.Errorf("Limit = %d, want default %d for unparsable input", p.Limit, defaultLimit)
	}
}

func TestParseListParamsLimitCapped(t *testing.T) {
	r := httptest.NewRequest("GET", "/?limit=99999", nil)
	p := parseListParams(r.URL.Query())
	if p.Limit != defaultLimit {
		t.Errorf("Limit = %d, want default %d when the requested value exceeds maxLimit", p.Limit, defaultLimit)
	}
}

func TestParseListParamsValidValues(t *testing.T) {
	r := httptest.NewRequest("GET", "/?service=svc-1&q=checkout&status=error&from=2026-01-01T00:00:00Z&limit=10", nil)
	p := parseListParams(r.URL.Query())

	if p.Service != "svc-1" || p.Span.Text != "checkout" {
		t.Errorf("Service/Q = %q/%q, want svc-1/checkout", p.Service, p.Span.Text)
	}
	if p.Status == nil || *p.Status != store.StatusError {
		t.Errorf("Status = %v, want error", p.Status)
	}
	if p.From == nil || !p.From.Equal(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("From = %v, want 2026-01-01T00:00:00Z", p.From)
	}
	if p.Limit != 10 {
		t.Errorf("Limit = %d, want 10", p.Limit)
	}
}

// RFC3339 (a hand-built URL) and the datetime-local layout both parse, and
// the datetime-local value, which has no timezone, is read as UTC.
func TestParseDateTimeAcceptsBothLayouts(t *testing.T) {
	want := time.Date(2026, 3, 4, 15, 30, 0, 0, time.UTC)

	got, ok := parseDateTime("2026-03-04T15:30:00Z")
	if !ok || !got.Equal(want) {
		t.Errorf("parseDateTime(RFC3339) = %v, %v, want %v, true", got, ok, want)
	}

	got, ok = parseDateTime("2026-03-04T15:30")
	if !ok || !got.Equal(want) {
		t.Errorf("parseDateTime(datetime-local) = %v, %v, want %v, true (interpreted as UTC)", got, ok, want)
	}
}

func TestParseDateTimeMalformedIsLenient(t *testing.T) {
	if _, ok := parseDateTime("not-a-date"); ok {
		t.Error("parseDateTime(malformed) ok = true, want false")
	}
	if _, ok := parseDateTime(""); ok {
		t.Error("parseDateTime(\"\") ok = true, want false")
	}
}

func TestBuildNextURLNilCursor(t *testing.T) {
	u, _ := url.Parse("http://example.com/?limit=10")
	if got := buildNextURL(u, nil); got != "" {
		t.Errorf("buildNextURL(nil) = %q, want empty", got)
	}
}

func TestBuildNextURLSetsCursor(t *testing.T) {
	u, _ := url.Parse("http://example.com/?limit=10")
	cursor := store.Cursor{StartedAt: time.Now(), ID: "trace-1"}

	got := buildNextURL(u, &cursor)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	decoded, err := store.DecodeCursor(parsed.Query().Get("cursor"))
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if decoded.ID != cursor.ID {
		t.Errorf("round-tripped cursor ID = %q, want %q", decoded.ID, cursor.ID)
	}
	if parsed.Query().Get("limit") != "10" {
		t.Errorf("buildNextURL should preserve existing query params, got %q", got)
	}
}

func TestBuildNextURLStripsLiveFlag(t *testing.T) {
	u, _ := url.Parse("http://example.com/?live=1")
	cursor := store.Cursor{StartedAt: time.Now(), ID: "trace-1"}

	got := buildNextURL(u, &cursor)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if parsed.Query().Get("live") != "" {
		t.Errorf("buildNextURL should drop live, got %q", got)
	}
}

func TestParseListParamsLiveFlag(t *testing.T) {
	r := httptest.NewRequest("GET", "/?live=1", nil)
	if p := parseListParams(r.URL.Query()); !p.Live {
		t.Errorf("Live = %v, want true for ?live=1", p.Live)
	}

	r = httptest.NewRequest("GET", "/", nil)
	if p := parseListParams(r.URL.Query()); p.Live {
		t.Errorf("Live = %v, want false when absent", p.Live)
	}
}

func TestBuildLiveURLOnStripsCursor(t *testing.T) {
	u, _ := url.Parse("http://example.com/?service=svc-1&cursor=abc")
	got := toggleURL(u, "live", "1", true)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if parsed.Query().Get("live") != "1" {
		t.Errorf("toggleURL(true) should set live=1, got %q", got)
	}
	if parsed.Query().Get("cursor") != "" {
		t.Errorf("toggleURL(true) should drop cursor, got %q", got)
	}
	if parsed.Query().Get("service") != "svc-1" {
		t.Errorf("toggleURL should preserve other params, got %q", got)
	}
}

func TestBuildLiveURLOffRemovesFlag(t *testing.T) {
	u, _ := url.Parse("http://example.com/?live=1&service=svc-1")
	got := toggleURL(u, "live", "1", false)
	parsed, err := url.Parse(got)
	if err != nil {
		t.Fatalf("url.Parse(%q): %v", got, err)
	}
	if parsed.Query().Get("live") != "" {
		t.Errorf("toggleURL(false) should remove live, got %q", got)
	}
	if parsed.Query().Get("service") != "svc-1" {
		t.Errorf("toggleURL should preserve other params, got %q", got)
	}
}

// The span parameters describe one span; ?tool= is a tool span's name.
func TestParseSpanFilter(t *testing.T) {
	q, _ := url.ParseQuery("q=+rm+-rf+&kind=generation&tool=Bash&model=m1&failed=1")
	if got, want := parseSpanFilter(q), (store.SpanFilter{Text: "rm -rf", Kind: store.SpanKindTool, Name: "Bash", Model: "m1", Failed: true}); got != want {
		t.Errorf("parseSpanFilter = %+v, want %+v", got, want)
	}
	q, _ = url.ParseQuery("kind=nonsense&failed=yes")
	if got := parseSpanFilter(q); got != (store.SpanFilter{}) {
		t.Errorf("unknown kind and a failed that is not 1 must be dropped, got %+v", got)
	}
	if got := spanQuery(url.Values{"q": {"a b"}, "tool": {"Bash"}, "status": {"error"}, "span": {"x"}}); got != "&q=a+b&tool=Bash" {
		t.Errorf("spanQuery = %q, want only the span parameters", got)
	}
}

// Ranges are independent, one per measure, and a chip drops exactly its own filter.
func TestListParamsRangesAndChips(t *testing.T) {
	u, _ := url.Parse("/?q=timeout&status=error&cost_min=0.01&cost_max=abc&dur_max=10&tok_min=-5&service=api&cursor=xyz&model=")
	p := parseListParams(u.Query())
	if want := (map[store.Measure]store.Range{store.MeasureCost: {Min: 0.01}, store.MeasureDuration: {Max: 10}}); !reflect.DeepEqual(p.Ranges, want) {
		t.Errorf("Ranges = %+v, want %+v (a malformed or negative bound is no bound)", p.Ranges, want)
	}
	var got []string
	for _, c := range p.chips(u) {
		got = append(got, c.Label+" -> "+c.URL)
	}
	want := []string{
		"“timeout” -> /?cost_max=abc&cost_min=0.01&dur_max=10&service=api&status=error&tok_min=-5",
		"status error -> /?cost_max=abc&cost_min=0.01&dur_max=10&q=timeout&service=api&tok_min=-5",
		"cost $0.01 or more -> /?dur_max=10&q=timeout&service=api&status=error&tok_min=-5",
		"duration 0s – 10s -> /?cost_max=abc&cost_min=0.01&q=timeout&service=api&status=error&tok_min=-5",
		"service api -> /?cost_max=abc&cost_min=0.01&dur_max=10&q=timeout&status=error&tok_min=-5",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("chips:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
