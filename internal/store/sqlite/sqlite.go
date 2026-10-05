// Package sqlite is the default Store adapter: SQLite via
// modernc.org/sqlite, a pure-Go driver with no cgo, so the binary
// cross-compiles without a C toolchain.
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"

	_ "modernc.org/sqlite"

	"github.com/jhisse/spoor/internal/store"
)

type Store struct {
	writer
	db *sql.DB
}

var _ store.Store = (*Store)(nil)

// Open opens (creating if absent) the SQLite database at path in WAL
// mode (the default journal blocks readers during a write). Pragmas go in
// the DSN, not a PRAGMA Exec: database/sql pools connections and an Exec
// reaches only one. busy_timeout rides out the brief contention of two
// writers.
func Open(path string) (*Store, error) {
	// _txlock=immediate: every transaction here writes, and a deferred one
	// that reads first (the full-text triggers do) fails at once with
	// SQLITE_BUSY when another writer commits in between, without ever
	// waiting busy_timeout. BEGIN IMMEDIATE takes the write lock up front.
	return open(path, "_txlock=immediate&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
}

func open(path, params string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+url.PathEscape(path)+"?"+params)
	if err != nil {
		return nil, fmt.Errorf("opening sqlite database: %w", err)
	}
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("connecting to sqlite database %s: %w", path, err)
	}
	return &Store{writer{db}, db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}
