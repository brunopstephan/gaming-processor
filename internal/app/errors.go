// Package app holds the application use cases (Plan 3) and the ports they
// depend on. Adapters translate their failures into the errors below.
package app

import "errors"

var (
	// ErrNotFound reports a missing row.
	ErrNotFound = errors.New("app: not found")
	// ErrConflict reports a uniqueness violation; the message names the constraint.
	ErrConflict = errors.New("app: conflict")
	// ErrVersionConflict reports a lost race on an optimistic version guard.
	ErrVersionConflict = errors.New("app: concurrent update")
	// ErrTransient reports a temporary infrastructure failure worth retrying
	// (connection loss, lock or statement timeout, serialization, deadlock).
	ErrTransient = errors.New("app: transient infrastructure failure")
)
