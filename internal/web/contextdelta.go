package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"net/url"
	"sort"

	"github.com/jhisse/spoor/internal/store"
)

// genStep is one generation of a trace, in start order, paired with the
// generation it is compared against wherever context is shown: the preceding
// generation of the same model under the same nearest agent ancestor. A sub-agent's first call
// is never compared with its parent agent's last one: that is a different
// context, not a rewritten one.
type genStep struct {
	Span      store.Span
	Prev      *store.Span // nil for the first generation of its scope
	Scope     string      // nearest agent ancestor's span ID, "" when none
	ScopeName string
	// What the step did to its context: "C" a smaller prompt, "B" a cache
	// break, "E" a cache expiry. Break is the break's sentence and Extra its
	// estimated extra input cost, 0 without a price. markSteps sets them on
	// a trace, sessionSteps on a session.
	Mark, Break  string
	Extra        float64
	Label, Pause string // the session chart's turn label and pause
}

func genSteps(spans []store.Span) []genStep {
	byID := make(map[string]store.Span, len(spans))
	for _, sp := range spans {
		byID[sp.ID] = sp
	}
	var steps []genStep
	for _, sp := range spans {
		if sp.Kind != store.SpanKindGeneration {
			continue
		}
		st := genStep{Span: sp}
		if a := nearestAgent(byID, sp); a != nil {
			st.Scope, st.ScopeName = a.ID, a.Name
		}
		steps = append(steps, st)
	}
	sort.SliceStable(steps, func(i, j int) bool { return steps[i].Span.StartedAt.Before(steps[j].Span.StartedAt) })
	// A call of another model is another context (a coding agent's small
	// classifier call next to its main one), never a rewritten one.
	last := map[string]int{}
	for i := range steps {
		context := steps[i].Scope + "\x00" + store.Deref(steps[i].Span.Model)
		if j, ok := last[context]; ok {
			steps[i].Prev = &steps[j].Span
		}
		last[context] = i
	}
	return steps
}

// nearestAgent walks sp's parent chain. The step cap is for a payload whose
// parent ids form a cycle.
func nearestAgent(byID map[string]store.Span, sp store.Span) *store.Span {
	for i := 0; i < len(byID) && sp.ParentSpanID != nil; i++ {
		p, ok := byID[*sp.ParentSpanID]
		if !ok {
			return nil
		}
		if p.Kind == store.SpanKindAgent {
			return &p
		}
		sp = p
	}
	return nil
}

// contextDelta is a generation's prompt split into the prefix it shares with
// the previous generation's prompt (Kept, rendered collapsed) and what is
// new. NoBasis non-empty means there is nothing honest to compare against:
// the template then shows the whole prompt, unmarked.
type contextDelta struct {
	PrevURL     string // the generation compared against; "" when none
	TokenLine   string // input tokens and their change since the previous generation: measured, not estimated
	NoBasis     string
	Kept        []chatMessage
	KeptLabel   string
	RewrittenAt int // 1-based index of the first message that differs from the previous prompt; 0 when all of it was kept
	Rewrite     *rewrite
	New         []chatMessage
}

// rewrite points inside the first message the prompt no longer shares with
// the previous generation's: the text around the change and the change
// itself, old and new. A cache can only be reused up to this character.
type rewrite struct {
	Sentence            string
	Text                bool // the two messages differ in their text; false when only outside it
	Pre, Was, Now, Post string
}

// rewriteWindow is the text kept on each side of the change, rewriteMax the
// most of the change itself shown; the sentence has the exact counts.
const rewriteWindow, rewriteMax = 80, 300

