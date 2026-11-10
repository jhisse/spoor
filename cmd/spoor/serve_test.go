package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

type sweepRecorder struct {
	store.Store
	cutoffs chan time.Time
}

func (s sweepRecorder) SweepExpired(_ context.Context, cutoff time.Time) (int64, int64, error) {
	s.cutoffs <- cutoff
	return 0, 0, nil
}

// A process that lives less than one interval must still apply the
// retention: the first sweep runs at the start, not one interval later.
func TestSweepLoopSweepsAtStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	st := sweepRecorder{cutoffs: make(chan time.Time, 1)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runSweepLoop(ctx, st, 30, time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()

	select {
	case cutoff := <-st.cutoffs:
		want := time.Now().AddDate(0, 0, -30)
		if d := want.Sub(cutoff); d < 0 || d > time.Minute {
			t.Errorf("cutoff = %v, want 30 days before now (%v)", cutoff, want)
		}
	case <-time.After(5 * time.Second):
		t.Error("no sweep within 5s of the start, with a 1h interval")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the loop did not stop when its context was cancelled")
	}
}
