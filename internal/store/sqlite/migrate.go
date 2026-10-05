package sqlite

import (
	"database/sql"
	"fmt"
	"io/fs"
	"strconv"

	migrations "github.com/jhisse/spoor/migrations/sqlite"
)

func Migrate(path string) error {
	_, _, err := MigrateVersions(path)
	return err
}

// MigrateVersions is Migrate, also reporting the schema version before
// and after (0 = empty database) so `serve` can log what it applied. The
// version is the file's own PRAGMA user_version, set in the same transaction
// as the migration it names: a killed migration leaves nothing half done.
func MigrateVersions(path string) (from, to uint, err error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, 0, fmt.Errorf("opening sqlite database: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.QueryRow(`PRAGMA user_version`).Scan(&from); err != nil {
		return 0, 0, fmt.Errorf("reading schema version: %w", err)
	}
	files, err := fs.Glob(migrations.FS, "*.up.sql") // sorted; the Nth file is version N
	if err != nil {
		return 0, 0, err
	}
	for i, name := range files {
		if to = uint(i + 1); to > from {
			if err := applyMigration(db, name, to); err != nil {
				return from, to - 1, fmt.Errorf("applying %s: %w", name, err)
			}
		}
	}
	if err := syncModelPriceCatalog(db); err != nil {
		return from, to, fmt.Errorf("syncing model price catalog: %w", err)
	}
	return from, max(to, from), nil
}

func applyMigration(db *sql.DB, name string, version uint) error {
	script, err := fs.ReadFile(migrations.FS, name)
	if err != nil {
		return err
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	// #nosec G202 -- version is parsed from an embedded file name.
	if _, err := tx.Exec(string(script) + "\nPRAGMA user_version = " + strconv.FormatUint(uint64(version), 10)); err != nil {
		return err
	}
	return tx.Commit()
}