func buildRewrite(at int, prev, cur chatMessage) *rewrite {
	where := fmt.Sprintf("Message %d (%s)", at, cur.Role)
	if prev.Role != cur.Role {
		return &rewrite{Sentence: fmt.Sprintf("%s was %s in the previous generation.", where, prev.Role)}
	}
	if prev.Content == cur.Content {
		return &rewrite{Sentence: where + " has the same text; what differs is outside it (tool calls, results or other fields)."}
	}
	a, b := []rune(prev.Content), []rune(cur.Content)
	p := 0
	for p < len(a) && p < len(b) && a[p] == b[p] {
		p++
	}
	s := 0
	for s < len(a)-p && s < len(b)-p && a[len(a)-1-s] == b[len(b)-1-s] {
		s++
	}
	was, now := a[p:len(a)-s], b[p:len(b)-s]
	r := &rewrite{Text: true, Pre: clipTail(a[:p]), Was: clipHead(was, rewriteMax), Now: clipHead(now, rewriteMax), Post: clipHead(b[len(b)-s:], rewriteWindow)}
	switch {
	case len(was) == 0:
		r.Sentence = fmt.Sprintf("%s grew by %s characters at character %s.", where, commas(int64(len(now))), commas(int64(p+1)))
	case len(now) == 0:
		r.Sentence = fmt.Sprintf("%s lost %s characters at character %s.", where, commas(int64(len(was))), commas(int64(p+1)))
	default:
		r.Sentence = fmt.Sprintf("%s changed at character %s: %s characters became %s.", where, commas(int64(p+1)), commas(int64(len(was))), commas(int64(len(now))))
	}
	return r
}

func clipHead(r []rune, n int) string {
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return string(r)
}

func clipTail(r []rune) string {
	if len(r) > rewriteWindow {
		return "…" + string(r[len(r)-rewriteWindow:])
	}
	return string(r)
}

func buildContextDelta(prev *store.Span, cur store.Span) contextDelta {
	var d contextDelta
	if cur.InputTokens != nil {
		d.TokenLine = fmtCount(*cur.InputTokens) + " input tokens"
	}
	if prev == nil {
		d.NoBasis = "first generation, there is no previous call"
		return d
	}
	d.PrevURL = spanURL(*prev)
	if cur.InputTokens != nil && prev.InputTokens != nil {
		diff := *cur.InputTokens - *prev.InputTokens
		d.TokenLine += fmt.Sprintf(" (%s%s vs the previous generation)", map[bool]string{true: "+"}[diff >= 0], commas(diff))
	}
	msgs := parseMessages(cur.Input)
	curRaw, prevRaw := rawMessages(cur.Input), rawMessages(prev.Input)
	k := commonPrefix(prevRaw, curRaw)
	switch {
	case len(msgs) == 0:
		d.NoBasis = "this generation's prompt was not recorded as a list of messages"
	case len(prevRaw) == 0:
		d.NoBasis = "the previous generation has no recorded prompt"
	case k == 0:
		d.NoBasis = "no leading message in common with the previous generation"
	}
	if d.NoBasis != "" {
		return d
	}
	d.Kept, d.New = msgs[:k], msgs[k:]
	for i := range d.New {
		d.New[i].New = true
	}
	if k < len(prevRaw) {
		d.RewrittenAt = k + 1
		// A prompt whose entries are not all objects decodes as raw messages but not as chat messages.
		if prevMsgs := parseMessages(prev.Input); k < len(prevMsgs) {
			d.Rewrite = buildRewrite(k+1, prevMsgs[k], msgs[k])
		}
	}
	var keptChars, allChars int64
	for i, m := range curRaw {
		allChars += int64(len(m))
		if i < k {
			keptChars += int64(len(m))
		}
	}
	d.KeptLabel = fmt.Sprintf("unchanged earlier messages: %d", k)
	if cur.InputTokens != nil {
		d.KeptLabel += " · ≈ " + fmtCount(*cur.InputTokens*keptChars/allChars) + " tokens (estimated)"
	} else {
		d.KeptLabel += " · " + fmtCount(keptChars) + " characters"
	}
	return d
}

func spanURL(sp store.Span) string {
	return "/traces/" + url.PathEscape(sp.TraceID) + "?span=" + url.QueryEscape(sp.ID)
}

