package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jhisse/spoor/internal/store"
	"github.com/jhisse/spoor/internal/store/sqlite"
)

func TestRepriceDryRunThenReal(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "spoor.db")
	if err := sqlite.Migrate(dbPath); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	st, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = st.Close() }()
	ctx := context.Background()
	now := time.Now().UTC()
	model, in, out, cached, reported := "claude-sonnet-5", int64(1000), int64(10), int64(900), 0.5
	for _, name := range []string{"mine", "other"} {
		if err := st.MergeTrace(ctx, store.Trace{ID: name, Service: &name, Name: name, StartedAt: now, EndedAt: now, Status: store.StatusOK, CreatedAt: now}, true); err != nil {
			t.Fatalf("MergeTrace: %v", err)
		}
		base := store.Span{TraceID: name, Kind: store.SpanKindGeneration, StartedAt: now, EndedAt: now,
			Status: store.StatusOK, CreatedAt: now, Model: &model, InputTokens: &in, OutputTokens: &out}
		unpriced, old, kept := base, base, base
		unpriced.ID, unpriced.CacheReadTokens = "unpriced", &cached
		old.ID, old.StartedAt = "old", now.Add(-48*time.Hour)
		kept.ID, kept.CostUSD, kept.Metadata = "kept", &reported, []byte(`{"llm.cost.total": 0.5}`)
		for _, sp := range []store.Span{unpriced, old, kept} {
			if err := st.InsertSpan(ctx, sp); err != nil {
				t.Fatalf("InsertSpan: %v", err)
			}
		}
	}
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(binPath, append([]string{"reprice"}, args...)...) // #nosec G204 -- the test's own binary and arguments
		cmd.Env = buildEnv([]string{"SPOOR_SQLITE_PATH=" + dbPath})
		outBytes, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("reprice %v: %v\n%s", args, err, outBytes)
		}
		return string(outBytes)
	}
	// priced lists the spans that have a cost, with the cache read tokens on file.
	priced := func() string {
		t.Helper()
		spans, err := st.QuerySpans(ctx, store.SpanQuery{})
		if err != nil {
			t.Fatalf("QuerySpans: %v", err)
		}
		var names []string
		for _, sp := range spans {
			if sp.CostUSD != nil {
				names = append(names, fmt.Sprintf("%s/%s(cache %d)", sp.TraceID, sp.ID, cacheRead(sp)))
			}
		}
		sort.Strings(names)
		return strings.Join(names, " ")
	}
	window := "--from=" + now.Add(-time.Hour).Format(time.RFC3339)
	const reportedOnly = "mine/kept(cache 0) other/kept(cache 0)"

	for _, step := range []struct {
		args                   []string
		wantOutput, wantPriced string
	}{
		{[]string{"--service=mine", window, "--dry-run"}, "1 of 2 spans would change", reportedOnly},
		{[]string{"--service=mine", window}, "1 of 2 spans changed; total cost $0.500000 -> $0.500", "mine/kept(cache 0) mine/unpriced(cache 900) other/kept(cache 0)"},
		// By model: how many spans, the dollars, and the price row before and after.
		{nil, "3 of 6 spans changed; total cost $1.000480 -> $1.005160\n  claude-sonnet-5: 3 spans, $0.000000 -> $0.004680; price row: no recorded row -> (?i)^((anthropic\\/)?claude-sonnet-5|", "mine/kept(cache 0) mine/old(cache 0) mine/unpriced(cache 900) other/kept(cache 0) other/old(cache 0) other/unpriced(cache 900)"},
		// Idempotent: a second run over the same table changes nothing and lists no model.
		{nil, "0 of 6 spans changed; total cost $1.005160 -> $1.005160\n\x00", "mine/kept(cache 0) mine/old(cache 0) mine/unpriced(cache 900) other/kept(cache 0) other/old(cache 0) other/unpriced(cache 900)"},
	} {
		if got := run(step.args...) + "\x00"; !strings.Contains(got, step.wantOutput) { // \x00 marks the end of the output
			t.Errorf("reprice %v printed %q, want it to contain %q", step.args, got, step.wantOutput)
		}
		if got := priced(); got != step.wantPriced {
			t.Errorf("after reprice %v, priced spans = %q, want %q", step.args, got, step.wantPriced)
		}
	}
}

func cacheRead(sp store.Span) int64 {
	if sp.CacheReadTokens == nil {
		return 0
	}
	return *sp.CacheReadTokens
}
