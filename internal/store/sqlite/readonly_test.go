package sqlite

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

func TestOpenReadOnlyMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.db")
	if _, err := OpenReadOnly(path); err == nil {
		t.Fatal("OpenReadOnly on a missing file succeeded")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("OpenReadOnly created the file")
	}
}

// The MCP endpoint and `spoor export` read through this Store: SQLite itself
// refuses every write, and what was stored stays readable.
func TestOpenReadOnlyReadsAndRefusesWrites(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spoor.db")
	if err := Migrate(path); err != nil {
		t.Fatal(err)
	}
	rw, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	tr := store.Trace{ID: "t1", Name: "run", StartedAt: now, EndedAt: now, Status: store.StatusOK, CreatedAt: now}
	if err := rw.MergeTrace(t.Context(), tr, true); err != nil {
		t.Fatal(err)
	}
	_ = rw.Close()

	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ro.Close() }()
	if got, err := ro.GetTraceSummary(t.Context(), "t1"); err != nil || got.Name != "run" {
		t.Errorf("read through the read-only store: %+v, %v", got, err)
	}
	tr.ID = "t2"
	if err := ro.MergeTrace(t.Context(), tr, true); err == nil || !strings.Contains(err.Error(), "readonly") {
		t.Errorf("a write through the read-only store: %v, want a readonly refusal", err)
	}
}
