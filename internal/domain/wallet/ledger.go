// Package wallet holds the Wallet aggregate root and its append-only ledger.
package wallet

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

// Direction is the side of a ledger movement.
type Direction string

// Ledger directions.
const (
	DirectionDebit  Direction = "DEBIT"
	DirectionCredit Direction = "CREDIT"
)

// IsValid reports whether d is DEBIT or CREDIT.
func (d Direction) IsValid() bool { return d == DirectionDebit || d == DirectionCredit }

// LedgerEntry is an immutable record of one balance movement. Corrections are
// new entries; an entry is never edited.
type LedgerEntry struct {
	id            uuid.UUID
	walletID      uuid.UUID
	transactionID uuid.UUID
	direction     Direction
	amount        money.Money
	balanceBefore money.Money
	balanceAfter  money.Money
	createdAt     time.Time
}

// LedgerEntryParams carries the fields of a LedgerEntry.
type LedgerEntryParams struct {
	ID            uuid.UUID
	WalletID      uuid.UUID
	TransactionID uuid.UUID
	Direction     Direction
	Amount        money.Money
	BalanceBefore money.Money
	BalanceAfter  money.Money
	CreatedAt     time.Time
}

// NewLedgerEntry validates p and builds an entry, enforcing
// balanceAfter = balanceBefore ± amount by direction and non-negative
// balances. It serves both creation and rehydration: it has no side effects.
func NewLedgerEntry(p LedgerEntryParams) (LedgerEntry, error) {
	if p.ID == uuid.Nil || p.WalletID == uuid.Nil || p.TransactionID == uuid.Nil {
		return LedgerEntry{}, fmt.Errorf("%w: id, walletId and transactionId are required", ErrInvalidLedgerEntry)
	}
	if !p.Direction.IsValid() {
		return LedgerEntry{}, fmt.Errorf("%w: direction %q", ErrInvalidLedgerEntry, p.Direction)
	}
	if !p.Amount.IsPositive() {
		return LedgerEntry{}, fmt.Errorf("%w: amount must be positive", ErrInvalidLedgerEntry)
	}
	if p.CreatedAt.IsZero() {
		return LedgerEntry{}, fmt.Errorf("%w: createdAt is required", ErrInvalidLedgerEntry)
	}
	if !p.BalanceBefore.IsValid() || !p.BalanceAfter.IsValid() ||
		p.BalanceBefore.IsNegative() || p.BalanceAfter.IsNegative() {
		return LedgerEntry{}, fmt.Errorf("%w: balances must be valid and non-negative", ErrInvalidLedgerEntry)
	}
	var expected money.Money
	var err error
	if p.Direction == DirectionCredit {
		expected, err = p.BalanceBefore.Add(p.Amount)
	} else {
		expected, err = p.BalanceBefore.Sub(p.Amount)
	}
	if err != nil {
		return LedgerEntry{}, fmt.Errorf("%w: %v", ErrInvalidLedgerEntry, err)
	}
	if !expected.Equal(p.BalanceAfter) {
		return LedgerEntry{}, fmt.Errorf("%w: balanceAfter %s, expected %s", ErrInvalidLedgerEntry, p.BalanceAfter, expected)
	}
	return LedgerEntry{
		id:            p.ID,
		walletID:      p.WalletID,
		transactionID: p.TransactionID,
		direction:     p.Direction,
		amount:        p.Amount,
		balanceBefore: p.BalanceBefore,
		balanceAfter:  p.BalanceAfter,
		createdAt:     p.CreatedAt.UTC(),
	}, nil
}

// ID returns the entry id.
func (e LedgerEntry) ID() uuid.UUID { return e.id }

// WalletID returns the wallet the entry belongs to.
func (e LedgerEntry) WalletID() uuid.UUID { return e.walletID }

// TransactionID returns the wager transaction that produced the entry.
func (e LedgerEntry) TransactionID() uuid.UUID { return e.transactionID }

// Direction returns DEBIT or CREDIT.
func (e LedgerEntry) Direction() Direction { return e.direction }

// Amount returns the moved amount (always positive).
func (e LedgerEntry) Amount() money.Money { return e.amount }

// BalanceBefore returns the wallet balance before the movement.
func (e LedgerEntry) BalanceBefore() money.Money { return e.balanceBefore }

// BalanceAfter returns the wallet balance after the movement.
func (e LedgerEntry) BalanceAfter() money.Money { return e.balanceAfter }

// CreatedAt returns the creation instant (UTC).
func (e LedgerEntry) CreatedAt() time.Time { return e.createdAt }
