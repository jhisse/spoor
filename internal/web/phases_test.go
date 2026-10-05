package web

import (
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// An interactive Claude Code turn (sanitised capture): 20 spans, of which 8
// are tool phases and 3 are permission-classifier calls under a phase.
func TestInteractiveCaptureFoldsPhasesUnderTheirTool(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "claude-code-interactive-tools.pb")
	tr, _ := onlyTrace(t, s)

	body := doDetail(t, h, tr.ID).Body.String()
	for _, want := range []string{
		"Showing 9 of 20 spans.", "3 spans below, collapsed", "2 spans below, collapsed",
		"claude_code.tool Bash", ">waited 5.5 s<", ">generate_session_title<", ">repl_main_thread<",
		"List the files here and read the note", // the prompt, on the span selected by default
	} {
		if !strings.Contains(body, want) {
			t.Errorf("detail page is missing %q", want)
		}
	}
	if strings.Contains(body, "In progress") {
		t.Error("a trace with its root must not be called in progress")
	}
	if n := strings.Count(body, `<li><a class="trow"`); n != 20 {
		t.Errorf("%d span rows in the HTML, want all 20: phases are closed, not dropped", n)
	}
	if all := doDetailQuery(t, h, tr.ID, "fold=0").Body.String(); !strings.Contains(all, "All 20 spans shown.") || strings.Contains(all, "spans below, collapsed") {
		t.Error("?fold=0 must open the phase rows and say so")
	}
}

func TestInteractiveCaptureToolPanelAndSession(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "claude-code-interactive-tools.pb")
	tr, spans := onlyTrace(t, s)
	var tool, classifier store.Span
	for _, sp := range spans {
		if sp.Kind == store.SpanKindTool && tool.ID == "" {
			tool = sp
		}
		if sp.Kind == store.SpanKindGeneration && sp.ParentSpanID != nil && *sp.ParentSpanID != spans[0].ID && classifier.ID == "" {
			classifier = sp
		}
	}
	panel := doDetailQuery(t, h, tr.ID, "span="+tool.ID).Body.String()
	for _, want := range []string{">Input<", "bash_command", "ls -la", ">Output<", "note.txt\nREADME.md"} {
		if !strings.Contains(panel, want) {
			t.Errorf("tool panel is missing %q: the tool.output event is the tool's input and output", want)
		}
	}
	// Selecting a span under a phase opens the way to it.
	if under := doDetailQuery(t, h, tr.ID, "span="+classifier.ID).Body.String(); !strings.Contains(under, "Showing 12 of 20 spans.") || !strings.Contains(under, ">auto_mode<") {
		t.Error("selecting the classifier call must open its tool's phases")
	}

	if replay := doSessions(t, h, "session=00000000-0000-4000-8000-00000000c0de").Body.String(); !strings.Contains(replay, "List the files here and read the note") {
		t.Error("a turn whose model calls carry no body should preview the prompt on its agent span")
	}
}

// A turn still running: spans whose parent has not arrived, no root.
func TestRootlessTraceSaysInProgress(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	t0 := time.Now().Add(-time.Minute)
	missing := "not-arrived"
	tr := store.Trace{ID: "live", Name: "llm_request", StartedAt: t0, EndedAt: t0.Add(time.Second), Status: store.StatusUnset, CreatedAt: t0}
	if err := s.MergeTrace(t.Context(), tr, false); err != nil {
		t.Fatal(err)
	}
	seedSpan(t, s, "live", "a", &missing, t0)
	seedSpan(t, s, "live", "b", &missing, t0.Add(time.Second))
	seedTrace(t, s, "done", t0)
	seedSpan(t, s, "done", "root", nil, t0)

	if body := doDetail(t, h, "live").Body.String(); !strings.Contains(body, "In progress — root span not received yet<") || !strings.Contains(body, "the 2 top-level rows") {
		t.Error("a rootless trace must say it is in progress")
	} else if strings.Contains(body, ">no parent<") {
		t.Error("the banner already says the root is missing: no chip on every row")
	}
	if body := doDetail(t, h, "done").Body.String(); strings.Contains(body, "In progress") {
		t.Error("a trace with a root is not in progress")
	}
	if list := doList(t, h, "").Body.String(); strings.Count(list, "◷ in progress") != 1 {
		t.Error("the list must mark the rootless trace, and only it")
	}
}

