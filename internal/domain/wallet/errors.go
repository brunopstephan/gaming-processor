package wallet

import "errors"

var (
	// ErrInvalidWallet reports invalid wallet construction or rehydration data.
	ErrInvalidWallet = errors.New("wallet: invalid wallet")
	// ErrInvalidLedgerEntry reports an entry that violates its invariants.
	ErrInvalidLedgerEntry = errors.New("wallet: invalid ledger entry")
	// ErrInsufficientFunds reports a debit larger than the balance.
	ErrInsufficientFunds = errors.New("wallet: insufficient funds")
	// ErrCurrencyMismatch reports a movement in a currency other than the wallet's.
	ErrCurrencyMismatch = errors.New("wallet: currency mismatch")
	// ErrInvalidAmount reports a movement amount that is not positive.
	ErrInvalidAmount = errors.New("wallet: movement amount must be positive")
)
