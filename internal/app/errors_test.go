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

func TestLockTimeoutIsTransient(t *testing.T) {
	if !errors.Is(ErrLockTimeout, ErrTransient) {
		t.Fatal("ErrLockTimeout must match ErrTransient")
	}
	wrapped := fmt.Errorf("op: %w", ErrLockTimeout)
	if !errors.Is(wrapped, ErrLockTimeout) || !errors.Is(wrapped, ErrTransient) {
		t.Fatal("wrapped ErrLockTimeout must match both sentinels")
	}
	if errors.Is(ErrTransient, ErrLockTimeout) || errors.Is(ErrVersionConflict, ErrLockTimeout) {
		t.Fatal("ErrLockTimeout must stay a distinct sentinel")
	}
}
