package web

import (
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

const (
	msgSystem = `{"role":"system","content":"you are terse"}`
	msgUser1  = `{"role":"user","content":"first question"}`
	msgAsst1  = `{"role":"assistant","content":"first answer"}`
	msgUser2  = `{"role":"user","content":"second question"}`
)

func prompt(msgs ...string) *string { return ptr("[" + strings.Join(msgs, ",") + "]") }

func TestGenStepsPairsWithinNearestAgent(t *testing.T) {
	t0 := time.Now()
	agent := store.Span{ID: "agent", Kind: store.SpanKindAgent, Name: "main", StartedAt: t0}
	sub := store.Span{ID: "sub", Kind: store.SpanKindAgent, Name: "helper", ParentSpanID: ptr("agent"), StartedAt: t0}
	tool := store.Span{ID: "tool", Kind: store.SpanKindTool, ParentSpanID: ptr("sub"), StartedAt: t0}
	steps := genSteps([]store.Span{
		agent, sub, tool,
		gen("t", "g3", ptr("agent"), t0.Add(3*time.Second), nil),
		gen("t", "g1", ptr("agent"), t0.Add(1*time.Second), nil),
		gen("t", "g2", ptr("tool"), t0.Add(2*time.Second), nil),
	})
	if len(steps) != 3 || steps[0].Span.ID != "g1" || steps[2].Span.ID != "g3" {
		t.Fatalf("steps not in start order: %+v", steps)
	}
	if steps[0].Prev != nil || steps[1].Prev != nil {
		t.Errorf("first generation of each agent must have no predecessor")
	}
	if steps[1].ScopeName != "helper" {
		t.Errorf("g2 scope = %q, want the nearest agent ancestor (through the tool span)", steps[1].ScopeName)
	}
	if steps[2].Prev == nil || steps[2].Prev.ID != "g1" {
		t.Errorf("g3 must be compared with g1 (same agent), not with the sub-agent's g2")
	}
}

func TestGenStepsSurvivesParentCycle(t *testing.T) {
	a := store.Span{ID: "a", Kind: store.SpanKindChain, ParentSpanID: ptr("b")}
	b := store.Span{ID: "b", Kind: store.SpanKindChain, ParentSpanID: ptr("a")}
	steps := genSteps([]store.Span{a, b, gen("t", "g", ptr("a"), time.Now(), nil)})
	if len(steps) != 1 || steps[0].Scope != "" {
		t.Errorf("steps = %+v, want one generation with no agent scope", steps)
	}
}

func prevGen() store.Span {
	prev := gen("t", "g1", nil, time.Now(), prompt(msgSystem, msgUser1))
	prev.InputTokens = ptr(int64(100))
	return prev
}

func TestBuildContextDeltaGrownPrompt(t *testing.T) {
	prev := prevGen()
	cur := gen("t", "g2", nil, time.Now(), prompt(msgSystem, msgUser1, msgAsst1, msgUser2))
	cur.InputTokens = ptr(int64(180))
	d := buildContextDelta(&prev, cur)
	if d.NoBasis != "" || len(d.Kept) != 2 || len(d.New) != 2 || d.RewrittenAt != 0 {
		t.Fatalf("delta = %+v", d)
	}
	if d.New[1].Content != "second question" {
		t.Errorf("New = %+v", d.New)
	}
	if !strings.Contains(d.KeptLabel, "unchanged earlier messages: 2") || !strings.Contains(d.KeptLabel, "(estimated)") {
		t.Errorf("KeptLabel = %q", d.KeptLabel)
	}
	if !strings.Contains(d.TokenLine, "+80") {
		t.Errorf("TokenLine = %q, want the measured +80 input tokens", d.TokenLine)
	}
	if d.PrevURL != "/traces/t?span=g1" {
		t.Errorf("PrevURL = %q", d.PrevURL)
	}
}

func TestBuildContextDeltaRewrittenAndIdentical(t *testing.T) {
	prev := prevGen()
	d := buildContextDelta(&prev, gen("t", "g2", nil, time.Now(), prompt(msgSystem, msgUser2)))
	if d.NoBasis != "" || len(d.Kept) != 1 || d.RewrittenAt != 2 || len(d.New) != 1 {
		t.Fatalf("rewritten: delta = %+v", d)
	}
	if !strings.Contains(d.KeptLabel, "characters") || strings.Contains(d.KeptLabel, "tokens") {
		t.Errorf("KeptLabel = %q, want characters when input tokens are unknown", d.KeptLabel)
	}

	d = buildContextDelta(&prev, gen("t", "g2", nil, time.Now(), prompt(msgSystem, msgUser1)))
	if d.NoBasis != "" || len(d.Kept) != 2 || len(d.New) != 0 || d.RewrittenAt != 0 {
		t.Fatalf("identical prompt: delta = %+v", d)
	}
}

func TestBuildContextDeltaPointsInsideTheRewrittenMessage(t *testing.T) {
	prev := prevGen()
	grown := `{"role":"user","content":"first question, briefly"}`
	d := buildContextDelta(&prev, gen("t", "g2", nil, time.Now(), prompt(msgSystem, grown)))
	if d.RewrittenAt != 2 || d.Rewrite == nil {
		t.Fatalf("delta = %+v", d)
	}
	want := rewrite{Sentence: "Message 2 (user) grew by 9 characters at character 15.", Text: true, Pre: "first question", Now: ", briefly"}
	if *d.Rewrite != want {
		t.Errorf("rewrite = %+v", d.Rewrite)
	}

	long := strings.Repeat("x", 500)
	prev = gen("t", "g1", nil, time.Now(), prompt(msgSystem, `{"role":"user","content":"`+long+`B`+long+`"}`))
	d = buildContextDelta(&prev, gen("t", "g2", nil, time.Now(), prompt(msgSystem, `{"role":"user","content":"`+long+`A`+long+`"}`)))
	want = rewrite{Sentence: "Message 2 (user) changed at character 501: 1 characters became 1.", Text: true, Was: "B", Now: "A",
		Pre: "…" + strings.Repeat("x", 80), Post: strings.Repeat("x", 80) + "…"}
	if *d.Rewrite != want {
		t.Errorf("rewrite = %+v", d.Rewrite)
	}

	d = buildContextDelta(&prev, gen("t", "g2", nil, time.Now(), prompt(msgSystem, `{"role":"tool","content":"first question"}`)))
	if want = (rewrite{Sentence: "Message 2 (tool) was user in the previous generation."}); *d.Rewrite != want {
		t.Errorf("rewrite = %+v", d.Rewrite)
	}
}

func TestBuildContextDeltaNoBasis(t *testing.T) {
	t0 := time.Now()
	prev := prevGen()
	noBasis := map[string]struct {
		prev *store.Span
		cur  store.Span
	}{
		"no previous generation":      {nil, gen("t", "g2", nil, t0, prompt(msgSystem))},
		"no common first message":     {&prev, gen("t", "g2", nil, t0, prompt(msgUser2))},
		"input is not a message list": {&prev, gen("t", "g2", nil, t0, ptr(`"just a string"`))},
		"input not recorded":          {&prev, gen("t", "g2", nil, t0, nil)},
		"previous prompt not recorded": {
			ptr(gen("t", "g1", nil, t0, nil)), gen("t", "g2", nil, t0, prompt(msgSystem)),
		},
	}
	for name, tc := range noBasis {
		t.Run(name, func(t *testing.T) {
			d := buildContextDelta(tc.prev, tc.cur)
			if d.NoBasis == "" || d.Kept != nil || d.New != nil || d.RewrittenAt != 0 {
				t.Errorf("delta = %+v, want no basis and no markers", d)
			}
		})
	}
}

// The LangGraph capture has two generations whose second prompt is the
// first prompt plus the tool round-trip.
func TestDetailContextDeltaOnRealToolLoopCapture(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "openllmetry-langgraph-tool-loop.pb")
	tr, spans := onlyTrace(t, s)
	steps := genSteps(spans)
	if len(steps) != 2 {
		t.Fatalf("want 2 generations in the capture, got %d", len(steps))
	}

	second := doDetailQuery(t, h, tr.ID, "span="+steps[1].Span.ID).Body.String()
	for _, want := range []string{"new since the previous generation", "unchanged earlier messages: 1", "(estimated)", "Context per step</h3>", "2 generations · peak "} {
		if !strings.Contains(second, want) {
			t.Errorf("second generation: body missing %q", want)
		}
	}
	if n := strings.Count(second, ">new<"); n != 2 {
		t.Errorf("want 2 messages marked new (tool call, tool result), got %d", n)
	}
	// The baseline block has a no-basis sentence of its own: look at the input only.
	if input := second[strings.Index(second, "<h3>Input"):]; strings.Contains(input, "No basis for comparison") {
		t.Errorf("second generation has a basis, must not say otherwise")
	}

	first := doDetailQuery(t, h, tr.ID, "span="+steps[0].Span.ID).Body.String()
	if !strings.Contains(first, "No basis for comparison") || strings.Contains(first, ">new<") {
		t.Errorf("first generation must show the full prompt, unmarked, and say there is no basis")
	}
}

