package sqlite

// OpenReadOnly opens an existing database with mode=ro: SQLite itself
// refuses every write to the file. The MCP endpoint, `spoor export` and
// `spoor reprice --dry-run` read through it.
func OpenReadOnly(path string) (*Store, error) {
	return open(path, "mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)")
}
