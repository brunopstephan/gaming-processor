package postgres

import (
	"context"

	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

var _ app.LedgerRepository = (*LedgerRepository)(nil)

// LedgerRepository appends ledger entries. The table is append-only: the
// application role cannot update or delete, and triggers block the owner too.
type LedgerRepository struct {
	db *gorm.DB
}

// NewLedgerRepository builds a LedgerRepository.
func NewLedgerRepository(db *gorm.DB) *LedgerRepository { return &LedgerRepository{db: db} }

// Append inserts e; a second entry for the same (wallet, transaction) is app.ErrConflict.
func (r *LedgerRepository) Append(ctx context.Context, e wallet.LedgerEntry) error {
	m := ledgerEntryModel{
		ID:                 e.ID(),
		WalletID:           e.WalletID(),
		TransactionID:      e.TransactionID(),
		Direction:          string(e.Direction()),
		AmountMinor:        e.Amount().MinorUnits(),
		Currency:           e.Amount().Currency().Code(),
		BalanceBeforeMinor: e.BalanceBefore().MinorUnits(),
		BalanceAfterMinor:  e.BalanceAfter().MinorUnits(),
		CreatedAt:          e.CreatedAt(),
	}
	return mapError(conn(ctx, r.db).Create(&m).Error)
}