// Claude Code exports no prompt on its model calls.
func TestDetailNoBasisOnRealCaptureWithoutPrompts(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "claude-code-simple.pb")
	tr, spans := onlyTrace(t, s)
	for _, st := range genSteps(spans) {
		body := doDetailQuery(t, h, tr.ID, "span="+st.Span.ID).Body.String()
		if !strings.Contains(body, "No basis for comparison") || strings.Contains(body, ">new<") || strings.Contains(body, "unchanged earlier") {
			t.Errorf("span %s: want the no-basis sentence and no novelty markers", st.Span.Name)
		}
	}
}

func TestDetailNoCommonPrefixShowsFullPromptUnmarked(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	t0 := time.Now()
	tr := seedTrace(t, s, "trace-1", t0)
	insertSpans(t, s,
		gen(tr.ID, "g1", nil, t0, prompt(msgUser1)),
		gen(tr.ID, "g2", nil, t0.Add(time.Second), prompt(msgUser2)))

	body := doDetailQuery(t, h, tr.ID, "span=g2").Body.String()
	for _, want := range []string{"No basis for comparison", "second question"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, ">new<") || strings.Contains(body, "unchanged earlier") {
		t.Errorf("no-basis rendering must carry no novelty marker and no collapsed prefix")
	}
}

