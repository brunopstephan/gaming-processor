package app

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

// TxManager runs fn inside one SQL transaction carried by ctx. Repositories
// called with that ctx join the transaction; a nested call joins the outer
// one. fn's error rolls everything back.
//
// A nested WithinTx joins the outer transaction (no savepoints): an error
// swallowed inside still aborts the outer transaction. Work that must commit
// independently (e.g. the FAILED audit write after a rollback) must start
// from a context that carries no transaction.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
	// WithinSnapshot runs fn in a read-only REPEATABLE READ transaction: every
	// read sees one consistent snapshot and writes are refused. It never joins
	// an outer transaction.
	WithinSnapshot(ctx context.Context, fn func(ctx context.Context) error) error
	// InTx reports whether ctx carries a transaction.
	InTx(ctx context.Context) bool
}

// WalletRepository persists the Wallet aggregate.
type WalletRepository interface {
	// Create inserts a new wallet; ErrConflict if (playerId, currency) exists.
	Create(ctx context.Context, w *wallet.Wallet) error
	// Get reads a wallet without locking; ErrNotFound if missing.
	Get(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error)
	// GetForUpdate reads and row-locks a wallet; it requires a transaction.
	GetForUpdate(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error)
	// Update writes balance and version, guarded by expectedVersion;
	// ErrVersionConflict if another writer moved the wallet.
	Update(ctx context.Context, w *wallet.Wallet, expectedVersion int64) error
}

// TransactionRepository persists WagerTransactions.
type TransactionRepository interface {
	// Insert adds t unless a row already exists with the same id, the same
	// (provider, idempotency key), the same (provider, external id) or is the
	// wallet's OPENING; inserted=false reports that, and callers re-read by
	// key, then by external id. Inserting a PROCESSED reversal directly is
	// unsupported: insert PENDING then Update (a reversal-index race then
	// surfaces as ErrConflict on Update). A FAILED row may be inserted
	// directly.
	Insert(ctx context.Context, t *wagering.WagerTransaction) (inserted bool, err error)
	// Update writes the mutable state of t (status, reference, result, retry).
	Update(ctx context.Context, t *wagering.WagerTransaction) error
	Get(ctx context.Context, id uuid.UUID) (*wagering.WagerTransaction, error)
	FindByIdempotencyKey(ctx context.Context, providerID, key string) (*wagering.WagerTransaction, error)
	FindByExternalID(ctx context.Context, providerID, externalTransactionID string) (*wagering.WagerTransaction, error)
	// HasProcessedReversal reports whether referenceID already has a PROCESSED
	// reversal of kind or, when referenceKind is BET, any PROCESSED REFUND or
	// ROLLBACK — the value of ProcessInput.ReferenceAlreadyReversed.
	HasProcessedReversal(ctx context.Context, referenceID uuid.UUID, kind, referenceKind wagering.Kind) (bool, error)
}

// LedgerRepository appends immutable ledger entries and queries them.
type LedgerRepository interface {
	Append(ctx context.Context, e wallet.LedgerEntry) error
	// List returns up to limit entries of walletID with id greater than after
	// (uuid.Nil for the first page), ordered by id.
	List(ctx context.Context, walletID, after uuid.UUID, limit int) ([]wallet.LedgerEntry, error)
	// Totals returns credits minus debits in minor units and the entry count.
	Totals(ctx context.Context, walletID uuid.UUID) (net int64, count int64, err error)
}

// OutboxRepository stores integration events in the same transaction as the
// changes that produced them.
type OutboxRepository interface {
	Append(ctx context.Context, envs ...events.Envelope) error
}

// InboxMessage records that a consumer handled a message. It is written in
// the transaction of the message's effects and identifies redeliveries.
type InboxMessage struct {
	Consumer    string
	MessageID   string
	PayloadHash string
	ReceivedAt  time.Time
	ProcessedAt time.Time
}

// InboxRepository stores handled messages.
type InboxRepository interface {
	// Get returns the handled message; ErrNotFound if unknown.
	Get(ctx context.Context, consumer, messageID string) (InboxMessage, error)
	// Insert records m unless (consumer, messageId) exists; inserted=false reports that.
	Insert(ctx context.Context, m InboxMessage) (inserted bool, err error)
}
