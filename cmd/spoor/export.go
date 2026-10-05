package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/jhisse/spoor/internal/config"
	"github.com/jhisse/spoor/internal/store"
	"github.com/jhisse/spoor/internal/store/sqlite"
	"github.com/jhisse/spoor/internal/web"
)

func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	traceID := fs.String("trace", "", "id of the trace to export")
	sessionID := fs.String("session", "", "id of the session to export")
	html := fs.Bool("html", false, "write one self-contained HTML page instead of JSONL")
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), `usage: spoor export --trace=<id> [--html]
       spoor export --session=<id> [--html]

Writes a trace, or every trace of a session, to standard output, complete.
Default is JSONL: one span per line, every spans column plus service,
session_id, trace_name and trace_status (docs/schema.md). --html writes one
page that can be shared as a file: no script, no external request.
`)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if (*traceID == "") == (*sessionID == "") {
		fs.Usage()
		return errors.New("export: give either --trace or --session")
	}

	path, err := config.SQLitePath()
	if err != nil {
		return err
	}
	ro, err := sqlite.OpenReadOnly(path)
	if err != nil {
		return err
	}
	defer func() { _ = ro.Close() }()
	traces, spans, err := exportData(context.Background(), ro, *traceID, *sessionID)
	if err != nil {
		return err
	}
	if *html {
		return web.ExportHTML(os.Stdout, traces, spans)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false) // prompts are full of <tags>; < helps nobody here
	for i, t := range traces {
		for _, sp := range spans[i] {
			attrs, _ := shown(string(sp.Attributes), 0)
			row := exportRow{spanRow: newSpanRow(sp, 0), Attributes: attrs, Service: t.Service, SessionID: t.SessionID, TraceName: t.Name, TraceStatus: t.Status}
			if err := enc.Encode(row); err != nil {
				return err
			}
		}
	}
	return nil
}

// exportData reads what to export, each trace with its spans: the trace, or
// every turn of the session.
func exportData(ctx context.Context, st store.Store, traceID, sessionID string) (traces []store.Trace, spans [][]store.Span, err error) {
	ids := []string{traceID}
	if sessionID != "" {
		_, turns, err := st.GetSession(ctx, sessionID)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, nil, fmt.Errorf("export: %w", err)
		}
		ids = ids[:0]
		for _, t := range turns {
			ids = append(ids, t.ID)
		}
	}
	count := 0
	for _, id := range ids {
		t, sp, err := st.GetTraceByID(ctx, id)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, nil, fmt.Errorf("export: %w", err)
		}
		traces, spans, count = append(traces, t), append(spans, sp), count+len(sp)
	}
	if count == 0 {
		return nil, nil, errors.New("export: no spans found for that trace or session")
	}
	return traces, spans, nil
}
