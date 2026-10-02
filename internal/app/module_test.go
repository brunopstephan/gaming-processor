//go:build !unit && !e2e

package app_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/postgres"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func externalDeps() fx.Option {
	return fx.Options(
		fx.Provide(func() app.Metrics { return app.NopMetrics{} }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
	)
}

func TestAppModuleGraph(t *testing.T) {
	t.Setenv("DATABASE_URL", pgtest.AppURL())
	err := fx.ValidateApp(fx.NopLogger, config.Module, postgres.Module, app.Module, externalDeps(),
		fx.Invoke(func(*app.WalletService, *app.WageringService, *app.QueryService, *app.ReconciliationService) {}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestAppModuleServesUseCases(t *testing.T) {
	pgtest.AppDB(t)
	t.Setenv("DATABASE_URL", pgtest.AppURL())
	var wallets *app.WalletService
	fxApp := fxtest.New(t, fx.NopLogger, config.Module, postgres.Module, app.Module, externalDeps(), fx.Populate(&wallets))
	fxApp.RequireStart()
	defer fxApp.RequireStop()
	cmd, err := app.ParseOpenWallet(uuid.NewString(), "10.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wallets.Open(context.Background(), cmd, app.Meta{}); err != nil {
		t.Fatal(err)
	}
}

func TestAppModuleRejectsInvalidRetryPolicy(t *testing.T) {
	t.Setenv("DATABASE_URL", pgtest.AppURL())
	t.Setenv("REFERENCE_RETRY_MAX_ATTEMPTS", "0")
	fxApp := fx.New(fx.NopLogger, config.Module, postgres.Module, app.Module, externalDeps(),
		fx.Invoke(func(*app.WageringService) {}))
	if fxApp.Err() == nil {
		t.Fatal("an invalid retry policy must stop the application before it starts")
	}
}
