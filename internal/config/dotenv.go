package config

import (
	"bufio"
	"os"
	"strings"
)

// loadDotEnv sets SPOOR_* env vars from a KEY=VALUE file without overwriting any
// already set. A missing file is not an error.
func loadDotEnv(path string) error {
	f, err := os.Open(path) // #nosec G304 -- the operator's own .env file, never request-controlled
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if !strings.HasPrefix(key, "SPOOR_") { // a cloned directory's .env must not set GODEBUG or a proxy
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, value)
	}
	return scanner.Err()
}
