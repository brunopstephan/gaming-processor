// Package app holds the application use cases (Plan 3) and the ports they
// depend on. Adapters translate their failures into the errors below.
package app

import (
	"errors"
	"fmt"
)

var (
	// ErrNotFound reports a missing row.
	ErrNotFound = errors.New("app: not found")
	// ErrConflict reports a uniqueness violation; the message names the constraint.
	ErrConflict = errors.New("app: conflict")
	// ErrVersionConflict reports a lost race on an optimistic version guard.
	// It also matches ErrTransient: the caller should retry, never record a
	// permanent failure.
	ErrVersionConflict = fmt.Errorf("app: concurrent update: %w", ErrTransient)
	// ErrLockTimeout reports a row lock not granted within lock_timeout. It
	// also matches ErrTransient.
	ErrLockTimeout = fmt.Errorf("app: lock timeout: %w", ErrTransient)
	// ErrTransient reports a temporary infrastructure failure worth retrying
	// (connection loss, lock or statement timeout, serialization, deadlock).
	ErrTransient = errors.New("app: transient infrastructure failure")
)
