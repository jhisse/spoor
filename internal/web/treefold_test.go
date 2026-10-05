package web

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// agentTrace is a long agent trace: a turn with a few model calls and
// several subagents, each a long run of tool calls. Sub 1 has one failing
// Bash in the middle of its run.
func agentTrace() []store.Span {
	t0 := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	var spans []store.Span
	at := 0
	add := func(id string, parent *string, kind store.SpanKind, name string, dur int) {
		start := t0.Add(time.Duration(at) * time.Second)
		spans = append(spans, store.Span{ID: id, ParentSpanID: parent, Kind: kind, Name: name,
			StartedAt: start, EndedAt: start.Add(time.Duration(dur) * time.Second), Status: store.StatusOK})
	}
	add("turn", nil, store.SpanKindAgent, "agent.turn", 100)
	add("llm", strPtr("turn"), store.SpanKindGeneration, "llm.call", 2)
	spans[1].InputTokens, spans[1].OutputTokens, spans[1].CostUSD = i64Ptr(900), i64Ptr(100), f64Ptr(0.5)
	at = 2
	for s := range 3 {
		sub := fmt.Sprintf("sub%d", s)
		add(sub, strPtr("turn"), store.SpanKindAgent, "subagent", 20)
		for i := range 10 {
			add(fmt.Sprintf("%s-bash%d", sub, i), strPtr(sub), store.SpanKindTool, "tool.Bash", 1)
			at++
		}
		at += 10
	}
	for i := range spans {
		if spans[i].ID == "sub1-bash4" {
			spans[i].Status = store.StatusError
		}
	}
	return spans
}

func foldFor(spans []store.Span, selected string, fold bool) ([]treeRow, foldStats) {
	return foldTree(spans, BuildSpanTree(spans), treeRow{TraceID: "t", SelectedID: selected}, fold)
}

// visibleIDs walks the rows the way the template shows them before any
// <details> is opened: a ×n row counts as "name×n" and hides its members.
func visibleIDs(rows []treeRow) []string {
	var ids []string
	for _, r := range rows {
		if r.Run != nil {
			ids = append(ids, fmt.Sprintf("%s×%d", r.Node.Span.Name, len(r.Run)))
			continue
		}
		ids = append(ids, r.Node.Span.ID)
		if r.Open {
			ids = append(ids, visibleIDs(r.Items)...)
		}
	}
	return ids
}

func TestFoldTreeKeepsErrorAndSelectionPathOpen(t *testing.T) {
	spans := agentTrace() // 35 spans
	rows, stats := foldFor(spans, "sub2-bash7", true)

	got := strings.Join(visibleIDs(rows), " ")
	want := "turn llm sub0 " +
		"sub1 tool.Bash×4 sub1-bash4 tool.Bash×5 " + // the error breaks the run and stays visible
		"sub2 tool.Bash×7 sub2-bash7 sub2-bash8 sub2-bash9" // so does the selected span; two left over is no run
	if got != want {
		t.Errorf("visible rows:\n got %s\nwant %s", got, want)
	}
	// sub0 is collapsed (10 below it); 4+5+7 spans sit in three ×n rows.
	if stats.Total != 35 || stats.Collapsed != 10 || stats.Folded != 16 || stats.Runs != 3 || stats.Shown() != 9 {
		t.Errorf("stats = %+v (shown %d)", stats, stats.Shown())
	}
	sub0 := rows[0].Items[1]
	if sub0.Open || sub0.Below != 10 {
		t.Errorf("sub0: open=%v below=%d, want collapsed with 10 below", sub0.Open, sub0.Below)
	}
}

// Every span is either shown, below a collapsed row, or in a ×n row —
// whichever span is selected. The count the page states must close.
func TestFoldTreeHiddenCountAlwaysCloses(t *testing.T) {
	spans := agentTrace()
	for _, sel := range []string{"", "turn", "sub0", "sub0-bash0", "sub1-bash4", "nope"} {
		rows, stats := foldFor(spans, sel, true)
		shown := 0
		for _, id := range visibleIDs(rows) {
			if !strings.Contains(id, "×") {
				shown++
			}
		}
		if shown != stats.Shown() || stats.Shown()+stats.Collapsed+stats.Folded != len(spans) {
			t.Errorf("selected %q: %d rows visible, stats %+v", sel, shown, stats)
		}
		if sel != "" && sel != "nope" && !strings.Contains(" "+strings.Join(visibleIDs(rows), " ")+" ", " "+sel+" ") {
			t.Errorf("selected %q is not visible", sel)
		}
	}
}

