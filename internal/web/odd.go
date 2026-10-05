package web

import (
	"cmp"
	"fmt"
	"math"
	"slices"

	"github.com/jhisse/spoor/internal/store"
)

// oddMin is the fewest traces of one service and name a trace is measured
// against; with fewer there is no typical trace to be far from.
const oddMin = 5

// oddView is what ?sort=odd adds to the list: how many traces were ranked
// among, how many of them had no basis, and each ranked trace's sentence.
type oddView struct {
	OddPool  int // the newest traces ranked among
	Unranked int // of them, those with fewer than oddMin of the same name, listed last
	Why      map[string]string
}

// page is what the list shows of the traces QueryTraces returned: under
// ?sort=odd the pool is ranked, cut to p.Limit and has no next page.
func (p listParams) page(summaries []store.TraceSummary, next *store.Cursor) ([]store.TraceSummary, *store.Cursor, oddView) {
	if !p.Odd {
		return summaries, next, oddView{}
	}
	o := oddView{OddPool: len(summaries)}
	o.Why, o.Unranked = oddRank(summaries)
	return summaries[:min(len(summaries), p.Limit)], nil, o
}

type oddMeasure struct {
	name  string
	value func(store.TraceSummary) float64 // 0 = unknown
}

var oddMeasures = []oddMeasure{
	{"cost", func(t store.TraceSummary) float64 { return store.Deref(t.TotalCostUSD) }},
	{"duration", func(t store.TraceSummary) float64 { return t.EndedAt.Sub(t.StartedAt).Seconds() }},
	{"tokens", func(t store.TraceSummary) float64 { return float64(t.TotalInputTokens + t.TotalOutputTokens) }},
}

// oddRank reorders traces by how far each sits from the typical trace of
// the same service and name among them: the largest ratio, over or under,
// of its cost, duration or token total to that group's median. The
// returned sentences name each ranked trace's measure and ratio. A trace
// with fewer than oddMin peers is unranked and keeps its order after the
// ranked ones; their count is returned.
func oddRank(traces []store.TraceSummary) (why map[string]string, unranked int) {
	groups := map[string][]int{}
	for i, t := range traces {
		k := store.Deref(t.Service) + "\x00" + t.Name
		groups[k] = append(groups[k], i)
	}
	why = map[string]string{}
	score := map[string]float64{} // by trace id; an unranked trace is below every ranked one
	for _, idx := range groups {
		if len(idx) < oddMin {
			unranked += len(idx)
			for _, i := range idx {
				score[traces[i].ID] = -1
			}
			continue
		}
		for _, m := range oddMeasures {
			oddScore(traces, idx, m, score, why)
		}
	}
	slices.SortStableFunc(traces, func(a, b store.TraceSummary) int { return cmp.Compare(score[b.ID], score[a.ID]) })
	return why, unranked
}

// oddScore raises each trace of the group (idx) to its distance from the
// group's median of m, when at least oddMin of them have a value.
func oddScore(traces []store.TraceSummary, idx []int, m oddMeasure, score map[string]float64, why map[string]string) {
	var vals []float64
	for _, i := range idx {
		if v := m.value(traces[i]); v > 0 {
			vals = append(vals, v)
		}
	}
	if len(vals) < oddMin {
		return
	}
	slices.Sort(vals)
	median := vals[len(vals)/2]
	for _, i := range idx {
		v := m.value(traces[i])
		if d := math.Abs(math.Log(v / median)); v > 0 && d > score[traces[i].ID] {
			score[traces[i].ID] = d
			why[traces[i].ID] = fmt.Sprintf("%.2g× the typical %s · %d of this name", v/median, m.name, len(vals))
		}
	}
}
