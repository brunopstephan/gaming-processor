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
	// ErrWalletAlreadyExists reports a second wallet for the same (player, currency).
	ErrWalletAlreadyExists = errors.New("app: wallet already exists for player and currency")
	// ErrIdempotencyKeyConflict reports a key reused with a different business payload.
	ErrIdempotencyKeyConflict = errors.New("app: idempotency key reused with a different payload")
	// ErrExternalTransactionConflict reports an external transaction already
	// registered under another idempotency key.
	ErrExternalTransactionConflict = errors.New("app: external transaction already registered under another idempotency key")
	// ErrInsideTransaction reports WageringService.Process called with a
	// context that already carries a transaction.
	ErrInsideTransaction = errors.New("app: Process must not run inside a transaction; use alongside")
	// ErrInvalidCursor reports an undecodable ledger cursor.
	ErrInvalidCursor = errors.New("app: invalid ledger cursor")
	// ErrInvalidLimit reports a ledger page size outside 1..MaxLedgerLimit.
	ErrInvalidLimit = errors.New("app: invalid ledger page size")
)
