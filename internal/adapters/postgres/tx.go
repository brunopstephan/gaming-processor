package postgres

import (
	"context"
	"database/sql"
	"errors"
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

// errNestedSnapshot reports WithinSnapshot called inside another transaction.
var errNestedSnapshot = errors.New("postgres: WithinSnapshot cannot run inside another transaction")

// WithinTx runs fn in a READ COMMITTED transaction with transaction-local
// lock_timeout and statement_timeout. A call inside an existing transaction
// joins it. Errors are classified with mapError.
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFrom(ctx); ok {
		return fn(ctx)
	}
	return m.run(ctx, nil, fn)
}

// WithinSnapshot runs fn in a REPEATABLE READ READ ONLY transaction, so all
// reads see the same snapshot (used by reconciliation). It never joins an
// existing transaction.
func (m *TxManager) WithinSnapshot(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFrom(ctx); ok {
		return errNestedSnapshot
	}
	return m.run(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, fn)
}

// InTx reports whether ctx carries a transaction.
func (m *TxManager) InTx(ctx context.Context) bool {
	_, ok := txFrom(ctx)
	return ok
}

func (m *TxManager) run(ctx context.Context, opts *sql.TxOptions, fn func(ctx context.Context) error) error {
	var options []*sql.TxOptions
	if opts != nil {
		options = append(options, opts)
	}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Exec("SELECT set_config('lock_timeout', ?, true), set_config('statement_timeout', ?, true)",
			millis(m.lockTimeout), millis(m.statementTimeout)).Error
		if err != nil {
			return err
		}
		return fn(context.WithValue(ctx, txKey{}, tx))
	}, options...)
	return mapError(err)
}

func millis(d time.Duration) string { return fmt.Sprintf("%dms", d.Milliseconds()) }

// txFrom returns the transaction carried by ctx, if any.
func txFrom(ctx context.Context) (*gorm.DB, bool) {
	tx, ok := ctx.Value(txKey{}).(*gorm.DB)
	return tx, ok
}

// conn returns the transaction in ctx or, outside a transaction, db, both bound to the per-call ctx.
func conn(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx, ok := txFrom(ctx); ok {
		return tx.WithContext(ctx)
	}
	return db.WithContext(ctx)
}
