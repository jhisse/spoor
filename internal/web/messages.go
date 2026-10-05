package web

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// chatMessage is the display side of the JSON internal/otlp's message type
// writes into spans.input/output. A field renamed there must be renamed
// here, or the page falls back to showing the raw JSON.
type chatMessage struct {
	Role         string
	Content      string
	New          bool // not in the previous generation's prompt (set by buildContextDelta)
	FinishReason string
	Reasoning    string // the model's own reasoning text, from a thinking or reasoning block
	ToolCalls    []toolCallView
	ToolResults  []toolResultView
}

// excerptLimit is where a long text is cut: the panel shows the head and
// keeps the rest behind a disclosure that states its size.
const excerptLimit = 600

// longText is a text split for display. More is empty when it is short.
type longText struct {
	Head, More string
	N          int // characters in More
}

// excerpt cuts s at the last line break in the second half of its first
// excerptLimit bytes, else at the last space, else at a rune boundary. A
// text only a little over the limit is left whole.
func excerpt(s string) longText {
	if len(s) <= excerptLimit*5/4 {
		return longText{Head: s}
	}
	cut := excerptLimit
	for !utf8.RuneStart(s[cut]) {
		cut--
	}
	if i := strings.LastIndexByte(s[:cut], '\n'); i >= excerptLimit/2 {
		cut = i
	} else if i := strings.LastIndexByte(s[:cut], ' '); i >= excerptLimit/2 {
		cut = i
	}
	more := strings.TrimLeft(s[cut:], " \n")
	return longText{Head: s[:cut], More: more, N: utf8.RuneCountInString(more)}
}

// toolCallView's Arguments render as a key/value tree (ArgumentsTree) when they
// are a JSON object; Arguments, pretty-printed, is the fallback.
type toolCallView struct {
	Name          string
	Arguments     string
	ArgumentsTree []metadataNode
}

type toolResultView struct {
	ToolUseID   string
	Content     string
	ContentTree []metadataNode
	IsError     bool
}

// buildToolCallView and buildToolResultView use the tree when rawJSON is a
// JSON object (buildMetadataTree returns nil otherwise), else the
// pretty-printed text: a tool may report back a plain string.
func buildToolCallView(name, rawJSON string) toolCallView {
	if tree := buildMetadataTree(json.RawMessage(rawJSON)); tree != nil {
		return toolCallView{Name: name, ArgumentsTree: tree}
	}
	return toolCallView{Name: name, Arguments: prettyJSON(rawJSON)}
}

func buildToolResultView(toolUseID, rawJSON string, isError bool) toolResultView {
	if tree := buildMetadataTree(json.RawMessage(rawJSON)); tree != nil {
		return toolResultView{ToolUseID: toolUseID, ContentTree: tree, IsError: isError}
	}
	return toolResultView{ToolUseID: toolUseID, Content: prettyJSON(rawJSON), IsError: isError}
}

// contentBlock is one entry of Anthropic's multi-part message content
// ({"type": "text"|"tool_use"|"tool_result", ...}). When a turn isn't plain
// text, OpenLLMetry JSON-encodes that array as a string inside the message's
// content (testdata/openllmetry-langgraph-tool-loop).
type contentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   json.RawMessage `json:"content"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

// parseMessages decodes a span's Input or Output as the message array
// internal/otlp writes. Nil when raw is nil or is not that shape (a
// single-string body): the caller then shows it pretty-printed.
func parseMessages(raw *string) []chatMessage {
	if raw == nil {
		return nil
	}

	var decoded []struct {
		Role         string `json:"role"`
		Content      string `json:"content"`
		Reasoning    string `json:"reasoning"`
		FinishReason string `json:"finish_reason"`
		ToolCalls    []struct {
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(*raw), &decoded); err != nil || len(decoded) == 0 {
		return nil
	}

	out := make([]chatMessage, len(decoded))
	for i, m := range decoded {
		msg := chatMessage{Role: m.Role, Content: m.Content, Reasoning: m.Reasoning, FinishReason: m.FinishReason}
		if len(m.ToolCalls) > 0 {
			msg.ToolCalls = make([]toolCallView, len(m.ToolCalls))
			for j, tc := range m.ToolCalls {
				msg.ToolCalls[j] = buildToolCallView(tc.Name, tc.Arguments)
			}
		}
		expandContentBlocks(&msg)
		out[i] = msg
	}
	return out
}

// expandContentBlocks rewrites msg when its Content is a JSON-encoded array
// of contentBlocks (or one block): text blocks are joined back into Content, thinking and
// reasoning blocks into Reasoning, tool_use
// blocks become ToolCalls, tool_result blocks become ToolResults. An array
// with a block type it does not know is left as text, never guessed at.
func expandContentBlocks(msg *chatMessage) {
	var blocks []contentBlock
	if err := json.Unmarshal([]byte(msg.Content), &blocks); err != nil {
		var one contentBlock // OpenLLMetry writes a lone reasoning block as one object
		if json.Unmarshal([]byte(msg.Content), &one) != nil || one.Type == "" {
			return
		}
		blocks = []contentBlock{one}
	}
	if len(blocks) == 0 {
		return
	}
	for _, b := range blocks {
		switch b.Type {
		case "text", "tool_use", "tool_result", "thinking", "reasoning":
		default:
			return
		}
	}

	var textParts []string
	for _, b := range blocks {
		switch b.Type {
		case "text":
			if b.Text != "" {
				textParts = append(textParts, b.Text)
			}
		case "thinking":
			msg.Reasoning = strings.TrimSpace(msg.Reasoning + "\n\n" + b.Thinking)
		case "reasoning":
			msg.Reasoning = strings.TrimSpace(msg.Reasoning + "\n\n" + scalarString(b.Content))
		case "tool_use":
			msg.ToolCalls = append(msg.ToolCalls, buildToolCallView(b.Name, string(b.Input)))
		case "tool_result":
			msg.ToolResults = append(msg.ToolResults, buildToolResultView(b.ToolUseID, scalarString(b.Content), b.IsError))
		}
	}
	msg.Content = strings.Join(textParts, "\n\n")
}

// prettyJSON re-indents s if it is valid JSON, else returns s unchanged.
func prettyJSON(s string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
}
