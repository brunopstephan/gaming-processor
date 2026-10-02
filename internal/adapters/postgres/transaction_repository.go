package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

var _ app.TransactionRepository = (*TransactionRepository)(nil)

// TransactionRepository persists wager transactions. Idempotency relies on
// the unique constraints: Insert uses INSERT ... ON CONFLICT DO NOTHING, so a
// concurrent duplicate waits for the winner's commit and then inserts nothing.
type TransactionRepository struct {
	db *gorm.DB
}

// NewTransactionRepository builds a TransactionRepository.
func NewTransactionRepository(db *gorm.DB) *TransactionRepository {
	return &TransactionRepository{db: db}
}

// Insert adds t unless any unique key already exists.
func (r *TransactionRepository) Insert(ctx context.Context, t *wagering.WagerTransaction) (bool, error) {
	m := transactionToModel(t)
	res := conn(ctx, r.db).Clauses(clause.OnConflict{DoNothing: true}).Create(&m)
	if res.Error != nil {
		return false, mapError(res.Error)
	}
	return res.RowsAffected == 1, nil
}

// Update writes the mutable columns of t. The database trigger refuses
// changes to terminal rows and invalid transitions.
func (r *TransactionRepository) Update(ctx context.Context, t *wagering.WagerTransaction) error {
	m := transactionToModel(t)
	res := conn(ctx, r.db).Model(&transactionModel{}).Where("id = ?", m.ID).Updates(map[string]any{
		"status":                   m.Status,
		"reference_transaction_id": m.ReferenceTransactionID,
		"reference_kind":           m.ReferenceKind,
		"failure_code":             m.FailureCode,
		"result_balance_minor":     m.ResultBalanceMinor,
		"attempts":                 m.Attempts,
		"next_attempt_at":          m.NextAttemptAt,
		"updated_at":               m.UpdatedAt,
		"processed_at":             m.ProcessedAt,
	})
	if res.Error != nil {
		return mapError(res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: transaction %s", app.ErrNotFound, m.ID)
	}
	return nil
}

// Get reads a transaction by internal id.
func (r *TransactionRepository) Get(ctx context.Context, id uuid.UUID) (*wagering.WagerTransaction, error) {
	return r.findOne(ctx, "id = ?", id)
}

// FindByIdempotencyKey reads the provider's transaction for key.
func (r *TransactionRepository) FindByIdempotencyKey(ctx context.Context, providerID, key string) (*wagering.WagerTransaction, error) {
	return r.findOne(ctx, "provider_id = ? AND idempotency_key = ?", providerID, key)
}

// FindByExternalID reads the provider's transaction for its external id.
func (r *TransactionRepository) FindByExternalID(ctx context.Context, providerID, externalTransactionID string) (*wagering.WagerTransaction, error) {
	return r.findOne(ctx, "provider_id = ? AND external_transaction_id = ?", providerID, externalTransactionID)
}

// HasProcessedReversal implements the ReferenceAlreadyReversed rule; the
// partial unique indexes enforce the same rule if two writers race.
func (r *TransactionRepository) HasProcessedReversal(ctx context.Context, referenceID uuid.UUID, kind, referenceKind wagering.Kind) (bool, error) {
	var exists bool
	err := conn(ctx, r.db).Raw(`
		SELECT EXISTS (
			SELECT 1 FROM wager_transactions
			WHERE reference_transaction_id = ?
			  AND status = 'PROCESSED'
			  AND kind IN ('REFUND', 'ROLLBACK')
			  AND (kind = ? OR ?)
		)`, referenceID, string(kind), referenceKind == wagering.KindBet).Scan(&exists).Error
	return exists, mapError(err)
}

func (r *TransactionRepository) findOne(ctx context.Context, query string, args ...any) (*wagering.WagerTransaction, error) {
	var m transactionModel
	if err := conn(ctx, r.db).Where(query, args...).Take(&m).Error; err != nil {
		return nil, mapError(err)
	}
	return transactionFromModel(m)
}
