package web

import (
	"testing"

	"github.com/jhisse/spoor/internal/store"
)

func TestReadShareIsUnknownWithoutCacheCounts(t *testing.T) {
	mix := TokenMix{CacheRead: 900, Fresh: 50, CacheWrite: 50, Output: 10}
	if got := readShare(mix, 3); got != "90%" {
		t.Errorf("readShare = %q, want 90%% (output is not input)", got)
	}
	if got := readShare(mix, 0); got != "" {
		t.Errorf("readShare without a reported cache count = %q, want unknown, not 0%%", got)
	}
}

// A call that reported no cache count is left out of the share, not counted
// as a miss: its prompt would read as fresh input and understate the share.
func TestCacheMixLeavesOutCallsThatReportedNoCacheCount(t *testing.T) {
	read, in := int64(900), int64(1000)
	steps := []genStep{
		{Span: store.Span{CacheReadTokens: &read, InputTokens: &in}},
		{Span: store.Span{InputTokens: &in}}, // no cache count: not evidence of a miss
		{Span: store.Span{CacheReadTokens: &read, InputTokens: &in}},
	}
	sum, reported := cacheMix(steps)
	if reported != 2 || sum.CacheRead != 1800 || sum.Fresh != 200 {
		t.Errorf("cacheMix = %+v over %d calls, want the two reporting calls only", sum, reported)
	}
	// Counting the middle call as a miss would read 1800/3000 = 60%.
	if got := readShare(sum, reported); got != "90%" {
		t.Errorf("readShare = %q, want 90%% over the reporting calls", got)
	}
	if got := readNote(reported, len(steps)); got != "2 of 3 calls" {
		t.Errorf("readNote = %q, want the coverage named", got)
	}
	if got := readNote(3, 3); got != "" {
		t.Errorf("readNote with every call reporting = %q, want nothing", got)
	}
}

// Without a cache count anywhere, the header says nothing about the cache:
// "none" would claim a cache spoor cannot tell was lost or never read.
func TestCacheLostIsSilentWithoutCacheCounts(t *testing.T) {
	if got := (&contextChart{Steps: 4}).CacheLost(); got != "" {
		t.Errorf("CacheLost without a reported cache count = %q, want nothing", got)
	}
	if got := (&contextChart{Steps: 4, ReadCalls: 4}).CacheLost(); got != "none" {
		t.Errorf("CacheLost with every call reporting and none losing it = %q, want none", got)
	}
}

// Expiries and breaks are one line each, expiries first; the pointer lands
// on the earliest step that lost its cache, whatever its kind.
func TestGroupLossesAndThePointerToTheFirst(t *testing.T) {
	step := func(id, mark string, extra float64) genStep {
		st := genStep{Span: store.Span{ID: id, Name: "call " + id}, Mark: mark, Extra: extra}
		if mark != "" && mark != "C" {
			st.Break = "sentence " + id
		}
		return st
	}
	steps := []genStep{step("1", "", 0), step("2", "CB", 0.25), step("3", "C", 0), step("4", "E", 0.5), step("5", "E", 0)}
	losses := groupLosses(steps)
	if len(losses) != 2 || losses[0].Mark != "E" || losses[1].Mark != "B" {
		t.Fatalf("losses = %+v, want an expiry group then a break group", losses)
	}
	if got := losses[0].Summary(); got != "2 cache expiries · steps 4, 5 · +$0.5000 est. · 1 without a price" {
		t.Errorf("expiry summary = %q", got)
	}
	if got := losses[1].Summary(); got != "1 cache break · step 2 · +$0.2500 est." {
		t.Errorf("break summary = %q", got)
	}
	c := &contextChart{Losses: losses, Steps: 5, ReadCalls: 5, Spark: contextSpark{Shrank: 2}}
	if got := c.CacheLost(); got != "2 cache expiries · 1 cache break · +$0.7500 est." {
		t.Errorf("CacheLost = %q", got)
	}
	l, ok := c.lossLook()
	if !ok || l.SpanID != "2" || l.Value != "step 2 · +$0.2500 est." {
		t.Errorf("lossLook = %+v, want the break at step 2", l)
	}
	if _, ok := (*contextChart)(nil).lossLook(); ok {
		t.Error("no chart, no pointer")
	}
}
