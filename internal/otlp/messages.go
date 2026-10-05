package otlp

import (
	"cmp"
	"encoding/json"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// message is one entry of the array stored in spans.input and spans.output,
// whatever form the sender used: every dialect below is rebuilt into it.
type message struct {
	Role         string     `json:"role,omitempty"`
	Content      string     `json:"content,omitempty"`
	Reasoning    string     `json:"reasoning,omitempty"` // the model's own reasoning text, kept apart from its answer
	FinishReason string     `json:"finish_reason,omitempty"`
	ToolCalls    []toolCall `json:"tool_calls,omitempty"`
}

type toolCall struct {
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

func (c *toolCall) set(field, val string) {
	switch field {
	case "id":
		c.ID = val
	case "name":
		c.Name = val
	case "arguments":
		c.Arguments = val
	}
}

// messageField matches one attribute of a message flattened into indexed
// attributes, in OpenLLMetry's form (gen_ai.prompt.N.role,
// gen_ai.completion.N.tool_calls.M.name, LiteLLM's
// gen_ai.completion.N.function_call.name) and OpenInference's
// (llm.input_messages.N.message.content,
// llm.output_messages.N.message.tool_calls.M.tool_call.function.name,
// llm.output_messages.N.message.contents.K.message_content.text, or .image.image.url
// for a picture). Groups: 1 side, 2 message index, 3 field, 4 tool call index,
// 5 tool call field, 6 content block index, 7 its kind.
var messageField = regexp.MustCompile(`^(gen_ai\.prompt|gen_ai\.completion|llm\.input_messages|llm\.output_messages)\.(\d+)\.(?:message\.)?(?:` +
	`(role|content|finish_reason)|` +
	`(?:tool_calls\.(\d+)|function_call)\.(?:tool_call\.)?(?:function\.)?(id|name|arguments)|` +
	`contents\.(\d+)\.message_content\.(text|image\.image\.url))$`)

// flattened rebuilds one side's message array, in index order, from the
// attributes messageField matches; output chooses the completion side.
func flattened(attrs map[string]string, output bool) []message {
	byIndex := map[int]*message{}
	texts := map[int]map[int]string{}
	calls := map[int]map[int]*toolCall{} // by index, never a slice: the index is the sender's
	for key, val := range attrs {
		m := messageField.FindStringSubmatch(key)
		if m == nil || output != (strings.HasSuffix(m[1], "completion") || strings.HasSuffix(m[1], "output_messages")) {
			continue
		}
		idx, _ := strconv.Atoi(m[2])
		if byIndex[idx] == nil {
			byIndex[idx], texts[idx], calls[idx] = &message{}, map[int]string{}, map[int]*toolCall{}
		}
		msg := byIndex[idx]
		switch {
		case m[3] == "role":
			msg.Role = val
		case m[3] == "content":
			msg.Content = val
		case m[3] == "finish_reason":
			msg.FinishReason = val
		case m[5] != "":
			call, _ := strconv.Atoi(m[4]) // function_call has no index: the only call
			if calls[idx][call] == nil {
				calls[idx][call] = &toolCall{}
			}
			calls[idx][call].set(m[5], val)
		default:
			block, _ := strconv.Atoi(m[6])
			if m[7] != "text" {
				val = imageNote(val)
			}
			texts[idx][block] = val
		}
	}
	var out []message
	for _, idx := range slices.Sorted(maps.Keys(byIndex)) {
		msg := *byIndex[idx]
		for _, call := range slices.Sorted(maps.Keys(calls[idx])) {
			msg.ToolCalls = append(msg.ToolCalls, *calls[idx][call])
		}
		if msg.Content == "" {
			msg.Content = joinBlocks(texts[idx])
		}
		out = append(out, msg)
	}
	return out
}

// part is one entry of a message's parts in the OTel GenAI form of
// gen_ai.input.messages, gen_ai.output.messages and
// gen_ai.system_instructions. Arguments, Response and Result are a string
// or any JSON value, depending on the sender.
type part struct {
	Type      string          `json:"type"`
	Content   string          `json:"content"`
	Text      string          `json:"text"` // Gemini CLI: {"text": "..."} and no type
	MimeType  string          `json:"mime_type"`
	Modality  string          `json:"modality"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Response  json.RawMessage `json:"response"`
	Result    json.RawMessage `json:"result"`
}

// partsMessages converts the OTel GenAI message array (role plus parts) in
// raw, preceded by the system instructions when there are any. Nil when raw
// is not that form: the caller then stores it as sent.
func partsMessages(raw, system string) []message {
	var in []struct {
		Role         string            `json:"role"`
		Parts        []json.RawMessage `json:"parts"`
		FinishReason string            `json:"finish_reason"`
	}
	if json.Unmarshal([]byte(raw), &in) != nil || !strings.Contains(raw, `"parts"`) {
		return nil
	}
	var out []message
	var instructions []json.RawMessage
	if json.Unmarshal([]byte(system), &instructions) == nil {
		if len(instructions) > 0 {
			out = append(out, partsMessage("system", instructions))
		}
	} else if system != "" { // Gemini CLI sends one JSON string
		out = append(out, message{Role: "system", Content: jsonText(json.RawMessage(system))})
	}
	for _, m := range in {
		msg := partsMessage(m.Role, m.Parts)
		msg.FinishReason = m.FinishReason
		out = append(out, msg)
	}
	return out
}

func partsMessage(role string, parts []json.RawMessage) message {
	msg := message{Role: role}
	var texts, reasoning []string
	for _, raw := range parts {
		var p part
		_ = json.Unmarshal(raw, &p) // not an object: no type, shown as sent
		switch {
		case p.Type == "text", p.Type == "" && p.Text != "":
			texts = append(texts, p.Content+p.Text)
		case p.Type == "blob":
			texts = append(texts, mediaNote(cmp.Or(p.Modality, "blob"), p.MimeType, p.Content))
		case p.Type == "thinking", p.Type == "reasoning": // Pydantic AI says thinking, the GenAI conventions reasoning
			reasoning = append(reasoning, p.Content+p.Text)
		case p.Type == "tool_call":
			msg.ToolCalls = append(msg.ToolCalls, toolCall{ID: p.ID, Name: p.Name, Arguments: jsonText(p.Arguments)})
		case p.Type == "tool_call_response":
			// Anthropic's convention sends a tool result in a user message.
			if len(parts) == 1 {
				msg.Role = "tool"
			}
			texts = append(texts, jsonText(p.Response)+jsonText(p.Result))
		default: // a part type not known here is shown as sent, never dropped
			texts = append(texts, string(raw))
		}
	}
	msg.Content = strings.Join(texts, "\n\n")
	msg.Reasoning = strings.Join(reasoning, "\n\n")
	return msg
}

// jsonText is a JSON string's text, or any other JSON value as written.
func jsonText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	return string(raw)
}

// mediaNote says what inline base64 media is, by kind, type and size: the bytes
// are of no use to a reader and can be megabytes.
func mediaNote(kind, mime, b64 string) string {
	n := len(b64)*3/4 - strings.Count(b64[max(len(b64)-2, 0):], "=")
	return "[" + kind + ": " + mime + ", " + strconv.Itoa(n) + " bytes]"
}

// imageNote is mediaNote for an image given as a data URL; any other URL is shown as it is.
func imageNote(url string) string {
	if head, data, ok := strings.Cut(url, ";base64,"); ok && strings.HasPrefix(head, "data:") {
		return mediaNote("image", strings.TrimPrefix(head, "data:"), data)
	}
	return "[image: " + url + "]"
}

// joinBlocks is a message's content blocks, in index order, as one text.
func joinBlocks(blocks map[int]string) string {
	var out []string
	for _, i := range slices.Sorted(maps.Keys(blocks)) {
		out = append(out, blocks[i])
	}
	return strings.Join(out, "\n\n")
}
