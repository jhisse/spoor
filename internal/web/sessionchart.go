package web

import (
	"fmt"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

const (
	// The shortest common prompt-cache lifetime: that long without a model call is labelled, a cache lost across it is an expiry.
	pauseThreshold = 5 * time.Minute
	// A longer session shows its first calls only, and says so.
	sessionChartMax = 400
)

func inputOf(sp store.Span) int64 { return sp.Mix().Total() - sp.Mix().Output }

// sessionContexts is the last call of every context seen so far. Spans do not say which context a call
// belongs to (an agent may emit a sub-agent's calls as siblings of its own), so it is read from the tokens.
type sessionContexts struct {
	tails []store.Span
	steps []int // each tail's 1-based step number
}

// find returns the context sp continues, -1 when none. read means sp read at least half of that
// context's last prompt from cache, which only a continuation can do; the closest such prompt wins.
// Otherwise it is an assumption: the largest earlier prompt of the same model not larger than sp's.
func (c *sessionContexts) find(sp store.Span) (k int, read, sameModel bool) {
	in, cacheRead := inputOf(sp), sp.Mix().CacheRead
	k = -1
	for i, t := range c.tails {
		if store.Deref(t.Model) != store.Deref(sp.Model) {
			continue
		}
		sameModel = true
		tin := inputOf(t)
		if tin > in {
			continue
		}
		hit := cacheRead <= tin && cacheRead*2 >= tin
		if hit && (!read || tin < inputOf(c.tails[k])) {
			k, read = i, true
		} else if !hit && !read && (k < 0 || tin > inputOf(c.tails[k])) {
			k = i
		}
	}
	return k, read, sameModel
}

// place files step st, the session's nth, under its context and sets its mark and, for a lost cache,
// its sentence and estimated cost. A call that read its context from cache is never a break.
func (c *sessionContexts) place(n int, st *genStep, prices []store.ModelPrice) {
	sp := st.Span
	if inputOf(sp) == 0 {
		return
	}
	k, read, sameModel := c.find(sp)
	if k < 0 {
		c.tails, c.steps = append(c.tails, sp), append(c.steps, n)
		if sameModel {
			st.Mark = "C"
		}
		return
	}
	if b, isBreak := detectCacheBreak(c.tails[k].Mix(), sp.Mix()); isBreak && !read {
		st.Mark = "B"
		st.Break, st.Extra = b.sentence(n, prices, sp.Model)
		if gap := sp.StartedAt.Sub(c.tails[k].EndedAt); gap >= pauseThreshold {
			st.Mark = "E"
			st.Break += fmt.Sprintf(" That context's previous call ended %s earlier: a cache expiry.", fmtCoarse(gap))
		}
		st.Break += fmt.Sprintf(" Assumed to continue step %d; a new sub-agent with a larger prompt would look the same.", c.steps[k])
	}
	c.tails[k], c.steps[k] = sp, n
}

// sessionSteps reads a session's generations (oldest first) as steps: each
// one's context mark, its turn (turn numbers a trace id) and the pause
// before it, with a note per pause.
func sessionSteps(turn map[string]int, gens []store.Span, prices []store.ModelPrice) (steps []genStep, notes []string) {
	steps = make([]genStep, len(gens))
	var ctxs sessionContexts
	var lastEnd time.Time
	var lastTrace string
	for i, sp := range gens {
		st := genStep{Span: sp, Scope: sp.TraceID, ScopeName: fmt.Sprintf("turn %d", turn[sp.TraceID])}
		if sp.TraceID != lastTrace {
			st.Label, lastTrace = fmt.Sprintf("T%d", turn[sp.TraceID]), sp.TraceID
		}
		if gap := sp.StartedAt.Sub(lastEnd); i > 0 && gap >= pauseThreshold {
			st.Pause = fmtCoarse(gap)
			note := fmt.Sprintf("Step %d: no model call for %s before it", i+1, st.Pause)
			if in := inputOf(sp); in > 0 && sp.CacheReadTokens != nil {
				note += fmt.Sprintf("; it read %.0f%% of its input from cache", float64(*sp.CacheReadTokens)/float64(in)*100)
			}
			notes = append(notes, note+".")
		}
		if sp.EndedAt.After(lastEnd) {
			lastEnd = sp.EndedAt
		}
		ctxs.place(i+1, &st, prices)
		steps[i] = st
	}
	return steps, notes
}

// buildSessionChart is the context chart over every generation of a session
// (gens, oldest first), with turn boundaries and pauses between model calls.
func buildSessionChart(traces []store.TraceSummary, gens []store.Span, prices []store.ModelPrice) *contextChart {
	turn := make(map[string]int, len(traces))
	for i, t := range traces {
		turn[t.ID] = i + 1
	}
	total := len(gens)
	steps, notes := sessionSteps(turn, gens[:min(total, sessionChartMax)], prices)
	c := buildContextChart(steps, prices, "")
	if total < 2 || c.Tokens == "" { // one call, or no call reports tokens: nothing to draw
		return nil
	}
	if total > len(steps) {
		notes = append(notes, fmt.Sprintf("Showing the first %d of %d generations.", len(steps), total))
	}
	c.Notes = append(notes, c.Notes...)
	return c
}
