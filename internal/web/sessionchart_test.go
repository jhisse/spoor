package web

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// call is one generation of a session the way a caching SDK reports it.
// read/write < 0 leaves the cache columns unreported.
func call(trace string, n int, at time.Time, model string, in, read, write int64) store.Span {
	sp := gen(trace, fmt.Sprintf("c%d", n), nil, at, nil)
	sp.EndedAt = at.Add(time.Second)
	sp.Model, sp.InputTokens, sp.OutputTokens, sp.CostUSD = &model, &in, ptr(int64(100)), ptr(0.01)
	if read >= 0 {
		sp.CacheReadTokens, sp.CacheWriteTokens = &read, &write
	}
	return sp
}

func turns(ids ...string) []store.TraceSummary {
	var out []store.TraceSummary
	for _, id := range ids {
		out = append(out, store.TraceSummary{Trace: store.Trace{ID: id}})
	}
	return out
}

var markerRe = regexp.MustCompile(`>[CBE]+<`)

func marks(c *contextChart) string {
	return strings.Join(markerRe.FindAllString(string(c.Tokens), -1), "")
}

// A sub-agent starts a smaller context inside turn 2, grows past the main
// one, and turn 3 resumes the main context from cache. Nothing here is a
// cache break.
func TestSessionChartSubAgentIsNotABreak(t *testing.T) {
	t0 := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }
	gens := []store.Span{
		call("a", 1, at(0), "m", 87_000, 86_000, 1_000),
		call("b", 2, at(10), "m", 91_000, 87_000, 4_000),
		call("b", 3, at(20), "m", 26_000, 0, 26_000),      // sub-agent's first call
		call("b", 4, at(30), "m", 96_000, 26_000, 70_000), // …which read its 26k and outgrew the main context
		call("b", 5, at(40), "m", 113_000, 96_000, 17_000),
		call("c", 6, at(50), "m", 93_000, 91_000, 2_000), // main context resumed, read from cache
	}
	c := buildSessionChart(turns("a", "b", "c"), gens, sonnet)
	if got := marks(c); got != ">C<" {
		t.Errorf("markers = %q, want only the smaller-context C on step 3", got)
	}
	if len(c.Breaks) != 0 {
		t.Errorf("a sub-agent and a resumed context are not cache breaks: %v", c.Breaks)
	}
	tokens := string(c.Tokens)
	for _, want := range []string{">T1<", ">T2<", ">T3<", "turn 2", "$0.0600"} {
		if !strings.Contains(tokens, want) {
			t.Errorf("chart missing %q: %s", want, tokens)
		}
	}
	if strings.Contains(tokens, "⋯") {
		t.Error("no pause of 5 minutes: nothing to label")
	}
}

func TestSessionChartExpiryBreakAndSurvivedPause(t *testing.T) {
	t0 := time.Date(2026, 10, 3, 4, 0, 0, 0, time.UTC)
	gens := []store.Span{
		call("a", 1, t0, "claude-sonnet-5", 90_000, 88_000, 2_000),
		call("b", 2, t0.Add(48*time.Minute), "claude-sonnet-5", 92_000, 90_000, 2_000),             // long pause, cache survived
		call("c", 3, t0.Add(2*time.Hour), "claude-sonnet-5", 94_000, 0, 94_000),                    // long pause, cache gone
		call("c", 4, t0.Add(2*time.Hour+10*time.Second), "claude-sonnet-5", 96_000, 94_000, 2_000), // back to normal
		call("c", 5, t0.Add(2*time.Hour+20*time.Second), "claude-sonnet-5", 98_000, 0, 98_000),     // lost with no pause
		call("c", 6, t0.Add(2*time.Hour+30*time.Second), "other", 5_000, 0, 5_000),                 // a model seen for the first time
	}
	c := buildSessionChart(turns("a", "b", "c"), gens, sonnet)
	if got := marks(c); got != ">E<>B<" {
		t.Errorf("markers = %q, want E on step 3 and B on step 5", got)
	}
	if len(c.Breaks) != 2 || !strings.Contains(c.Breaks[0], "Step 3") || !strings.Contains(c.Breaks[0], "1 h 12 min earlier: a cache expiry") ||
		!strings.Contains(c.Breaks[0], "Assumed to continue step 2") || strings.Contains(c.Breaks[1], "expiry") {
		t.Errorf("Breaks = %q", c.Breaks)
	}
	// 94k written at 1.25× instead of read at 0.1×, at $3/M.
	if !strings.Contains(c.Breaks[0], "$0.3525") {
		t.Errorf("expiry sentence should carry the input cost: %s", c.Breaks[0])
	}
	notes := strings.Join(c.Notes, "\n")
	if !strings.Contains(notes, "Step 2: no model call for 48 min before it; it read 98% of its input from cache.") ||
		!strings.Contains(notes, "Step 3: no model call for 1 h 12 min before it; it read 0% of its input from cache.") {
		t.Errorf("Notes = %q", c.Notes)
	}
	if strings.Count(string(c.Tokens), "⋯") != 4 {
		t.Errorf("want two labelled pauses in the chart: %s", c.Tokens)
	}
}

