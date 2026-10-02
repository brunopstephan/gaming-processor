package postgres

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

var _ app.TxManager = (*TxManager)(nil)

type txKey struct{}

// TxManager delimits SQL transactions for use cases. The GORM transaction
// travels in the context; repositories pick it up through conn.
type TxManager struct {
	db               *gorm.DB
	lockTimeout      time.Duration
	statementTimeout time.Duration
}

// NewTxManager builds a TxManager with the per-transaction timeouts of cfg.
func NewTxManager(db *gorm.DB, cfg config.Config) *TxManager {
	return &TxManager{db: db, lockTimeout: cfg.Database.LockTimeout, statementTimeout: cfg.Database.StatementTimeout}
}

// WithinTx runs fn in a READ COMMITTED transaction with transaction-local
// lock_timeout and statement_timeout. A call inside an existing transaction
// joins it. Errors are classified with mapError.
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFrom(ctx); ok {
		return fn(ctx)
	}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Exec("SELECT set_config('lock_timeout', ?, true), set_config('statement_timeout', ?, true)",
			millis(m.lockTimeout), millis(m.statementTimeout)).Error
		if err != nil {
			return err
		}
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
	return mapError(err)
}

func millis(d time.Duration) string { return fmt.Sprintf("%dms", d.Milliseconds()) }

// txFrom returns the transaction carried by ctx, if any.
func txFrom(ctx context.Context) (*gorm.DB, bool) {
	tx, ok := ctx.Value(txKey{}).(*gorm.DB)
	return tx, ok
}

// conn returns the transaction in ctx or, outside a transaction, db bound to ctx.
func conn(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx, ok := txFrom(ctx); ok {
		return tx
	}
	return db.WithContext(ctx)
}
