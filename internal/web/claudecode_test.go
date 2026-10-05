package web

import (
	"strings"
	"testing"
)

// A Claude Code capture (`claude -p`, one file read) from the ingestion
// handler to the pages: model, the four token buckets, the calculated cost
// and the session must all be on screen.
func TestClaudeCodeCaptureRendersUsageCostAndSession(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "claude-code-tool-read.pb")
	tr, _ := onlyTrace(t, s)
	const session = "d0f3dfdb-e920-4602-b7fa-dc9a65327872"

	// The first model call: input 10, cache read 26500, cache write 9245, output 150.
	detail := doDetailQuery(t, h, tr.ID, "span=5ad25b41c3d69785").Body.String()
	for _, want := range []string{
		"claude-haiku-4-5-20251001", session,
		"cache read: 26,500", "fresh input: 10", "cache write: 9,245", "output: 150",
		"$0.01496625",                            // that call, priced by bucket from the catalog
		"Σ $0.0197",                              // the interaction: its three model calls, counted once
		"3 generations", `title="tool">T</span>`, // the three llm_request spans in the chart, the Read tool span in the tree
	} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail page is missing %q", want)
		}
	}

	root := doDetail(t, h, tr.ID).Body.String()
	if !strings.Contains(root, "Read the file note.txt") {
		t.Error("the interaction span should show the user prompt as its input")
	}

	if list := doList(t, h, "").Body.String(); !strings.Contains(list, "$0.0197") {
		t.Error("trace list should show the trace's total cost")
	}
	if sessions := doSessions(t, h, "").Body.String(); !strings.Contains(sessions, session) {
		t.Error("sessions page should list the Claude Code session")
	}
	if replay := doSessions(t, h, "session="+session).Body.String(); !strings.Contains(replay, tr.ID) {
		t.Error("session replay should link the interaction's trace")
	}
}
