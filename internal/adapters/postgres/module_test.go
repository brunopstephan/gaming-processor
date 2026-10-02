//go:build !unit && !e2e

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/postgres"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func TestModuleGraphIsValid(t *testing.T) {
	t.Setenv("DATABASE_URL", pgtest.AppURL())
	if err := fx.ValidateApp(fx.NopLogger, config.Module, postgres.Module,
		fx.Invoke(func(app.TxManager, app.WalletRepository, app.TransactionRepository, app.LedgerRepository, app.OutboxRepository) {
		}),
	); err != nil {
		t.Fatal(err)
	}
}

func TestModuleLifecycle(t *testing.T) {
	pgtest.AppDB(t) // clear failure when the test database is down
	t.Setenv("DATABASE_URL", pgtest.AppURL())

	var (
		db      *gorm.DB
		tm      app.TxManager
		wallets app.WalletRepository
	)
	fxApp := fxtest.New(t, fx.NopLogger, config.Module, postgres.Module, fx.Populate(&db, &tm, &wallets))
	fxApp.RequireStart()

	brl, _ := money.Parse("10.00", "BRL")
	w, _, err := wallet.Open(wallet.OpenParams{
		ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl,
		OpeningTransactionID: uuid.New(), LedgerEntryID: uuid.New(), Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = tm.WithinTx(context.Background(), func(ctx context.Context) error { return wallets.Create(ctx, w) })
	if err != nil {
		t.Fatalf("repositories must work after start: %v", err)
	}

	fxApp.RequireStop()
	sqlDB, _ := db.DB()
	if err := sqlDB.PingContext(context.Background()); err == nil {
		t.Fatal("the pool must be closed after stop")
	}
}

func TestModuleStartFailsWithoutDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://wallet_app:wallet_app@127.0.0.1:1/wallet?sslmode=disable&connect_timeout=1")
	fxApp := fx.New(fx.NopLogger, config.Module, postgres.Module, fx.Invoke(func(*gorm.DB) {}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fxApp.Start(ctx); err == nil {
		_ = fxApp.Stop(ctx)
		t.Fatal("start must fail when postgres is unreachable")
	}
}

func TestModuleRejectsInvalidConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	fxApp := fx.New(fx.NopLogger, config.Module, postgres.Module, fx.Invoke(func(*gorm.DB) {}))
	if fxApp.Err() == nil {
		t.Fatal("missing DATABASE_URL must fail before start")
	}
}
