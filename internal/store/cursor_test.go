package store

import (
	"testing"
	"time"
)

func TestCursorRoundTrip(t *testing.T) {
	want := Cursor{StartedAt: time.Now().UTC().Truncate(time.Nanosecond), ID: "trace-123"}

	got, err := DecodeCursor(EncodeCursor(want))
	if err != nil {
		t.Fatalf("DecodeCursor: %v", err)
	}
	if !got.StartedAt.Equal(want.StartedAt) || got.ID != want.ID {
		t.Errorf("DecodeCursor(EncodeCursor(c)) = %+v, want %+v", got, want)
	}
}

func TestDecodeCursorRejectsMalformedInput(t *testing.T) {
	if _, err := DecodeCursor("not-valid-base64!!"); err == nil {
		t.Error("DecodeCursor: want error for malformed input, got nil")
	}
}
