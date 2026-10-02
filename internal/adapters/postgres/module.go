package postgres

import (
	"context"
	"fmt"

	"go.uber.org/fx"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
)

// Module wires the database handle, the transaction manager and the
// repositories, exposed as application ports.
var Module = fx.Module("postgres",
	fx.Provide(
		NewDB,
		fx.Annotate(NewTxManager, fx.As(new(app.TxManager))),
		fx.Annotate(NewWalletRepository, fx.As(new(app.WalletRepository))),
		fx.Annotate(NewTransactionRepository, fx.As(new(app.TransactionRepository))),
		fx.Annotate(NewLedgerRepository, fx.As(new(app.LedgerRepository))),
		fx.Annotate(NewOutboxRepository, fx.As(new(app.OutboxRepository))),
		fx.Annotate(NewInboxRepository, fx.As(new(app.InboxRepository))),
		fx.Annotate(newHealthCheck, fx.ResultTags(`group:"readiness"`)),
	),
)

// NewDB opens the pool and binds it to the Fx lifecycle: OnStart verifies
// connectivity (the process does not start without PostgreSQL), OnStop closes
// the pool. Fx stops components in reverse order, so everything that uses the
// pool has stopped before it closes.
func NewDB(lc fx.Lifecycle, cfg config.Config) (*gorm.DB, error) {
	db, err := Open(cfg.Database)
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("postgres: pool: %w", err)
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := sqlDB.PingContext(ctx); err != nil {
				return fmt.Errorf("postgres: ping: %w", err)
			}
			return nil
		},
		OnStop: func(context.Context) error { return sqlDB.Close() },
	})
	return db, nil
}

// newHealthCheck reports PostgreSQL readiness with a ping.
func newHealthCheck(db *gorm.DB) (health.Check, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return health.Check{}, err
	}
	return health.Check{Name: "postgres", Probe: sqlDB.PingContext}, nil
}
