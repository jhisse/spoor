package web

import (
	"html/template"

	"github.com/jhisse/spoor/internal/store"
)

// Collapse by interest starts above foldMinSpans: a trace up to that size
// fits about one screen and is shown whole. A run folds from foldMinRun
// siblings on: two would save one row and cost a click.
const (
	foldMinSpans = 25
	foldMinRun   = 3
)

// treeRow is what "span_row" renders: a span, or — when Run is set — a ×n
// row for consecutive siblings of one kind and name, whose Node is
// synthetic (the run's extent in time, the Σ of its members' usage).
type treeRow struct {
	Node       *SpanNode
	TraceID    string
	SelectedID string
	Scale      int64           // token-bar scale, see detailPageData.TokenScale
	Keep       template.URL    // what a row link carries on: see detailPageData.Keep
	Hits       map[string]bool // the spans the search describes
	Unfolded   bool            // ?fold=0: everything was asked for
	Rootless   bool            // no root arrived: the page says so once, not on every row
	Open       bool            // children are rendered expanded
	Below      int             // number of descendants
	Items      []treeRow       // children, after run folding
	Run        []treeRow
}

// foldStats counts what the folded tree hides, by rule.
type foldStats struct{ Total, Collapsed, Folded, Runs int }

func (s foldStats) Shown() int { return s.Total - s.Collapsed - s.Folded }

type folder struct {
	proto treeRow // TraceID, SelectedID, Scale, Keep, Hits and Rootless, copied to every row
	on    bool
	path  map[string]bool // the selected span, the search's spans and their ancestors
	stats foldStats
}

// foldTree turns the span forest into the tree's rows. With fold, a row
// stays open only when it is of interest — on the way to the selected span,
// or with an error at or below it — and a run of uninteresting siblings of
// one kind and name becomes one ×n row.
func foldTree(spans []store.Span, roots []*SpanNode, proto treeRow, fold bool) ([]treeRow, foldStats) {
	parent := make(map[string]*string, len(spans))
	for _, sp := range spans {
		parent[sp.ID] = sp.ParentSpanID
	}
	proto.Rootless = rootless(roots)
	f := &folder{proto: proto, on: fold, path: map[string]bool{}, stats: foldStats{Total: len(spans)}}
	open := func(id *string) {
		for ; id != nil && !f.path[*id]; id = parent[*id] {
			f.path[*id] = true
		}
	}
	open(&proto.SelectedID)
	for id := range proto.Hits {
		open(&id)
	}
	rows, _ := f.items(roots, false)
	return rows, f.stats
}

func (f *folder) interesting(n *SpanNode) bool { return f.path[n.Span.ID] || n.HasError }

func (f *folder) runEnd(children []*SpanNode, i int) int {
	j := i
	for f.on && j < len(children) && !f.interesting(children[j]) &&
		children[j].Span.Kind == children[i].Span.Kind && children[j].Span.Name == children[i].Span.Name {
		j++
	}
	if j-i < foldMinRun {
		return i + 1
	}
	return j
}

// items builds one sibling list; hidden: it is below a collapsed row or in a ×n row.
func (f *folder) items(children []*SpanNode, hidden bool) (rows []treeRow, below int) {
	for i := 0; i < len(children); {
		j := f.runEnd(children, i)
		run := j-i > 1
		var members []treeRow
		for _, n := range children[i:j] {
			r := f.proto
			r.Node, r.Open = n, (!f.on || f.interesting(n)) && !f.phasesClosed(n)
			r.Items, r.Below = f.items(n.Children, hidden || run || !r.Open)
			below += 1 + r.Below
			if hidden {
				f.stats.Collapsed++
			} else if run {
				f.stats.Folded++
			}
			members = append(members, r)
		}
		if run {
			if !hidden {
				f.stats.Runs++
			}
			r := f.proto
			r.Node, r.Run = runNode(children[i:j]), members
			members = []treeRow{r}
		}
		rows, i = append(rows, members...), j
	}
	return rows, below
}

// runNode adopts the run as children, so rollUp gives it a parent's Σ.
func runNode(run []*SpanNode) *SpanNode {
	first, last := run[0], run[len(run)-1]
	n := &SpanNode{Span: first.Span, Children: run, Offset: first.Offset, Width: last.Offset + last.Width - first.Offset}
	n.Span.EndedAt = last.Span.EndedAt
	rollUp(n)
	return n
}
