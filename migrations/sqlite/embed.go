// Package migrations embeds the SQLite schema migrations, so the binary
// never depends on this directory existing on disk.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