func TestDetailRewrittenContextSaysWhere(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	t0 := time.Now()
	tr := seedTrace(t, s, "trace-1", t0)
	insertSpans(t, s,
		gen(tr.ID, "g1", nil, t0, prompt(msgSystem, msgUser1, msgAsst1)),
		gen(tr.ID, "g2", nil, t0.Add(time.Second), prompt(msgSystem, msgUser2)))

	body := doDetailQuery(t, h, tr.ID, "span=g2").Body.String()
	if !strings.Contains(body, "Context rewritten from message 2 on. Message 2 (user) changed at character 1: 5 characters became 6.") {
		t.Errorf("body missing the rewrite sentence")
	}
	if !strings.Contains(body, "<mark>first</mark> question") || !strings.Contains(body, "<mark>second</mark> question") {
		t.Errorf("body missing the old and new text around the change")
	}
	if strings.Count(body, ">new<") != 1 {
		t.Errorf("want exactly the rewritten message marked new")
	}
}

func TestDetailFirstGenerationComparesWithPreviousTraceOfSession(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	t0 := time.Now().Add(-time.Minute)
	for i, id := range []string{"turn-1", "turn-2"} {
		at := t0.Add(time.Duration(i) * 10 * time.Second)
		tr := store.Trace{ID: id, SessionID: ptr("sess"), Name: id, StartedAt: at, EndedAt: at.Add(time.Second), Status: store.StatusOK, CreatedAt: at}
		if err := s.MergeTrace(t.Context(), tr, true); err != nil {
			t.Fatal(err)
		}
	}
	insertSpans(t, s,
		gen("turn-1", "g1", nil, t0, prompt(msgSystem, msgUser1)),
		gen("turn-2", "g2", nil, t0.Add(10*time.Second), prompt(msgSystem, msgUser1, msgAsst1, msgUser2)))

	body := doDetailQuery(t, h, "turn-2", "span=g2").Body.String()
	for _, want := range []string{"unchanged earlier messages: 2", `href="/traces/turn-1?span=g1"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	first := doDetailQuery(t, h, "turn-1", "span=g1").Body.String()
	if !strings.Contains(first, "No basis for comparison") {
		t.Errorf("the session's first generation has nothing before it")
	}
}

// A call is compared with the previous call of its own model: a coding
// agent's small classifier call after its main one is another context, not a
// shrunken one.
func TestGenStepsComparesWithinOneModel(t *testing.T) {
	big, small := "claude-opus-5-5", "claude-haiku-4-5"
	t0 := time.Now()
	gen := func(id, model string, at int) store.Span {
		return store.Span{ID: id, Kind: store.SpanKindGeneration, Model: &model, StartedAt: t0.Add(time.Duration(at) * time.Second)}
	}
	steps := genSteps([]store.Span{gen("main-1", big, 0), gen("classifier-1", small, 1), gen("main-2", big, 2), gen("classifier-2", small, 3)})
	prev := map[string]string{}
	for _, st := range steps {
		if st.Prev != nil {
			prev[st.Span.ID] = st.Prev.ID
		}
	}
	if len(prev) != 2 || prev["main-2"] != "main-1" || prev["classifier-2"] != "classifier-1" {
		t.Errorf("previous calls = %v, want each model's own", prev)
	}
}
