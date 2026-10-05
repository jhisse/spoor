package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jhisse/spoor/internal/store"
)

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

// Nullable columns scan straight into the domain types' pointer fields
// (database/sql leaves the pointer nil for NULL) and metadata into its
// json.RawMessage through (*[]byte). Only timestamps need help.

// at scans a timestamp. The driver hands back a time.Time for a column
// declared TIMESTAMP and text for an expression such as MIN(started_at).
type at struct{ dst *time.Time }

func (a at) Scan(src any) (err error) {
	switch v := src.(type) {
	case time.Time:
		*a.dst = v.UTC()
	case string:
		*a.dst, err = time.Parse(timeLayout, v)
	default:
		err = fmt.Errorf("timestamp column holds %T", src)
	}
	return err
}

// timeLayout is the one form a timestamp is stored in: UTC text with always
// nine fractional digits. Fixed width makes text order time order
// (RFC3339Nano drops trailing zeros, which sorts "…:00Z" after "…:00.5Z").
const timeLayout = "2006-01-02T15:04:05.000000000Z"

// formatTime binds a timestamp as timeLayout text, not the driver's native
// time.Time: explicit and portable across backends.
func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func queryAll[T any](ctx context.Context, db *sql.DB, scan func(scanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func notFound(err error, what string) error {
	if errors.Is(err, sql.ErrNoRows) {
		return store.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("querying %s: %w", what, err)
	}
	return nil
}

// nullableText stores empty metadata as SQL NULL, so it reads back as a
// nil json.RawMessage rather than an empty one.
func nullableText(m json.RawMessage) any {
	if len(m) == 0 {
		return nil
	}
	return string(m)
}
