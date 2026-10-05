package sqlite

import (
	"bytes"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io"
)

// packAttributes is the stored form of spans.attributes: the JSON,
// zlib-compressed. Prompts make it large and it compresses about 3.5 to 1;
// nothing queries it, so it is opaque to SQL. nil (SQL NULL) when empty.
func packAttributes(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var b bytes.Buffer
	w := zlib.NewWriter(&b)
	_, _ = w.Write(raw) // a bytes.Buffer does not fail
	_ = w.Close()
	return b.Bytes()
}

func unpackAttributes(packed []byte) (json.RawMessage, error) {
	if len(packed) == 0 {
		return nil, nil
	}
	r, err := zlib.NewReader(bytes.NewReader(packed))
	if err != nil {
		return nil, fmt.Errorf("reading span attributes: %w", err)
	}
	defer func() { _ = r.Close() }()
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("reading span attributes: %w", err)
	}
	return raw, nil
}
