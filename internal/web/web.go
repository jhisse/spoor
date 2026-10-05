// Package web is the UI: server-rendered html/template pages made
// interactive with htmx, styled by one hand-written stylesheet, all
// embedded. There is no JSON API and no login: every route is open, guarded
// only by SameOrigin and by where the port is reachable from.
package web

import (
	"github.com/jhisse/spoor/internal/otlp"
	"github.com/jhisse/spoor/internal/store"
)

// Handlers holds what the UI routes need; serve builds it as a struct literal.
type Handlers struct {
	Store store.Store

	// IngestAddr is the OTLP server's listen address; the empty trace
	// list uses its port to print the exact endpoint to send to.
	IngestAddr string

	// RetentionDays is SPOOR_RETENTION_DAYS, shown on the blind-spots page;
	// 0 = keep forever.
	RetentionDays int

	// Version is the release the binary was built as; "dev" when the build info has none.
	Version string

	// Ingest is the process's OTLP receiver, for its counters of what never
	// reached storage; nil when the process runs no ingest server.
	Ingest *otlp.Handler
}