// rawMessages keeps each stored message as its exact JSON bytes: two
// messages are "the same" only when role and content (and anything else
// the SDK recorded) are byte-identical.
func rawMessages(raw *string) []json.RawMessage {
	if raw == nil {
		return nil
	}
	var out []json.RawMessage
	if json.Unmarshal([]byte(*raw), &out) != nil {
		return nil
	}
	return out
}

func commonPrefix(a, b []json.RawMessage) int {
	k := 0
	for k < len(a) && k < len(b) && bytes.Equal(a[k], b[k]) {
		k++
	}
	return k
}

// fmtCount abbreviates a token or character count: 950, 91.8k, 1.17M.
func fmtCount(n int64) string {
	switch {
	case n < 1000 && n > -1000:
		return fmt.Sprint(n)
	case n < 1_000_000 && n > -1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	default:
		return fmt.Sprintf("%.2fM", float64(n)/1e6)
	}
}

// tokenPair is "input in · output out", abbreviated, the exact figures in a
// title. A nil output is "?": not reported is unknown, not zero.
func tokenPair(in int64, out *int64) template.HTML {
	exact, short := "output not reported by the SDK", "?"
	if out != nil {
		exact, short = commas(*out)+" out", fmtCount(*out)
	} else if in == 0 {
		return "–"
	}
	return template.HTML(fmt.Sprintf(`<span title="%s in · %s">%s <small>in</small> · %s <small>out</small></span>`, commas(in), exact, fmtCount(in), short)) // #nosec G203 -- integers and fixed strings only
}

// tokenSum is tokenPair for a SQL aggregate, which sums a missing count as 0.
// An output sum of 0 is read as not reported; make the aggregate
// nullable in Store if a call that truly produced no token ever matters.
func tokenSum(in, out int64) template.HTML {
	if out == 0 {
		return tokenPair(in, nil)
	}
	return tokenPair(in, &out)
}

// contextViews builds the trace's chart and, when the selected span is a
// generation, its delta. The first generation of a trace in a session is
// compared with the last generation of the session's previous trace under
// an agent of the same name and of the same model: two Store calls, made
// only in that case.
func (h *Handlers) contextViews(ctx context.Context, trace store.Trace, spans []store.Span, selectedID string) (*contextChart, *contextDelta, error) {
	steps := genSteps(spans)
	var chart *contextChart
	if len(steps) >= 2 {
		prices, err := h.Store.ListModelPrices(ctx)
		if err != nil {
			return nil, nil, err
		}
		chart = buildContextChart(steps, prices, selectedID)
	}
	for i, st := range steps {
		if st.Span.ID != selectedID {
			continue
		}
		prev := st.Prev
		if i == 0 && trace.SessionID != nil {
			var err error
			if prev, err = h.lastGenerationBefore(ctx, trace, st.ScopeName, store.Deref(st.Span.Model)); err != nil {
				return nil, nil, err
			}
		}
		d := buildContextDelta(prev, st.Span)
		return chart, &d, nil
	}
	return chart, nil, nil
}

// lastGenerationBefore is the last generation of the session's previous
// turn in the same context as a call of scopeName and model: a call of
// another model is another context (genSteps), so a turn that ended on a
// classifier does not stand for the main model's last prompt. Nil when the
// previous turn has no such call.
func (h *Handlers) lastGenerationBefore(ctx context.Context, trace store.Trace, scopeName, model string) (*store.Span, error) {
	_, turns, err := h.Store.GetSession(ctx, *trace.SessionID)
	if err != nil {
		return nil, err
	}
	for i, turn := range turns {
		if turn.ID != trace.ID || i == 0 {
			continue
		}
		_, spans, err := h.Store.GetTraceByID(ctx, turns[i-1].ID)
		if err != nil {
			return nil, err
		}
		steps := genSteps(spans)
		for j := len(steps) - 1; j >= 0; j-- {
			if steps[j].ScopeName == scopeName && store.Deref(steps[j].Span.Model) == model {
				return &steps[j].Span, nil
			}
		}
	}
	return nil, nil
}
