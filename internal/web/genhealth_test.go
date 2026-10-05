package web

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func TestGenHealth(t *testing.T) {
	in := ptr(`[{"role":"user","content":"hi"}]`)
	labels := func(sp store.Span) string { return healthGlyph(sp) }
	out := func(input *string, output string) store.Span {
		sp := gen("t", "g", nil, time.Now(), input)
		sp.Output = &output
		return sp
	}
	cases := map[string]struct {
		span store.Span
		want string
	}{
		"normal stop":            {out(in, `[{"role":"assistant","content":"ok","finish_reason":"stop"}]`), ""},
		"openai length":          {out(in, `[{"role":"assistant","content":"cut","finish_reason":"length"}]`), "truncated"},
		"anthropic max_tokens":   {out(in, `[{"role":"assistant","content":"cut","finish_reason":"max_tokens"}]`), "truncated"},
		"content filter":         {out(in, `[{"role":"assistant","content":"x","finish_reason":"content_filter"}]`), "filtered"},
		"refusal":                {out(in, `[{"role":"assistant","content":"x","finish_reason":"refusal"}]`), "filtered"},
		"empty":                  {out(in, `[{"role":"assistant","finish_reason":"stop"}]`), "empty"},
		"tool call is not empty": {out(in, `[{"role":"assistant","tool_calls":[{"name":"f","arguments":"{}"}]}]`), ""},
		"content not captured":   {out(nil, `[{"role":"assistant","finish_reason":"stop"}]`), ""},
		"unparsed output":        {out(in, `"plain string"`), ""},
	}
	for name, tc := range cases {
		if got := labels(tc.span); got != tc.want {
			t.Errorf("%s: badges = %q, want %q", name, got, tc.want)
		}
	}
	notGen := out(in, `[{"role":"assistant","finish_reason":"length"}]`)
	notGen.Kind = store.SpanKindTool
	if got := labels(notGen); got != "" {
		t.Errorf("a tool span has no generation health, got %q", got)
	}
}

// The Gemini capture stopped on its token limit (finish_reason "length");
// no other capture may raise a badge.
func TestGenHealthOnRealCaptures(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/*.pb")
	for _, f := range files {
		s := newTestStore(t)
		ingestCapture(t, s, filepath.Base(f))
		_, spans := onlyTrace(t, s)
		for _, sp := range spans {
			want := ""
			if strings.Contains(f, "gemini-simple") && sp.Kind == store.SpanKindGeneration {
				want = "truncated"
			}
			if got := healthGlyph(sp); got != want {
				t.Errorf("%s %s: badges = %q, want %q", filepath.Base(f), sp.Name, got, want)
			}
		}
	}
}

func TestDetailHealthBadgeOnRealTruncatedCapture(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "openllmetry-openrouter-gemini-simple.pb")
	tr, _ := onlyTrace(t, s)
	body := doDetail(t, h, tr.ID).Body.String()
	for _, want := range []string{"</svg> truncated</span>", `title="finish_reason: length"`, "pointers worth a look, not verdicts", `</svg>truncated</span>`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
