package events

import (
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

// Event type names.
const (
	TypeWagerTransactionProcessed        = "WagerTransactionProcessed"
	TypeWagerTransactionRejected         = "WagerTransactionRejected"
	TypeWalletBalanceChanged             = "WalletBalanceChanged"
	TypeWagerTransactionPendingReference = "WagerTransactionPendingReference"
)

// WagerTransactionProcessed is emitted when an operation succeeds, including
// LOSS and the internal OPENING. External metadata is omitted for OPENING.
type WagerTransactionProcessed struct {
	TransactionID          uuid.UUID   `json:"transactionId"`
	WalletID               uuid.UUID   `json:"walletId"`
	PlayerID               uuid.UUID   `json:"playerId"`
	Origin                 string      `json:"origin"`
	Kind                   string      `json:"kind"`
	Money                  money.Money `json:"money"`
	ProviderID             string      `json:"providerId,omitempty"`
	ExternalTransactionID  string      `json:"externalTransactionId,omitempty"`
	RoundID                string      `json:"roundId,omitempty"`
	GameID                 string      `json:"gameId,omitempty"`
	ReferenceTransactionID *uuid.UUID  `json:"referenceTransactionId,omitempty"`
	BalanceAfter           money.Money `json:"balanceAfter"`
}

// EventType implements Data.
func (WagerTransactionProcessed) EventType() string { return TypeWagerTransactionProcessed }

// EventVersion implements Data.
func (WagerTransactionProcessed) EventVersion() int { return 1 }

// AggregateID implements Data.
func (e WagerTransactionProcessed) AggregateID() uuid.UUID { return e.WalletID }

// WagerTransactionRejected is emitted on a definitive business rejection.
type WagerTransactionRejected struct {
	TransactionID         uuid.UUID   `json:"transactionId"`
	WalletID              uuid.UUID   `json:"walletId"`
	ProviderID            string      `json:"providerId"`
	ExternalTransactionID string      `json:"externalTransactionId"`
	Kind                  string      `json:"kind"`
	Money                 money.Money `json:"money"`
	FailureCode           string      `json:"failureCode"`
}

// EventType implements Data.
func (WagerTransactionRejected) EventType() string { return TypeWagerTransactionRejected }

// EventVersion implements Data.
func (WagerTransactionRejected) EventVersion() int { return 1 }

// AggregateID implements Data.
func (e WagerTransactionRejected) AggregateID() uuid.UUID { return e.WalletID }

// WalletBalanceChanged is emitted for every effective balance change.
type WalletBalanceChanged struct {
	WalletID      uuid.UUID   `json:"walletId"`
	TransactionID uuid.UUID   `json:"transactionId"`
	Direction     string      `json:"direction"`
	Money         money.Money `json:"money"`
	BalanceBefore money.Money `json:"balanceBefore"`
	BalanceAfter  money.Money `json:"balanceAfter"`
	WalletVersion int64       `json:"walletVersion"`
}

// EventType implements Data.
func (WalletBalanceChanged) EventType() string { return TypeWalletBalanceChanged }

// EventVersion implements Data.
func (WalletBalanceChanged) EventVersion() int { return 1 }

// AggregateID implements Data.
func (e WalletBalanceChanged) AggregateID() uuid.UUID { return e.WalletID }

// WagerTransactionPendingReference is emitted when an operation starts waiting
// for a reference that has not arrived yet.
type WagerTransactionPendingReference struct {
	TransactionID                  uuid.UUID `json:"transactionId"`
	WalletID                       uuid.UUID `json:"walletId"`
	ProviderID                     string    `json:"providerId"`
	ExternalTransactionID          string    `json:"externalTransactionId"`
	Kind                           string    `json:"kind"`
	ReferenceExternalTransactionID string    `json:"referenceExternalTransactionId"`
	Attempts                       int       `json:"attempts"`
	NextAttemptAt                  string    `json:"nextAttemptAt"`
}

// EventType implements Data.
func (WagerTransactionPendingReference) EventType() string {
	return TypeWagerTransactionPendingReference
}

// EventVersion implements Data.
func (WagerTransactionPendingReference) EventVersion() int { return 1 }

// AggregateID implements Data.
func (e WagerTransactionPendingReference) AggregateID() uuid.UUID { return e.WalletID }