func TestSessionChartDegrades(t *testing.T) {
	t0 := time.Now().UTC()
	// No cache columns: growth and a smaller context still show; a break cannot.
	gens := []store.Span{call("a", 1, t0, "m", 50_000, -1, 0), call("a", 2, t0.Add(time.Hour), "m", 60_000, -1, 0), call("b", 3, t0.Add(2*time.Hour), "m", 9_000, -1, 0)}
	gens[2].CostUSD = nil
	c := buildSessionChart(turns("a", "b"), gens, nil)
	if got := marks(c); got != ">C<" || len(c.Breaks) != 0 || c.Dollars != "" || strings.Contains(string(c.Tokens), "<polyline") {
		t.Errorf("markers %q breaks %v: want C only, no dollar band, no cost line", got, c.Breaks)
	}
	notes := strings.Join(c.Notes, "\n")
	for _, want := range []string{"Step 2: no model call for 1 h 00 min before it.", "no stored cost", "no known price", "no cache tokens"} {
		if !strings.Contains(notes, want) {
			t.Errorf("Notes missing %q: %q", want, c.Notes)
		}
	}
	if buildSessionChart(turns("a"), gens[:1], nil) != nil {
		t.Error("one generation is not a chart")
	}
	many := make([]store.Span, sessionChartMax+5)
	for i := range many {
		many[i] = call("a", i, t0.Add(time.Duration(i)*time.Second), "m", int64(1000+i), -1, 0)
	}
	c = buildSessionChart(turns("a"), many, nil)
	if c.Steps != sessionChartMax || !strings.Contains(strings.Join(c.Notes, "\n"), "Showing the first 400 of 405 generations.") {
		t.Errorf("Steps = %d, Notes = %q", c.Steps, c.Notes)
	}
}

func TestSessionsRendersSessionChart(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	base := time.Now().UTC().Add(-2 * time.Hour)
	seedSessionTurn(t, s, "turn-a", "conv-1", base)
	seedSessionTurn(t, s, "turn-b", "conv-1", base.Add(time.Hour))
	if body := doSessions(t, h, "session=conv-1").Body.String(); strings.Contains(body, "Inside the turns") {
		t.Error("generations without token counts: there is no chart to draw")
	}
	for i, tr := range []string{"turn-a", "turn-b"} {
		read := int64(88_000 * (1 - i)) // turn-b, an hour later, reads nothing from cache
		sp := call(tr, i, base.Add(time.Duration(i)*time.Hour+2*time.Second), "m", 90_000, read, 90_000-read)
		if err := s.InsertSpan(t.Context(), sp); err != nil {
			t.Fatal(err)
		}
	}
	body := doSessions(t, h, "session=conv-1").Body.String()
	for _, want := range []string{"Inside the turns", ">T2<", "⋯ 1 h 00 min ⋯", ">E<", "a cache expiry", "/traces/turn-b?span=c1", "the spans do not say which"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}
