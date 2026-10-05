package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
	"github.com/jhisse/spoor/internal/store/storetest"
)

func newTestStore(t *testing.T) store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "spoor.db")
	if err := Migrate(path); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestConformance(t *testing.T) {
	storetest.Run(t, newTestStore)
}

func TestOpenEnablesWALMode(t *testing.T) {
	s := newTestStore(t).(*Store)

	var mode string
	if err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}
}

func TestConcurrentInsertAndQueryDoesNotHang(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const spanCount = 200
	done := make(chan error, 2)

	go func() {
		tr := store.Trace{
			ID:        "trace-concurrent",
			Name:      "trace-concurrent",
			StartedAt: time.Now(),
			EndedAt:   time.Now(),
			Status:    store.StatusOK,
			CreatedAt: time.Now(),
		}
		if err := s.MergeTrace(ctx, tr, true); err != nil {
			done <- err
			return
		}
		for i := 0; i < spanCount; i++ {
			sp := store.Span{
				TraceID:   tr.ID,
				ID:        fmt.Sprintf("span-%d", i),
				Kind:      store.SpanKindGeneric,
				Name:      "span",
				StartedAt: time.Now(),
				EndedAt:   time.Now(),
				Status:    store.StatusOK,
				CreatedAt: time.Now(),
			}
			if err := s.InsertSpan(ctx, sp); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	go func() {
		for i := 0; i < spanCount; i++ {
			if _, _, err := s.QueryTraces(ctx, store.TraceQuery{}); err != nil {
				done <- err
				return
			}
		}
		done <- nil
	}()

	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("concurrent operation failed: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("concurrent insert/query did not complete within 10s (possible hang)")
		}
	}
}

// A fresh file goes from version 0 to the latest; a second run applies
// nothing. `spoor serve` logs these.
func TestMigrateVersionsReportsWhatWasApplied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spoor.db")

	from, to, err := MigrateVersions(path)
	if err != nil {
		t.Fatalf("MigrateVersions: %v", err)
	}
	if from != 0 || to == 0 {
		t.Errorf("fresh database: from=%d to=%d, want from 0 to the latest version", from, to)
	}

	again, same, err := MigrateVersions(path)
	if err != nil {
		t.Fatalf("second MigrateVersions: %v", err)
	}
	if again != to || same != to {
		t.Errorf("second run: from=%d to=%d, want both %d (nothing to apply)", again, same, to)
	}
}
