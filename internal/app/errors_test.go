//go:build !integration && !e2e

package app

import (
	"errors"
	"fmt"
	"testing"
)

func TestVersionConflictIsTransient(t *testing.T) {
	if !errors.Is(ErrVersionConflict, ErrTransient) {
		t.Fatal("ErrVersionConflict must match ErrTransient")
	}
	wrapped := fmt.Errorf("update wallet: %w", ErrVersionConflict)
	if !errors.Is(wrapped, ErrVersionConflict) || !errors.Is(wrapped, ErrTransient) {
		t.Fatalf("wrapped version conflict must match both sentinels: %v", wrapped)
	}
	if errors.Is(ErrTransient, ErrVersionConflict) {
		t.Fatal("ErrTransient must not match ErrVersionConflict")
	}
}
