package sqlite

import "fmt"

// OpenReadOnly opens an existing database with mode=ro: SQLite itself
// refuses every write to the file. The MCP endpoint and `spoor export` read
// through it.
func OpenReadOnly(path string) (*Store, error) {
	st, err := open(path, "mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err != nil {
		return nil, fmt.Errorf("opening read-only (does the database exist? run `spoor migrate`): %w", err)
	}
	return st, nil
}
