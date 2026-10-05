package web

import (
	"slices"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func strPtr(s string) *string { return &s }

// An orphan span appears at the root of the tree instead of disappearing.
func TestBuildSpanTreeOrphanRendersAtRoot(t *testing.T) {
	now := time.Now()
	root := store.Span{ID: "root", Name: "root", StartedAt: now}
	child := store.Span{ID: "child", Name: "child", ParentSpanID: strPtr("root"), StartedAt: now.Add(time.Second)}
	orphan := store.Span{ID: "orphan", Name: "orphan", ParentSpanID: strPtr("does-not-exist"), StartedAt: now.Add(2 * time.Second)}

	roots := BuildSpanTree([]store.Span{root, child, orphan})

	if len(roots) != 2 {
		t.Fatalf("BuildSpanTree returned %d roots, want 2 (root, orphan): %+v", len(roots), roots)
	}
	if roots[0].Span.ID != "root" || roots[1].Span.ID != "orphan" {
		t.Fatalf("roots = [%s, %s], want [root, orphan] (ordered by StartedAt)", roots[0].Span.ID, roots[1].Span.ID)
	}
	if len(roots[0].Children) != 1 || roots[0].Children[0].Span.ID != "child" {
		t.Errorf("root's children = %+v, want [child]", roots[0].Children)
	}
	if len(roots[1].Children) != 0 {
		t.Errorf("orphan's children = %+v, want none", roots[1].Children)
	}
}

// A grandchild's error marks HasError on its parent and on the root, though
// neither is itself in error; a sibling branch with no error stays false.
func TestBuildSpanTreeHasErrorPropagatesUpward(t *testing.T) {
	now := time.Now()
	root := store.Span{ID: "root", Name: "root", Status: store.StatusOK, StartedAt: now}
	child := store.Span{ID: "child", Name: "child", ParentSpanID: strPtr("root"), Status: store.StatusOK, StartedAt: now.Add(time.Second)}
	grandchild := store.Span{ID: "grandchild", Name: "grandchild", ParentSpanID: strPtr("child"), Status: store.StatusError, StartedAt: now.Add(2 * time.Second)}
	okSibling := store.Span{ID: "ok-sibling", Name: "ok-sibling", ParentSpanID: strPtr("root"), Status: store.StatusOK, StartedAt: now.Add(3 * time.Second)}

	roots := BuildSpanTree([]store.Span{root, child, grandchild, okSibling})
	if len(roots) != 1 {
		t.Fatalf("BuildSpanTree returned %d roots, want 1", len(roots))
	}
	rootNode := roots[0]
	if !rootNode.HasError {
		t.Error("root.HasError = false, want true (error is a descendant)")
	}

	var childNode, siblingNode, grandchildNode *SpanNode
	for _, c := range rootNode.Children {
		switch c.Span.ID {
		case "child":
			childNode = c
		case "ok-sibling":
			siblingNode = c
		}
	}
	if childNode == nil || siblingNode == nil {
		t.Fatalf("expected both child and ok-sibling nodes, got children: %+v", rootNode.Children)
	}
	if !childNode.HasError {
		t.Error("child.HasError = false, want true (grandchild is error)")
	}
	if siblingNode.HasError {
		t.Error("ok-sibling.HasError = true, want false (no error in this branch)")
	}

	grandchildNode = childNode.Children[0]
	if !grandchildNode.HasError {
		t.Error("grandchild.HasError = false, want true (it's the error itself)")
	}
}

func TestBuildSpanTreeOrdersChildrenByStartedAt(t *testing.T) {
	now := time.Now()
	root := store.Span{ID: "root", Name: "root", StartedAt: now}
	second := store.Span{ID: "second", Name: "second", ParentSpanID: strPtr("root"), StartedAt: now.Add(2 * time.Second)}
	first := store.Span{ID: "first", Name: "first", ParentSpanID: strPtr("root"), StartedAt: now.Add(time.Second)}

	roots := BuildSpanTree([]store.Span{root, second, first})

	if len(roots) != 1 {
		t.Fatalf("BuildSpanTree returned %d roots, want 1", len(roots))
	}
	children := roots[0].Children
	if len(children) != 2 || children[0].Span.ID != "first" || children[1].Span.ID != "second" {
		t.Errorf("children = %+v, want [first, second] (chronological order)", children)
	}
}

// Pre-order, the order span_row renders the tree in, on a two-level tree
// with siblings at each level.
func TestFlattenPreOrder(t *testing.T) {
	now := time.Now()
	root := store.Span{ID: "root", Name: "root", StartedAt: now}
	childA := store.Span{ID: "childA", Name: "childA", ParentSpanID: strPtr("root"), StartedAt: now.Add(time.Second)}
	grandchild := store.Span{ID: "grandchild", Name: "grandchild", ParentSpanID: strPtr("childA"), StartedAt: now.Add(2 * time.Second)}
	childB := store.Span{ID: "childB", Name: "childB", ParentSpanID: strPtr("root"), StartedAt: now.Add(3 * time.Second)}

	roots := BuildSpanTree([]store.Span{root, childA, grandchild, childB})
	var got []string
	for _, n := range flatten(roots) {
		got = append(got, n.Span.ID)
	}
	if want := []string{"root", "childA", "grandchild", "childB"}; !slices.Equal(got, want) {
		t.Errorf("flatten = %v, want %v", got, want)
	}
}
