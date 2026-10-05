package main

import (
	"flag"
	"fmt"

	"github.com/jhisse/spoor/internal/config"
	"github.com/jhisse/spoor/internal/store/sqlite"
)

func runMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ExitOnError)
	fs.Usage = func() {
		_, _ = fmt.Fprint(fs.Output(), `usage: spoor migrate

Applies pending SQLite schema migrations (migrations/sqlite/) to the
database at SPOOR_SQLITE_PATH.
`)
	}
	_ = fs.Parse(args) // ExitOnError: never returns an error

	path, err := config.SQLitePath()
	if err != nil {
		return err
	}

	if err := sqlite.Migrate(path); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	fmt.Println("migrate: applied pending migrations to", path)
	return nil
}