// Permission refused: the tool has a waiting phase that says "reject", no
// execution phase, and status unset like any other.
func TestRefusedToolCallIsMarked(t *testing.T) {
	t0 := time.Now()
	span := func(id, parent, meta string, kind store.SpanKind) store.Span {
		sp := store.Span{TraceID: "t", ID: id, Kind: kind, Name: id, StartedAt: t0, EndedAt: t0.Add(2 * time.Second), Metadata: []byte(meta)}
		if parent != "" {
			sp.ParentSpanID = &parent
		}
		return sp
	}
	roots := BuildSpanTree([]store.Span{
		span("turn", "", `{}`, store.SpanKindAgent),
		span("refused", "turn", `{}`, store.SpanKindTool),
		span("wait1", "refused", `{"span.type":"tool.blocked_on_user","decision":"reject"}`, store.SpanKindGeneric),
		span("ran", "turn", `{}`, store.SpanKindTool),
		span("wait2", "ran", `{"span.type":"tool.blocked_on_user","decision":"accept"}`, store.SpanKindGeneric),
		span("exec", "ran", `{"span.type":"tool.execution"}`, store.SpanKindGeneric),
	})
	refused, ran := roots[0].Children[0], roots[0].Children[1]
	if refused.Span.ID != "refused" {
		refused, ran = ran, refused
	}
	if !refused.Denied || refused.Wait != 2*time.Second || !refused.PhasesOnly {
		t.Errorf("refused call: %+v", refused)
	}
	if ran.Denied || ran.Wait != 2*time.Second || !ran.PhasesOnly || roots[0].PhasesOnly || roots[0].Wait != 0 {
		t.Errorf("allowed call: %+v, turn: %+v", ran, roots[0])
	}
}

// The long-context warning is for the models that have such a tier (Gemini
// Pro above 200k), never for Claude. Fast mode is flagged from the span's
// own speed attribute.
func TestCostBreakdownFlagsLongContextAndFastMode(t *testing.T) {
	model, in, out := "claude-sonnet-5[1m]", int64(250_000), int64(10)
	sp := store.Span{Kind: store.SpanKindGeneration, Model: &model, InputTokens: &in, OutputTokens: &out, Metadata: []byte(`{"speed":"normal"}`)}
	prices := []store.ModelPrice{
		{ModelPattern: "^claude-sonnet-5$", InputPricePerToken: 2e-6, OutputPricePerToken: 10e-6},
		{ModelPattern: "^(google/)?gemini-3.1-pro-preview$", InputPricePerToken: 2e-6, OutputPricePerToken: 12e-6},
	}
	c := buildCostBreakdown(sp, prices)
	if c.Pattern != "^claude-sonnet-5$" || c.LongContext || c.FastMode {
		t.Errorf("pattern %q long %v fast %v: want the [1m] model priced by its base row, with no warning", c.Pattern, c.LongContext, c.FastMode)
	}
	model = "google/gemini-3.1-pro-preview"
	if !buildCostBreakdown(sp, prices).LongContext {
		t.Error("a Gemini Pro prompt above 200k is billed at a higher rate: want the warning")
	}
	in = 150_000
	if buildCostBreakdown(sp, prices).LongContext {
		t.Error("150k tokens is not long context")
	}
	sp.Metadata = []byte(`{"speed":"fast"}`)
	if !buildCostBreakdown(sp, prices).FastMode {
		t.Error(`speed "fast" must be flagged: the cost is at the standard rate`)
	}
}

// The caption under a dollar chart names the 0.1x/1.25x estimate only when a
// step's price row really lacks a cache price.
func TestDollarsCaption(t *testing.T) {
	read, write := 2e-7, 5e-6
	explicit, bare := "explicit-model", "bare-model"
	prices := []store.ModelPrice{
		{ModelPattern: "^explicit-model$", InputPricePerToken: 4e-6, CacheReadPricePerToken: &read, CacheWritePricePerToken: &write},
		{ModelPattern: "^bare-model$", InputPricePerToken: 1e-6},
	}
	steps := []genStep{{Span: store.Span{Model: &explicit}}}
	if got := dollarsCaption(steps, prices); got != "The same steps in dollars, at list price from spoor's price table." {
		t.Errorf("caption with explicit cache prices = %q", got)
	}
	got := dollarsCaption(append(steps, genStep{Span: store.Span{Model: &bare}}), prices)
	if !strings.Contains(got, "bare-model has no cache price there") || !strings.Contains(got, "estimated at 0.1×") {
		t.Errorf("caption with an estimated cache price = %q", got)
	}
}
