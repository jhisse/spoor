package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// A real Claude Code turn (several model calls, a session) beside a
// one-call trace without a session: the list draws the turn's context per
// call with the counts the trace page gives it, puts its session in the
// band, and leaves the one-call trace with its token bar alone.
func TestListShowsContextPerCallAndLatestSessions(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "claude-code-interactive-tools.pb")
	turn, spans := onlyTrace(t, s)
	ingestCapture(t, s, "openllmetry-anthropic-simple.pb")

	prices, err := s.ListModelPrices(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	page := buildContextChart(genSteps(spans), prices, "").Spark // what the trace page says
	if page.Calls < 2 || page.Peak == 0 {
		t.Fatalf("the capture should have several model calls with tokens: %+v", page)
	}
	body := doList(t, h, "").Body.String()
	for _, want := range []string{
		fmt.Sprintf(`title="%d model calls, the largest %s tokens.`, page.Calls, fmtCount(page.Peak)),
		"Context <small>· per model call</small>", // each row draws on its own scale
		"Latest sessions", `href="/sessions?session=` + *turn.SessionID + `"`,
		fmt.Sprintf("1 turn over %s · %d model calls, the largest %s tokens", fmtCoarse(turn.EndedAt.Sub(turn.StartedAt)), page.Calls, fmtCount(page.Peak)),
		`<details class="disc grow"><summary><h3>Traces over time`, // closed
	} {
		if !strings.Contains(body, want) {
			t.Errorf("list is missing %q", want)
		}
	}
	if n := strings.Count(body, `<td class="ctxcol"><span`); n != 1 {
		t.Errorf("%d rows have a context drawing, want only the multi-call trace", n)
	}
	if got := strings.Count(body, ">C</b>"); (got > 0) != (page.Shrank > 0) {
		t.Errorf("list marks a smaller context %d times, the trace page counts %d", got, page.Shrank)
	}

	// The live poll carries the band; a search or a later page is not "lately".
	for query, want := range map[string]bool{"live=1": true, "status=unset": false, "limit=1": true, "q=claude": false} {
		if got := strings.Contains(doList(t, h, query).Body.String(), "Latest sessions"); got != want {
			t.Errorf("?%s: sessions band shown = %v, want %v", query, got, want)
		}
	}
	// A period opens the chart and the filters that hold it.
	from := doList(t, h, "from=2020-01-01T00:00").Body.String()
	if !strings.Contains(from, `<details class="disc grow" open>`) || !strings.Contains(from, `<details class="disc hint" open>`) {
		t.Error("a period filter should open the chart and More filters")
	}
}

// A call that loses its predecessor's cache on a grown prompt: the row says
// so, underlines the bar and prices it as the trace page does, labelled as
// an estimate. A break on a model without a price is marked without a figure.
func TestListMarksACacheBreakWithItsEstimatedCost(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now().UTC()
	for _, c := range []struct{ id, model string }{{"priced", "claude-sonnet-5"}, {"unpriced", "no-such-model"}} {
		seedTrace(t, s, c.id, now)
		var spans []store.Span
		for i, v := range [][3]int64{{90_000, 88_000, 2_000}, {95_000, 0, 95_000}} {
			sp := gen(c.id, fmt.Sprintf("g%d", i), nil, now.Add(time.Duration(i)*time.Second), nil)
			sp.Model, sp.InputTokens, sp.CacheReadTokens, sp.CacheWriteTokens, sp.OutputTokens = ptr(c.model), &v[0], &v[1], &v[2], i64Ptr(10)
			spans = append(spans, sp)
		}
		insertSpans(t, s, spans...)
	}
	prices, _ := s.ListModelPrices(t.Context())
	_, spans, _ := s.GetTraceByID(t.Context(), "priced")
	page := buildContextChart(genSteps(spans), prices, "").Spark
	if page.Breaks != 1 || page.Extra <= 0 {
		t.Fatalf("the trace page should count one priced break: %+v", page)
	}
	body := doList(t, h, "").Body.String()
	if n := strings.Count(body, `class="f-warn"`); n != 2 {
		t.Errorf("%d underlined bars, want one per trace", n)
	}
	if n := strings.Count(body, "+"+usd4(page.Extra)+" est."); n != 1 {
		t.Errorf("the trace page's estimate %s should be on the priced row only, found %d times", usd4(page.Extra), n)
	}
	for _, want := range []string{"Step 2: cache break", "No price for the model; cost not estimated.", "<b class=\"warn-t\">B</b> cache break"} {
		if !strings.Contains(body, want) {
			t.Errorf("list is missing %q", want)
		}
	}
}
