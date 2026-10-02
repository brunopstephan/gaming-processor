package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

var _ app.WalletRepository = (*WalletRepository)(nil)

// errNoTransaction reports a locking read attempted outside WithinTx.
var errNoTransaction = errors.New("postgres: operation requires a transaction (TxManager.WithinTx)")

// WalletRepository persists wallets with pessimistic row locks and a version guard.
type WalletRepository struct {
	db *gorm.DB
}

// NewWalletRepository builds a WalletRepository.
func NewWalletRepository(db *gorm.DB) *WalletRepository { return &WalletRepository{db: db} }

// Create inserts w; a duplicate (playerId, currency) returns app.ErrConflict.
func (r *WalletRepository) Create(ctx context.Context, w *wallet.Wallet) error {
	m := walletToModel(w)
	return mapError(conn(ctx, r.db).Create(&m).Error)
}

// Get reads a wallet without locking.
func (r *WalletRepository) Get(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error) {
	var m walletModel
	if err := conn(ctx, r.db).Where("id = ?", id).Take(&m).Error; err != nil {
		return nil, mapError(err)
	}
	return walletFromModel(m)
}

// GetForUpdate reads a wallet with SELECT ... FOR UPDATE. Concurrent writers
// of the same wallet wait (up to lock_timeout); other wallets are unaffected.
func (r *WalletRepository) GetForUpdate(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error) {
	tx, ok := txFrom(ctx)
	if !ok {
		return nil, errNoTransaction
	}
	var m walletModel
	err := tx.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).Where("id = ?", id).Take(&m).Error
	if err != nil {
		return nil, mapError(err)
	}
	return walletFromModel(m)
}

// Update writes the new balance with
// UPDATE wallets SET ..., version = version + 1 WHERE id = ? AND version = ?.
// No matching row means another writer committed first: app.ErrVersionConflict.
func (r *WalletRepository) Update(ctx context.Context, w *wallet.Wallet, expectedVersion int64) error {
	if w.Version() != expectedVersion+1 {
		return fmt.Errorf("postgres: wallet %s version %d must be expected %d + 1", w.ID(), w.Version(), expectedVersion)
	}
	res := conn(ctx, r.db).Model(&walletModel{}).
		Where("id = ? AND version = ?", w.ID(), expectedVersion).
		Updates(map[string]any{
			"balance_minor": w.Balance().MinorUnits(),
			"version":       gorm.Expr("version + 1"),
			"updated_at":    w.UpdatedAt(),
		})
	if res.Error != nil {
		return mapError(res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: wallet %s at version %d", app.ErrVersionConflict, w.ID(), expectedVersion)
	}
	return nil
}
