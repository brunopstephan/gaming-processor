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
