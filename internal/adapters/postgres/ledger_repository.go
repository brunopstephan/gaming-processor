package postgres

import (
	"context"

	"github.com/google/uuid"
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

// List returns a page of entries ordered by id (UUIDv7, stable).
func (r *LedgerRepository) List(ctx context.Context, walletID, after uuid.UUID, limit int) ([]wallet.LedgerEntry, error) {
	var rows []ledgerEntryModel
	err := conn(ctx, r.db).Where("wallet_id = ? AND id > ?", walletID, after).Order("id").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, mapError(err)
	}
	entries := make([]wallet.LedgerEntry, 0, len(rows))
	for _, m := range rows {
		e, err := ledgerEntryFromModel(m)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// Totals sums credits minus debits and counts the entries of walletID. The
// ::bigint cast fails loudly (22003) instead of overflowing silently.
func (r *LedgerRepository) Totals(ctx context.Context, walletID uuid.UUID) (int64, int64, error) {
	var row struct {
		Net   int64
		Count int64
	}
	err := conn(ctx, r.db).Raw(`
		SELECT COALESCE(SUM(CASE WHEN direction = 'CREDIT' THEN amount_minor ELSE -amount_minor END), 0)::bigint AS net,
		       COUNT(*) AS count
		FROM wallet_ledger_entries
		WHERE wallet_id = ?`, walletID).Scan(&row).Error
	if err != nil {
		return 0, 0, mapError(err)
	}
	return row.Net, row.Count, nil
}
