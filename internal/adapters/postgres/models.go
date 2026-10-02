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
