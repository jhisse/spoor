package web

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// cacheLoss is every step of a run of model calls that lost its cache in
// one way, an expiry or a break: one line for the reader, the step-by-step
// sentences behind it.
type cacheLoss struct {
	Mark      string // "E" or "B"
	Steps     []int
	Extra     float64 // the steps with a price; estimated
	Unpriced  int     // the steps whose model has no price, not in Extra
	Sentences []string
	SpanID    string // the first step's span, where a pointer lands
	Name      string
	FirstCost float64 // the first step's Extra
}

var lossWords = map[string][2]string{"E": {"cache expiry", "cache expiries"}, "B": {"cache break", "cache breaks"}}

// Summary is the line: how many, where, and what they are estimated to
// have added.
func (l cacheLoss) Summary() string {
	n, words, label := len(l.Steps), lossWords[l.Mark], "step "
	what := words[0]
	if n > 1 {
		what, label = words[1], "steps "
	}
	steps := make([]string, n)
	for i, s := range l.Steps {
		steps[i] = strconv.Itoa(s)
	}
	t := fmt.Sprintf("%d %s · %s%s", n, what, label, strings.Join(steps, ", "))
	if l.Extra > 0 {
		t += " · +" + usd4(l.Extra) + " est."
	}
	if l.Unpriced > 0 {
		t += fmt.Sprintf(" · %d without a price", l.Unpriced)
	}
	return t
}

// groupLosses gathers the steps that lost their cache, expiries first: the
// mark the session chart gives a break after a pause, then every other
// break. A step that also shrank (CB) is a break.
func groupLosses(steps []genStep) []cacheLoss {
	var losses []cacheLoss
	for _, mark := range []string{"E", "B"} {
		l := cacheLoss{Mark: mark}
		for i, st := range steps {
			if st.Break == "" || strings.Contains(st.Mark, "E") != (mark == "E") {
				continue
			}
			if l.SpanID == "" {
				l.SpanID, l.Name, l.FirstCost = st.Span.ID, st.Span.Name, st.Extra
			}
			l.Steps, l.Sentences = append(l.Steps, i+1), append(l.Sentences, st.Break)
			if l.Extra += st.Extra; st.Extra == 0 {
				l.Unpriced++
			}
		}
		if len(l.Steps) > 0 {
			losses = append(losses, l)
		}
	}
	return losses
}

// cacheMix is the token mix of the calls that reported a cache count, and
// how many of the run's calls did. A call that reported none is not
// evidence of a cache miss: counting its prompt as fresh input would
// understate the share read from cache, so it is left out.
func cacheMix(steps []genStep) (sum TokenMix, reported int) {
	for _, st := range steps {
		if st.Span.CacheReadTokens == nil && st.Span.CacheWriteTokens == nil {
			continue
		}
		sum, reported = sum.Add(st.Span.Mix()), reported+1
	}
	return sum, reported
}

// readShare is the share of the input read from cache, over the calls that
// reported a cache count; "" when none did. A missing count is not a share
// of zero.
func readShare(sum TokenMix, reported int) string {
	in := sum.CacheRead + sum.Fresh + sum.CacheWrite
	if reported == 0 || in == 0 {
		return ""
	}
	return fmt.Sprintf("%.0f%%", float64(sum.CacheRead)/float64(in)*100)
}

// readNote is the coverage of a share: "" when every call reported a cache
// count, else how many did.
func readNote(reported, calls int) string {
	if reported == 0 || reported == calls {
		return ""
	}
	return fmt.Sprintf("%d of %d calls", reported, calls)
}

// CacheLost is the header's line: what lost the cache, and what it is
// estimated to have cost. "" when no call reported a cache count: without
// one spoor cannot tell a lost cache from an unread one, and "none" would
// claim what it does not know. "none" when the calls reported and none lost
// it. A smaller context is a compaction or a new sub-agent, not a lost
// cache: the chart marks it.
func (c *contextChart) CacheLost() string {
	if c.ReadCalls == 0 {
		return ""
	}
	var parts []string
	var extra float64
	for _, l := range c.Losses {
		parts = append(parts, strings.SplitN(l.Summary(), " · ", 2)[0])
		extra += l.Extra
	}
	if len(parts) == 0 {
		return "none"
	}
	if extra > 0 {
		parts = append(parts, "+"+usd4(extra)+" est.")
	}
	return strings.Join(parts, " · ")
}

// ReadNote is the coverage of ReadShare, "" when every call reported one.
func (c *contextChart) ReadNote() string { return readNote(c.ReadCalls, c.Steps) }

// lossLook is the "worth a look" pointer to the first step that lost its
// cache; ok is false when none did.
func (c *contextChart) lossLook() (lookLink, bool) {
	if c == nil || len(c.Losses) == 0 {
		return lookLink{}, false
	}
	first := c.Losses[0]
	for _, l := range c.Losses[1:] {
		if l.Steps[0] < first.Steps[0] {
			first = l
		}
	}
	value := fmt.Sprintf("step %d", first.Steps[0])
	if first.FirstCost > 0 {
		value += " · +" + usd4(first.FirstCost) + " est."
	}
	return lookLink{Label: "cache lost", SpanID: first.SpanID, Name: first.Name, Value: value}, true
}

// withLossLook puts the cache pointer after the first failure, before the
// slowest, costliest and largest steps.
func withLossLook(look []lookLink, l lookLink) []lookLink {
	at := 0
	if len(look) > 0 && look[0].Label == "first failure" {
		at = 1
	}
	return slices.Insert(look, at, l)
}
