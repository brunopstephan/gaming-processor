package app

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

// Module wires the use cases. It requires the persistence ports (postgres
// module), a Metrics and a *slog.Logger from the composition root.
var Module = fx.Module("app",
	fx.Provide(
		func() Clock { return SystemClock },
		newDeps,
		NewWalletService,
		func(d Deps, cfg config.Config) (*WageringService, error) {
			return NewWageringService(d, retryPolicy(cfg))
		},
		NewQueryService,
		NewReconciliationService,
	),
)

type depsIn struct {
	fx.In
	Tx           TxManager
	Wallets      WalletRepository
	Transactions TransactionRepository
	Ledger       LedgerRepository
	Outbox       OutboxRepository
	Clock        Clock
	Metrics      Metrics
	Log          *slog.Logger
}

func newDeps(in depsIn) Deps {
	return Deps{
		Tx: in.Tx, Wallets: in.Wallets, Transactions: in.Transactions, Ledger: in.Ledger, Outbox: in.Outbox,
		Clock: in.Clock, Metrics: in.Metrics, Log: in.Log,
	}
}

func retryPolicy(cfg config.Config) wagering.RetryPolicy {
	return wagering.RetryPolicy{
		BaseDelay:   cfg.Wagering.ReferenceRetryBaseDelay,
		MaxDelay:    cfg.Wagering.ReferenceRetryMaxDelay,
		MaxAttempts: cfg.Wagering.ReferenceRetryMaxAttempts,
	}
}
