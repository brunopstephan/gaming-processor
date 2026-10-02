//go:build !integration && !e2e

package app

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	got, err := decodeCursor(encodeCursor(id))
	if err != nil || got != id {
		t.Fatalf("round trip = %s, %v", got, err)
	}
	if got, err := decodeCursor(""); err != nil || got != uuid.Nil {
		t.Fatalf("empty cursor = %s, %v; want first page", got, err)
	}
	for _, bad := range []string{"!!!", "AAAA", encodeCursor(id) + "x"} {
		if _, err := decodeCursor(bad); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("decodeCursor(%q) error = %v, want ErrInvalidCursor", bad, err)
		}
	}
}
