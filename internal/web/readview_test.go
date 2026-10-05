package web

import (
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// The Read view lays the LangGraph tool loop out as a transcript: two
// numbered model calls, the second showing only what it added, the tool
// span listed after the call it followed, and no tree.
func TestDetailReadViewIsATranscript(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "openllmetry-langgraph-tool-loop.pb")
	tr, _ := onlyTrace(t, s)

	tree := doDetail(t, h, tr.ID).Body.String()
	if !strings.Contains(tree, `view=read"`) || !strings.Contains(tree, `>Read</a>`) || !strings.Contains(tree, `id="span-tree"`) {
		t.Errorf("the tree page should offer the Read view and keep its tree")
	}
	body := doDetailQuery(t, h, tr.ID, "view=read").Body.String()
	for _, want := range []string{"2 model calls in start order", `<span class="turn-n">1</span>`, `<span class="turn-n">2</span>`,
		"new since the previous generation", "unchanged earlier messages: 1", `aria-current="true" title="The model calls as a transcript`} {
		if !strings.Contains(body, want) {
			t.Errorf("Read view missing %q", want)
		}
	}
	if strings.Contains(body, `id="span-tree"`) || strings.Count(body, ">new<") != 2 {
		t.Errorf("the Read view replaces the tree and marks only the second call's new messages")
	}

	// The LangChain capture runs its tool between the two calls: it is listed after the first.
	s2 := newTestStore(t)
	h2 := newTestHandlers(t, s2)
	ingestCapture(t, s2, "openinference-langchain-tool.pb")
	tr2, _ := onlyTrace(t, s2)
	body = doDetailQuery(t, h2, tr2.ID, "view=read").Body.String()
	first, then, second := strings.Index(body, `<span class="turn-n">1</span>`), strings.Index(body, `<span class="cap">then</span>`), strings.Index(body, `<span class="turn-n">2</span>`)
	if first < 0 || then < first || second < then || !strings.Contains(body[then:second], "get_weather") {
		t.Errorf("the tool span should be listed after the first call and before the second")
	}
}

// A trace with one model call has nothing to read as a transcript.
func TestReadViewNeedsTwoCalls(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	tr := seedTrace(t, s, "trace-1", time.Now())
	insertSpans(t, s, gen(tr.ID, "g1", nil, time.Now(), prompt(msgSystem, msgUser1)))
	if body := doDetailQuery(t, h, tr.ID, "view=read").Body.String(); strings.Contains(body, "view=read") || !strings.Contains(body, `id="span-tree"`) {
		t.Errorf("one call: no Read view offered, the tree stays")
	}
}

// A session is named after what the person asked first: the first user
// message of its first turn, in the sessions table and on the first
// screen's cards; the id stays in a title.
func TestSessionsAreNamedByTheirFirstPrompt(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	t0 := time.Now().Add(-time.Hour)
	seedSessionTurn(t, s, "turn-1", "sess-1", t0)
	seedSessionTurn(t, s, "turn-2", "sess-1", t0.Add(time.Minute))
	insertSpans(t, s, gen("turn-1", "g1", nil, t0.Add(-time.Second), prompt(msgUser1, msgAsst1))) // before the helper's own call

	for name, body := range map[string]string{"sessions": doSessions(t, h, "").Body.String(), "list": doList(t, h, "").Body.String()} {
		if !strings.Contains(body, `title="first question">first question</a>`) && !strings.Contains(body, `title="first question">first question</span>`) {
			t.Errorf("%s page: the session should be named by its first user message, got: %s", name, body)
		}
	}
}

// In a session, the Read view compares a turn's first call with the
// previous turn's last call, as the span panel does.
func TestReadViewComparesFirstCallWithPreviousTurn(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	t0 := time.Now().Add(-time.Hour)
	seedSessionTurn(t, s, "turn-1", "sess-1", t0)
	seedSessionTurn(t, s, "turn-2", "sess-1", t0.Add(time.Minute))
	// Each seeded turn has one call with the same two-message prompt; the
	// second turn's extra call extends it. Its first call, compared with the
	// previous turn's, adds nothing; the extra call adds two messages.
	insertSpans(t, s, gen("turn-2", "g2", nil, t0.Add(time.Minute+time.Second),
		prompt(`{"role":"system","content":"be brief"}`, `{"role":"user","content":"qual a capital?"}`, msgAsst1, msgUser2)))
	body := doDetailQuery(t, h, "turn-2", "view=read").Body.String()
	for _, want := range []string{"unchanged earlier messages: 2", "No new message in this call.", ">second question<"} {
		if !strings.Contains(body, want) {
			t.Errorf("Read view missing %q", want)
		}
	}
}

