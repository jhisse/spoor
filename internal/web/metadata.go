package web

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
)

// metadataNode is one entry of a trace's or span's metadata, rendered as a
// collapsible tree. Value is set only for a leaf; Children for a key whose
// value is a JSON object, or whose name shares a dotted prefix with a
// sibling key.
type metadataNode struct {
	Key      string
	Value    string
	Children []metadataNode
}

// buildMetadataTree turns metadata JSON into a tree, with no per-key
// mapping. Nil when raw is not a JSON object or is empty: the caller falls
// back to a pretty-printed dump.
func buildMetadataTree(raw json.RawMessage) []metadataNode {
	obj, ok := asObject(raw)
	if !ok || len(obj) == 0 {
		return nil
	}
	return nestByPrefix(obj, 0)
}

// maxKeyDepth is how many dots of one key become nesting. A key is the
// sender's: one made of a million dots must not become a million levels.
const maxKeyDepth = 16

// nestByPrefix nests a flat attribute map on two axes. By dotted key
// prefix: OTel attribute names are namespaced paths, so "gen_ai.system" and
// "gen_ai.usage.*" share one "gen_ai" group. And, within a leaf, by the
// value's own JSON structure (buildMetadataLeaf).
func nestByPrefix(flat map[string]json.RawMessage, depth int) []metadataNode {
	groups := make(map[string]map[string]json.RawMessage)
	var nodes []metadataNode

	for key, raw := range flat {
		prefix, rest, hasDot := strings.Cut(key, ".")
		if !hasDot || depth >= maxKeyDepth {
			nodes = append(nodes, buildMetadataLeaf(key, raw))
			continue
		}
		if groups[prefix] == nil {
			groups[prefix] = make(map[string]json.RawMessage)
		}
		groups[prefix][rest] = raw
	}
	for prefix, children := range groups {
		nodes = append(nodes, metadataNode{Key: prefix, Children: nestByPrefix(children, depth+1)})
	}

	sort.Slice(nodes, func(i, j int) bool { return nodes[i].Key < nodes[j].Key })
	return nodes
}

// buildMetadataLeaf expands a value into a nested group when it is
// expandable (expandableChildren), in two shapes: raw is JSON itself
// (traces.metadata's {"resource": {...}}); raw is a JSON string whose
// content is JSON (OpenLLMetry's tool-schema and CrewAI agent attributes).
// Anything else is a leaf.
func buildMetadataLeaf(key string, raw json.RawMessage) metadataNode {
	if children, ok := expandableChildren(raw); ok {
		return metadataNode{Key: key, Children: children}
	}

	s := scalarString(raw)
	if children, ok := expandableChildren(json.RawMessage(s)); ok {
		return metadataNode{Key: key, Children: children}
	}

	return metadataNode{Key: key, Value: prettyJSON(s)}
}

// expandableChildren returns tree children for raw if it is a non-empty
// JSON object, or a JSON array whose every element is an object. An array
// of scalars reads fine as leaf text. ok=false leaves raw as a leaf value.
func expandableChildren(raw json.RawMessage) ([]metadataNode, bool) {
	if obj, ok := asObject(raw); ok && len(obj) > 0 {
		return nestByPrefix(obj, 0), true
	}
	nodes := arrayNodes(raw)
	return nodes, len(nodes) > 0
}

// arrayNodes renders a JSON array whose every element is an object, each
// under its index in array order (object keys are sorted; these are not).
// Nil for anything else.
func arrayNodes(raw json.RawMessage) []metadataNode {
	var arr []json.RawMessage
	if json.Unmarshal(raw, &arr) != nil {
		return nil
	}
	nodes := make([]metadataNode, len(arr))
	for i, item := range arr {
		obj, ok := asObject(item)
		if !ok {
			return nil
		}
		nodes[i] = metadataNode{Key: strconv.Itoa(i), Children: nestByPrefix(obj, 0)}
	}
	return nodes
}

func asObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil {
		return nil, false
	}
	return obj, true
}

// scalarString reads v as a JSON string, else returns its compact JSON text.
func scalarString(v json.RawMessage) string {
	var s string
	if err := json.Unmarshal(v, &s); err == nil {
		return s
	}
	return string(v)
}
