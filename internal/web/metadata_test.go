package web

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

// Keys sharing a dotted prefix (OTel attribute names are namespaced paths)
// group under that prefix.
func TestBuildMetadataTreeGroupsByDottedPrefix(t *testing.T) {
	raw := json.RawMessage(`{"gen_ai.system":"OpenAI","llm.request.type":"chat"}`)
	got := buildMetadataTree(raw)
	want := []metadataNode{
		{Key: "gen_ai", Children: []metadataNode{{Key: "system", Value: "OpenAI"}}},
		{Key: "llm", Children: []metadataNode{
			{Key: "request", Children: []metadataNode{{Key: "type", Value: "chat"}}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildMetadataTree = %+v, want %+v", got, want)
	}
}

// Keys sharing more than one segment (gen_ai.usage.*) land under one nested
// group.
func TestBuildMetadataTreeGroupsSiblingsUnderSharedPrefix(t *testing.T) {
	raw := json.RawMessage(`{"gen_ai.system":"Anthropic","gen_ai.usage.cache_creation_input_tokens":"0","gen_ai.usage.cache_read_input_tokens":"0"}`)
	got := buildMetadataTree(raw)
	want := []metadataNode{
		{Key: "gen_ai", Children: []metadataNode{
			{Key: "system", Value: "Anthropic"},
			{Key: "usage", Children: []metadataNode{
				{Key: "cache_creation_input_tokens", Value: "0"},
				{Key: "cache_read_input_tokens", Value: "0"},
			}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildMetadataTree = %+v, want %+v", got, want)
	}
}

// traces.metadata's shape, {"resource": {...}}: the inner map's dotted keys
// (service.name) split too.
func TestBuildMetadataTreeNestedResource(t *testing.T) {
	raw := json.RawMessage(`{"resource":{"service.name":"spoor-testdata-capture"}}`)
	got := buildMetadataTree(raw)
	want := []metadataNode{
		{Key: "resource", Children: []metadataNode{
			{Key: "service", Children: []metadataNode{{Key: "name", Value: "spoor-testdata-capture"}}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildMetadataTree = %+v, want %+v", got, want)
	}
}

// The recursion on the value's structure is not limited to one level.
func TestBuildMetadataTreeDeeplyNested(t *testing.T) {
	raw := json.RawMessage(`{"a":{"b":{"c":"leaf"}}}`)
	got := buildMetadataTree(raw)
	want := []metadataNode{
		{Key: "a", Children: []metadataNode{
			{Key: "b", Children: []metadataNode{
				{Key: "c", Value: "leaf"},
			}},
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("buildMetadataTree = %+v, want %+v", got, want)
	}
}

// The shape of llm.request.functions.0.parameters in
// testdata/openllmetry-openai-openrouter-tool-use: a JSON object encoded as
// a string expands into a nested group, under llm > request > functions > 0
// > parameters.
func TestBuildMetadataTreeExpandsDoubleEncodedJSON(t *testing.T) {
	raw := json.RawMessage(`{"llm.request.functions.0.parameters":"{\"type\": \"object\", \"properties\": {\"city\": {\"type\": \"string\"}}, \"required\": [\"city\"]}"}`)
	got := buildMetadataTree(raw)

	// Descend llm -> request -> functions -> 0 -> parameters.
	node := got
	for _, key := range []string{"llm", "request", "functions", "0", "parameters"} {
		if len(node) != 1 || node[0].Key != key {
			t.Fatalf("expected a single %q node, got %+v", key, node)
		}
		node = node[0].Children
	}
	// node is now "parameters"'s children — the expanded schema.
	want := []metadataNode{
		{Key: "properties", Children: []metadataNode{
			{Key: "city", Children: []metadataNode{{Key: "type", Value: "string"}}},
		}},
		{Key: "required", Value: "[\n  \"city\"\n]"},
		{Key: "type", Value: "object"},
	}
	if !reflect.DeepEqual(node, want) {
		t.Errorf("parameters' children = %+v, want %+v", node, want)
	}
}

func TestBuildMetadataTreeSortedKeys(t *testing.T) {
	raw := json.RawMessage(`{"zebra":"1","alpha":"2"}`)
	got := buildMetadataTree(raw)
	if len(got) != 2 || got[0].Key != "alpha" || got[1].Key != "zebra" {
		t.Errorf("buildMetadataTree order = %+v, want alpha before zebra", got)
	}
}

// The shape of crewai.crew.agents in OpenLLMetry's CrewAI instrumentor: a JSON
// array of objects inside a string expands into a tree, one node per list index.
func TestBuildMetadataTreeExpandsJSONStringArrayOfObjects(t *testing.T) {
	agents := `[{"id": "0d1864c5-574c-4d90-ac36-f547a6ab0298", "role": "Researcher", "cache": true, "config": null}, {"id": "12c85417-532b-4484-968e-8841109a44d1", "role": "Writer", "cache": true, "config": null}]`
	raw := json.RawMessage(`{"crewai.crew.agents":` + strconv.Quote(agents) + `}`)

	got := buildMetadataTree(raw)
	// Descend crewai -> crew -> agents (the dot-prefix grouping axis).
	node := got
	for _, key := range []string{"crewai", "crew", "agents"} {
		if len(node) != 1 || node[0].Key != key {
			t.Fatalf("expected a single %q node, got %+v", key, node)
		}
		node = node[0].Children
	}
	// node is now "agents"'s children: one per list index, in order.
	if len(node) != 2 || node[0].Key != "0" || node[1].Key != "1" {
		t.Fatalf("agents children = %+v, want index nodes [0, 1] in order", node)
	}
	roleOf := func(n metadataNode) string {
		for _, c := range n.Children {
			if c.Key == "role" {
				return c.Value
			}
		}
		return ""
	}
	if roleOf(node[0]) != "Researcher" || roleOf(node[1]) != "Writer" {
		t.Errorf("agent roles = [%q, %q], want [Researcher, Writer]", roleOf(node[0]), roleOf(node[1]))
	}
}

// An array of scalars stays a leaf: only an array of objects is expanded.
func TestBuildMetadataTreeArrayOfScalarsStaysLeaf(t *testing.T) {
	raw := json.RawMessage(`{"tags":["a","b","c"]}`)
	got := buildMetadataTree(raw)
	if len(got) != 1 || got[0].Key != "tags" || got[0].Children != nil {
		t.Errorf("buildMetadataTree = %+v, want a single leaf node (no expansion)", got)
	}
}

func TestBuildMetadataTreeEmptyOrNil(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, {}, []byte("null"), []byte("{}")} {
		if got := buildMetadataTree(raw); got != nil {
			t.Errorf("buildMetadataTree(%q) = %+v, want nil", raw, got)
		}
	}
}

// A key is the sender's: one made of dots nests to a limit, not to its length.
func TestMetadataKeyNestingIsBounded(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{strings.Repeat(".", 200_000) + "end": "v"})
	depth := 0
	for nodes := buildMetadataTree(raw); len(nodes) == 1 && nodes[0].Children != nil; nodes = nodes[0].Children {
		depth++
	}
	if depth != maxKeyDepth {
		t.Errorf("nested %d levels, want %d", depth, maxKeyDepth)
	}
}
