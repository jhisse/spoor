package web

import (
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func oddTrace(id, name string, at time.Time, cost float64, seconds int) store.TraceSummary {
	service := "svc"
	return store.TraceSummary{Trace: store.Trace{ID: id, Service: &service, Name: name, StartedAt: at, EndedAt: at.Add(time.Duration(seconds) * time.Second)}, TotalCostUSD: &cost}
}

// Among six traces of one name, the one at ten times the median cost comes
// first and says so; a name with fewer than oddMin traces is unranked and
// listed last in its original order.
func TestOddRankPutsTheFurthestFromTypicalFirst(t *testing.T) {
	t0 := time.Now()
	traces := []store.TraceSummary{oddTrace("lone", "other", t0, 0.01, 1)}
	for i, cost := range []float64{0.01, 0.1, 0.01, 0.01, 0.01, 0.01} {
		traces = append(traces, oddTrace(string(rune('a'+i)), "turn", t0.Add(time.Duration(i)*time.Minute), cost, 1))
	}
	why, unranked := oddRank(traces)
	if traces[0].ID != "b" || traces[len(traces)-1].ID != "lone" || unranked != 1 {
		t.Fatalf("order = %v, unranked = %d", ids(traces), unranked)
	}
	if why["b"] != "10× the typical cost · 6 of this name" || why["lone"] != "" {
		t.Errorf("why = %v", why)
	}
}

func ids(traces []store.TraceSummary) (out []string) {
	for _, t := range traces {
		out = append(out, t.ID)
	}
	return out
}

// ?sort=odd lists the newest traces in that order, with no next page and a
// footer that says how they were ordered; the toggle is on.
func TestListOddFirst(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	t0 := time.Now().Add(-time.Hour)
	for i := 0; i < 6; i++ {
		seedServiceTrace(t, s, "acme", "trace-"+string(rune('a'+i)), t0.Add(time.Duration(i)*time.Minute))
	}
	seedSessionTurn(t, s, "trace-f", "sess-1", t0.Add(6*time.Minute))
	body := doList(t, h, "sort=odd&limit=3").Body.String()
	for _, want := range []string{"Odd first · on", "3 of the newest 6 traces, the furthest from the typical trace", `href="/?limit=3"`} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
	if strings.Contains(body, "Next page") || strings.Contains(body, "Latest sessions") {
		t.Errorf("odd order has no next page and no band of the newest sessions")
	}
}
