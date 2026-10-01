package wallet

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

// InitialVersion is the version of a newly opened wallet. The version only
// increases when the balance changes.
const InitialVersion int64 = 1

// Wallet is the financial aggregate root. One wallet exists per
// (playerId, currency). Its balance is never negative.
type Wallet struct {
	id        uuid.UUID
	playerID  uuid.UUID
	balance   money.Money
	version   int64
	createdAt time.Time
	updatedAt time.Time
}

// OpenParams carries the data needed to open a wallet.
type OpenParams struct {
	ID             uuid.UUID
	PlayerID       uuid.UUID
	InitialBalance money.Money
	// OpeningTransactionID and LedgerEntryID identify the opening credit and
	// are required only when InitialBalance is positive.
	OpeningTransactionID uuid.UUID
	LedgerEntryID        uuid.UUID
	Now                  time.Time
}

// Open creates a wallet at version 1. A positive initial balance also returns
// the opening credit entry (0 → initial) without bumping the version; a zero
// initial balance returns no entry.
func Open(p OpenParams) (*Wallet, *LedgerEntry, error) {
	if p.ID == uuid.Nil || p.PlayerID == uuid.Nil {
		return nil, nil, fmt.Errorf("%w: id and playerId are required", ErrInvalidWallet)
	}
	if !p.InitialBalance.IsValid() || p.InitialBalance.IsNegative() {
		return nil, nil, fmt.Errorf("%w: initial balance must be valid and non-negative", ErrInvalidWallet)
	}
	if p.Now.IsZero() {
		return nil, nil, fmt.Errorf("%w: now is required", ErrInvalidWallet)
	}
	now := p.Now.UTC()
	w := &Wallet{
		id:        p.ID,
		playerID:  p.PlayerID,
		balance:   p.InitialBalance,
		version:   InitialVersion,
		createdAt: now,
		updatedAt: now,
	}
	if p.InitialBalance.IsZero() {
		return w, nil, nil
	}
	zero, err := money.Zero(p.InitialBalance.Currency())
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %v", ErrInvalidWallet, err)
	}
	entry, err := NewLedgerEntry(LedgerEntryParams{
		ID:            p.LedgerEntryID,
		WalletID:      p.ID,
		TransactionID: p.OpeningTransactionID,
		Direction:     DirectionCredit,
		Amount:        p.InitialBalance,
		BalanceBefore: zero,
		BalanceAfter:  p.InitialBalance,
		CreatedAt:     now,
	})
	if err != nil {
		return nil, nil, err
	}
	return w, &entry, nil
}

// RehydrateParams carries a stored wallet.
type RehydrateParams struct {
	ID        uuid.UUID
	PlayerID  uuid.UUID
	Balance   money.Money
	Version   int64
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Rehydrate rebuilds a stored wallet without applying any movement.
func Rehydrate(p RehydrateParams) (*Wallet, error) {
	if p.ID == uuid.Nil || p.PlayerID == uuid.Nil {
		return nil, fmt.Errorf("%w: id and playerId are required", ErrInvalidWallet)
	}
	if !p.Balance.IsValid() || p.Balance.IsNegative() {
		return nil, fmt.Errorf("%w: balance must be valid and non-negative", ErrInvalidWallet)
	}
	if p.Version < InitialVersion {
		return nil, fmt.Errorf("%w: version %d < %d", ErrInvalidWallet, p.Version, InitialVersion)
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		return nil, fmt.Errorf("%w: timestamps are required", ErrInvalidWallet)
	}
	return &Wallet{
		id:        p.ID,
		playerID:  p.PlayerID,
		balance:   p.Balance,
		version:   p.Version,
		createdAt: p.CreatedAt.UTC(),
		updatedAt: p.UpdatedAt.UTC(),
	}, nil
}

// Debit subtracts amount, keeping the balance non-negative, bumps the version
// and returns the ledger entry to be committed with the new balance.
func (w *Wallet) Debit(transactionID, entryID uuid.UUID, amount money.Money, now time.Time) (LedgerEntry, error) {
	return w.move(DirectionDebit, transactionID, entryID, amount, now)
}

// Credit adds amount, bumps the version and returns the ledger entry.
func (w *Wallet) Credit(transactionID, entryID uuid.UUID, amount money.Money, now time.Time) (LedgerEntry, error) {
	return w.move(DirectionCredit, transactionID, entryID, amount, now)
}

func (w *Wallet) move(dir Direction, transactionID, entryID uuid.UUID, amount money.Money, now time.Time) (LedgerEntry, error) {
	if !amount.IsPositive() {
		return LedgerEntry{}, ErrInvalidAmount
	}
	if amount.Currency() != w.Currency() {
		return LedgerEntry{}, fmt.Errorf("%w: wallet %s, movement %s", ErrCurrencyMismatch, w.Currency(), amount.Currency())
	}
	var after money.Money
	var err error
	if dir == DirectionDebit {
		cmp, cmpErr := w.balance.Cmp(amount)
		if cmpErr != nil {
			return LedgerEntry{}, cmpErr
		}
		if cmp < 0 {
			return LedgerEntry{}, fmt.Errorf("%w: balance %s, debit %s", ErrInsufficientFunds, w.balance, amount)
		}
		after, err = w.balance.Sub(amount)
	} else {
		after, err = w.balance.Add(amount)
	}
	if err != nil {
		return LedgerEntry{}, err
	}
	entry, err := NewLedgerEntry(LedgerEntryParams{
		ID:            entryID,
		WalletID:      w.id,
		TransactionID: transactionID,
		Direction:     dir,
		Amount:        amount,
		BalanceBefore: w.balance,
		BalanceAfter:  after,
		CreatedAt:     now,
	})
	if err != nil {
		return LedgerEntry{}, err
	}
	w.balance = after
	w.version++
	w.updatedAt = now.UTC()
	return entry, nil
}

// ID returns the wallet id.
func (w *Wallet) ID() uuid.UUID { return w.id }

// PlayerID returns the owning player.
func (w *Wallet) PlayerID() uuid.UUID { return w.playerID }

// Currency returns the wallet currency.
func (w *Wallet) Currency() money.Currency { return w.balance.Currency() }

// Balance returns the current balance.
func (w *Wallet) Balance() money.Money { return w.balance }

// Version returns the optimistic version, incremented on every balance change.
func (w *Wallet) Version() int64 { return w.version }

// CreatedAt returns the creation instant (UTC).
func (w *Wallet) CreatedAt() time.Time { return w.createdAt }

// UpdatedAt returns the last change instant (UTC).
func (w *Wallet) UpdatedAt() time.Time { return w.updatedAt }
