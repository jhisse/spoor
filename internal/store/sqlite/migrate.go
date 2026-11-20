package sqlite

import (
	"database/sql"
	"errors"
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
// as the migration it names: a killed migration leaves nothing half done. A
// file at a version above the last migration is refused, from and to both
// that version.
func MigrateVersions(path string) (from, to uint, err error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return 0, 0, fmt.Errorf("opening sqlite database: %w", err)
	}
	defer func() { _ = db.Close() }()

	if err := db.QueryRow(`PRAGMA user_version`).Scan(&from); err != nil {
		return 0, 0, fmt.Errorf("reading schema version: %w", err)
	}
	files, err := migrationFiles()
	if err != nil {
		return 0, 0, err
	}
	if latest := uint(len(files)); from > latest {
		return from, from, newerSchema(from, latest)
	}
	for i, name := range files {
		if to = uint(i + 1); to > from {
			if err := applyMigration(db, name, to); err != nil {
				return from, to - 1, fmt.Errorf("applying %s: %w", name, err)
			}
		}
	}
	return from, max(to, from), nil
}

// migrationFiles lists the migrations in order: the Nth file is version N.
func migrationFiles() ([]string, error) {
	return fs.Glob(migrations.FS, "*.up.sql")
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

// checkVersion refuses a file whose schema is not the one this binary
// migrates to: its queries would fail on a missing or renamed column.
func checkVersion(dsn string) error {
	db, err := sql.Open("sqlite", dsn+"?mode=ro&_pragma=busy_timeout(5000)")
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()
	var version uint
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return fmt.Errorf("reading schema version: %w", err)
	}
	files, err := migrationFiles()
	if err != nil {
		return err
	}
	switch want := uint(len(files)); {
	case version == 0:
		return errors.New("no spoor schema in this file: check the path, or run `spoor migrate`")
	case version < want:
		return fmt.Errorf("schema version %d, this spoor needs %d: run `spoor migrate`", version, want)
	case version > want:
		return newerSchema(version, want)
	}
	return nil
}

func newerSchema(version, latest uint) error {
	return fmt.Errorf("schema version %d is newer than this spoor reads (up to %d): use a newer spoor", version, latest)
}
