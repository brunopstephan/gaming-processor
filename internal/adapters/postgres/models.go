package postgres

import (
	"time"

	"github.com/google/uuid"
)

// Persistence models are separate from domain types: the domain stays free of
// GORM tags. Timestamps are owned by the domain (autoCreate/UpdateTime off).

type walletModel struct {
	ID           uuid.UUID `gorm:"column:id;primaryKey"`
	PlayerID     uuid.UUID `gorm:"column:player_id"`
	Currency     string    `gorm:"column:currency"`
	BalanceMinor int64     `gorm:"column:balance_minor"`
	Version      int64     `gorm:"column:version"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt    time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (walletModel) TableName() string { return "wallets" }

type transactionModel struct {
	ID                             uuid.UUID  `gorm:"column:id;primaryKey"`
	Origin                         string     `gorm:"column:origin"`
	Kind                           string     `gorm:"column:kind"`
	Status                         string     `gorm:"column:status"`
	WalletID                       uuid.UUID  `gorm:"column:wallet_id"`
	PlayerID                       uuid.UUID  `gorm:"column:player_id"`
	AmountMinor                    int64      `gorm:"column:amount_minor"`
	Currency                       string     `gorm:"column:currency"`
	ProviderID                     *string    `gorm:"column:provider_id"`
	ExternalTransactionID          *string    `gorm:"column:external_transaction_id"`
	IdempotencyKey                 *string    `gorm:"column:idempotency_key"`
	PayloadHash                    *string    `gorm:"column:payload_hash"`
	RoundID                        *string    `gorm:"column:round_id"`
	GameID                         *string    `gorm:"column:game_id"`
	ReferenceExternalTransactionID *string    `gorm:"column:reference_external_transaction_id"`
	ReferenceTransactionID         *uuid.UUID `gorm:"column:reference_transaction_id"`
	ReferenceKind                  *string    `gorm:"column:reference_kind"`
	FailureCode                    *string    `gorm:"column:failure_code"`
	ResultBalanceMinor             *int64     `gorm:"column:result_balance_minor"`
	Attempts                       int        `gorm:"column:attempts"`
	NextAttemptAt                  *time.Time `gorm:"column:next_attempt_at"`
	CreatedAt                      time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt                      time.Time  `gorm:"column:updated_at;autoUpdateTime:false"`
	ProcessedAt                    *time.Time `gorm:"column:processed_at"`
}

func (transactionModel) TableName() string { return "wager_transactions" }

type ledgerEntryModel struct {
	ID                 uuid.UUID `gorm:"column:id;primaryKey"`
	WalletID           uuid.UUID `gorm:"column:wallet_id"`
	TransactionID      uuid.UUID `gorm:"column:transaction_id"`
	Direction          string    `gorm:"column:direction"`
	AmountMinor        int64     `gorm:"column:amount_minor"`
	Currency           string    `gorm:"column:currency"`
	BalanceBeforeMinor int64     `gorm:"column:balance_before_minor"`
	BalanceAfterMinor  int64     `gorm:"column:balance_after_minor"`
	CreatedAt          time.Time `gorm:"column:created_at;autoCreateTime:false"`
}

func (ledgerEntryModel) TableName() string { return "wallet_ledger_entries" }

type outboxEventModel struct {
	ID            uuid.UUID  `gorm:"column:id;primaryKey"`
	AggregateType string     `gorm:"column:aggregate_type"`
	AggregateID   uuid.UUID  `gorm:"column:aggregate_id"`
	EventType     string     `gorm:"column:event_type"`
	Payload       []byte     `gorm:"column:payload;type:jsonb"`
	OccurredAt    time.Time  `gorm:"column:occurred_at"`
	Attempts      int        `gorm:"column:attempts"`
	NextAttemptAt time.Time  `gorm:"column:next_attempt_at"`
	LockedBy      *string    `gorm:"column:locked_by"`
	LockedUntil   *time.Time `gorm:"column:locked_until"`
	PublishedAt   *time.Time `gorm:"column:published_at"`
	LastError     *string    `gorm:"column:last_error"`
}

func (outboxEventModel) TableName() string { return "outbox_events" }