func TestFoldTreeOffShowsEverything(t *testing.T) {
	spans := agentTrace()
	rows, stats := foldFor(spans, "turn", false)
	if n := len(visibleIDs(rows)); n != len(spans) || stats.Shown() != len(spans) || stats.Runs != 0 {
		t.Errorf("unfolded: %d visible, stats %+v, want all %d", n, stats, len(spans))
	}
}

func TestRunNodeSumsItsMembers(t *testing.T) {
	now := time.Now()
	gen := func(id string, at int) store.Span {
		return store.Span{ID: id, ParentSpanID: strPtr("root"), Kind: store.SpanKindGeneration, Name: "llm.call",
			StartedAt: now.Add(time.Duration(at) * time.Second), EndedAt: now.Add(time.Duration(at+1) * time.Second),
			InputTokens: i64Ptr(10), OutputTokens: i64Ptr(5), CostUSD: f64Ptr(0.01)}
	}
	spans := []store.Span{{ID: "root", Kind: store.SpanKindAgent, StartedAt: now, EndedAt: now.Add(10 * time.Second)},
		gen("a", 1), gen("b", 3), gen("c", 5)}
	rows, _ := foldFor(spans, "root", true)
	run := rows[0].Items[0]
	if len(run.Run) != 3 {
		t.Fatalf("want one ×3 row, got %v", visibleIDs(rows))
	}
	n := run.Node
	if n.Tokens.Total() != 45 || n.Cost == nil || math.Abs(*n.Cost-0.03) > 1e-9 || n.Span.EndedAt.Sub(n.Span.StartedAt) != 5*time.Second {
		t.Errorf("run node = tokens %d cost %v duration %s, want 45, 0.03, 5s", n.Tokens.Total(), n.Cost, n.Span.EndedAt.Sub(n.Span.StartedAt))
	}
}

func TestDetailFoldsLongTraceAndSaysSo(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "long", time.Now())
	for _, sp := range agentTrace() {
		sp.TraceID = tr.ID
		if err := s.InsertSpan(t.Context(), sp); err != nil {
			t.Fatal(err)
		}
	}

	body := doDetail(t, h, tr.ID).Body.String() // default selection: the first failure, sub1-bash4
	for _, want := range []string{
		"Showing 6 of 35 spans.", "20 are below collapsed rows", "9 are in 2 ×n rows",
		"10 spans below, collapsed", "×4</b>", "fold=0", `class="pane" id="span-detail"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("folded page missing %q", want)
		}
	}
	if n := strings.Count(body, `<li><a class="trow"`); n != 35 {
		t.Errorf("%d span rows in the HTML, want all 35: folding closes <details>, it never drops a row", n)
	}

	all := doDetailQuery(t, h, tr.ID, "fold=0").Body.String()
	if !strings.Contains(all, "All 35 spans shown.") || strings.Contains(all, "<details class=\"ml-4\">") || strings.Contains(all, "×4</b>") {
		t.Error("?fold=0 must show every row unfolded and say so")
	}
	if !strings.Contains(all, "?span=sub0-bash0&amp;fold=0") {
		t.Error("row links must keep fold=0, or the next click folds the tree again")
	}
}

// Small traces are left alone. Only a tool call's phase rows start closed,
// whatever the size (Claude Code).
func TestDetailLeavesEveryRealCaptureUnfolded(t *testing.T) {
	files, _ := filepath.Glob("../../testdata/*.pb")
	for _, f := range files {
		s := newTestStore(t)
		h := newTestHandlers(t, s)
		ingestCapture(t, s, filepath.Base(f))
		tr, spans := onlyTrace(t, s)
		body := doDetail(t, h, tr.ID).Body.String()
		if phases := strings.Contains(body, "claude_code.tool.execution"); strings.Contains(body, ">Showing ") != phases {
			t.Errorf("%s (%d spans) was folded", filepath.Base(f), len(spans))
		}
	}
}
