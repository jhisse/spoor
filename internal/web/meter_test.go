package web

import (
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// Only the live list carries the meter. It is the cost of the traces
// started in the last ten minutes, six times, and names the traces that
// have no cost instead of pricing them at zero.
func TestLiveListShowsTheSpendingRate(t *testing.T) {
	s := newTestStore(t)
	h := newTestHandlers(t, s)
	if body := doList(t, h, "live=1").Body.String(); !strings.Contains(body, "nothing in the last 10 minutes") {
		t.Errorf("empty live list should say the window is empty")
	}

	now, cost := time.Now(), 0.05
	seedServiceTrace(t, s, "acme", "costed", now.Add(-time.Minute))
	seedServiceTrace(t, s, "acme", "free", now.Add(-time.Minute))
	seedServiceTrace(t, s, "acme", "old", now.Add(-time.Hour))
	for _, id := range []string{"costed", "old"} {
		sp := seedSpan(t, s, id, id+"-span", nil, now.Add(-time.Minute))
		sp.CostUSD = &cost
		if err := s.UpdateSpanUsage(t.Context(), []store.Span{sp}); err != nil {
			t.Fatalf("UpdateSpanUsage: %v", err)
		}
	}
	body := doList(t, h, "live=1").Body.String()
	if !strings.Contains(body, "$0.3000/h now</b> over 2 traces, 1 without a cost") {
		t.Errorf("live list missing the meter, got: %s", body)
	}
	if body := doList(t, h, "").Body.String(); strings.Contains(body, "/h now") {
		t.Errorf("the meter belongs to the live list only")
	}
}