// The first filter row holds the search, the model and "that span failed";
// the trace status is behind "More filters", which opens when it is set.
func TestFilterFormRows(t *testing.T) {
	h := newTestHandlers(t, newTestStore(t))
	body := doList(t, h, "status=error").Body.String()
	if !strings.Contains(body, `<details class="disc hint" open>`) || !strings.Contains(body, `<option selected>error</option>`) {
		t.Errorf("a status filter should open More filters with it selected")
	}
	if i, j := strings.Index(body, `name="failed"`), strings.Index(body, `<details class="disc hint"`); i < 0 || i > j {
		t.Errorf("the failed-span checkbox belongs to the first row, before More filters")
	}
	if body := doList(t, h, "failed=1").Body.String(); strings.Contains(body, `<details class="disc hint" open>`) {
		t.Errorf("a failed-span filter alone should not open More filters")
	}
}

// A turn that ended on a small classifier does not stand for the main
// model's last prompt: the next turn's first Sonnet call is compared with
// the previous turn's last Sonnet call, and with nothing when there was none.
// The Read view and the tree's span panel read it the same way.
func TestFirstCallIsComparedWithThePreviousTurnsCallOfItsOwnModel(t *testing.T) {
	const sonnet, haiku = "claude-sonnet-5-5", "claude-haiku-4-5-20251001"
	t0 := time.Now().Add(-time.Hour)
	turn := func(s store.Store, id string, at time.Time) {
		t.Helper()
		sid := "sess-1"
		tr := store.Trace{ID: id, Name: id, SessionID: &sid, StartedAt: at, EndedAt: at.Add(20 * time.Second), Status: store.StatusOK, CreatedAt: at}
		if err := s.MergeTrace(t.Context(), tr, true); err != nil {
			t.Fatal(err)
		}
	}
	call := func(trace, id, model string, at time.Time, in int64) store.Span {
		sp := gen(trace, id, nil, at, prompt(msgSystem, msgUser1))
		sp.Model, sp.InputTokens = ptr(model), ptr(in)
		return sp
	}
	for _, c := range []struct {
		name       string
		previous   []store.Span // the previous turn's calls
		wantLink   string       // "" = no comparison
		wantTokens string
	}{
		{"classifier last", []store.Span{call("turn-1", "s1", sonnet, t0, 50000), call("turn-1", "h1", haiku, t0.Add(2*time.Second), 5000)},
			`href="/traces/turn-1?span=s1"`, "(&#43;7,000 vs the previous generation)"},
		{"only a classifier", []store.Span{call("turn-1", "h1", haiku, t0, 5000)}, "", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newTestStore(t)
			h := newTestHandlers(t, s)
			turn(s, "turn-1", t0)
			turn(s, "turn-2", t0.Add(time.Minute))
			insertSpans(t, s, c.previous...)
			insertSpans(t, s, call("turn-2", "s2", sonnet, t0.Add(time.Minute), 57000), call("turn-2", "s3", sonnet, t0.Add(time.Minute+5*time.Second), 58000))

			for view, body := range map[string]string{
				"Read view":  doDetailQuery(t, h, "turn-2", "view=read").Body.String(),
				"span panel": doDetailQuery(t, h, "turn-2", "span=s2").Body.String(),
			} {
				if strings.Contains(body, "span=h1") {
					t.Errorf("%s: the classifier's call must not be the comparison", view)
				}
				if c.wantLink == "" {
					if strings.Contains(body, "turn-1?span=") || !strings.Contains(body, "first generation, there is no previous call") {
						t.Errorf("%s: no previous call of this model, so no comparison with the previous turn", view)
					}
					continue
				}
				if !strings.Contains(body, c.wantLink) || !strings.Contains(body, c.wantTokens) {
					t.Errorf("%s: want %s and %q", view, c.wantLink, c.wantTokens)
				}
			}
		})
	}
}

// The Read view's Previous and Next match buttons load the page again, stay
// in the Read view and the search, and answer to n and p like the tree's do.
func TestReadViewSearchStepsKeepTheViewAndAnswerToKeys(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	now := time.Now()
	tr := seedTrace(t, s, "trace-1", now)
	insertSpans(t, s,
		gen(tr.ID, "g1", nil, now, prompt(msgSystem, `{"role":"user","content":"needle one"}`)),
		gen(tr.ID, "g2", nil, now.Add(time.Second), prompt(msgSystem, `{"role":"user","content":"needle two"}`)))

	body := doDetailQuery(t, h, tr.ID, "q=needle&view=read&span=g1").Body.String()
	step := `hx-get="/traces/trace-1?span=g2&amp;q=needle&amp;view=read" hx-target="#trace-detail" hx-swap="outerHTML" hx-push-url="true" hx-trigger="click, keyup[key=='`
	for _, want := range []string{step + "p' && ", step + "n' && "} {
		if !strings.Contains(body, want) {
			t.Errorf("Read view missing %q", want)
		}
	}
}
