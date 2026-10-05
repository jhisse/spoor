package web

import (
	"strings"
	"testing"

	"github.com/jhisse/spoor/internal/store"
)

// Captures of testdata/chatbots with real models: what a model that reasons
// leaves in a trace.

func reasoningTokens(spans []store.Span) (n int64) {
	for _, sp := range spans {
		n += store.Deref(sp.ReasoningTokens)
	}
	return n
}

func TestChatbotReasoningTokensAreKeptWhereTheSenderReportsThem(t *testing.T) {
	for file, want := range map[string]int64{"chatbot-llamaindex-reasoning.pb": 64 + 192, "chatbot-pydanticai-reasoning.pb": 128} {
		s := newTestStore(t)
		ingestCapture(t, s, file)
		if _, spans := onlyTrace(t, s); reasoningTokens(spans) != want {
			t.Errorf("%s: reasoning tokens = %d, want %d", file, reasoningTokens(spans), want)
		}
	}
	// The official OpenTelemetry OpenAI instrumentation does not send them: unknown, not zero.
	s := newTestStore(t)
	ingestCapture(t, s, "chatbot-plain-openai-reasoning.pb")
	if _, spans := onlyTrace(t, s); reasoningTokens(spans) != 0 {
		t.Errorf("reasoning tokens = %d, want none reported", reasoningTokens(spans))
	}
}

func TestChatbotClaudeThinkingShowsUnderItsOwnDisclosure(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	ingestCapture(t, s, "chatbot-plain-anthropic-thinking.pb")
	tr, spans := onlyTrace(t, s)
	shown := 0
	for _, sp := range spans {
		if sp.Output == nil || !strings.Contains(*sp.Output, `"reasoning"`) {
			continue
		}
		page := doDetailQuery(t, h, tr.ID, "span="+sp.ID).Body.String()
		if !strings.Contains(page, "<summary>Reasoning") {
			t.Errorf("span %s: the thinking block is not under a Reasoning disclosure", sp.Name)
		}
		shown++
	}
	if shown != 1 {
		t.Errorf("%d calls carry reasoning text, want 1", shown)
	}
}
