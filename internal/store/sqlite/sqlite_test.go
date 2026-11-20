package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

// Only a migration creates a database: opening a missing path, read-write or
// read-only, creates nothing and says so.
func TestOpenRefusesAMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	for name, openFn := range map[string]func(string) (*Store, error){"Open": Open, "OpenReadOnly": OpenReadOnly} {
		if _, err := openFn(path); err == nil || !strings.Contains(err.Error(), "no database at") {
			t.Errorf("%s on a missing file: %v, want an error saying there is no database", name, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s created the file (stat: %v)", name, err)
		}
	}
}

// A file at another schema version is refused, read-write or read-only, with
// what to do about it: version 0 has no spoor schema (a mistyped path, or a
// migration that never ran), and a newer one needs a newer spoor, which
// migrating cannot give either.
func TestOpenRefusesAnotherSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spoor.db")
	_, latest, err := MigrateVersions(path)
	if err != nil {
		t.Fatalf("MigrateVersions: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, c := range []struct {
		version uint
		want    string
	}{{0, "no spoor schema"}, {latest + 1, "use a newer spoor"}} {
		if _, err := db.Exec(fmt.Sprintf("PRAGMA user_version = %d", c.version)); err != nil {
			t.Fatal(err)
		}
		for name, openFn := range map[string]func(string) (*Store, error){"Open": Open, "OpenReadOnly": OpenReadOnly} {
			if st, err := openFn(path); err == nil || !strings.Contains(err.Error(), c.want) {
				if st != nil {
					_ = st.Close()
				}
				t.Errorf("%s at schema version %d: %v, want an error saying %q", name, c.version, err, c.want)
			}
		}
		if c.version > latest {
			if err := Migrate(path); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Migrate at schema version %d: %v, want an error saying %q", c.version, err, c.want)
			}
		}
	}
}

// A refused file is left as it was: Open's journal_mode(WAL) runs only once
// the version is checked.
func TestOpenLeavesARefusedFileAlone(t *testing.T) {
	path := filepath.Join(t.TempDir(), "other.db")
	journalMode := func(q string) string {
		t.Helper()
		db, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = db.Close() }()
		var m string
		if err := db.QueryRow(q).Scan(&m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if got := journalMode(`PRAGMA journal_mode = DELETE`); got != "delete" { // creates a file at version 0
		t.Fatalf("journal_mode = %q", got)
	}
	if _, err := Open(path); err == nil {
		t.Fatal("Open on a file at version 0 succeeded")
	}
	if got := journalMode(`PRAGMA journal_mode`); got != "delete" {
		t.Errorf("journal_mode after a refused Open = %q, want delete", got)
	}
}
