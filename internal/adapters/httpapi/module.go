package httpapi

import (
	"log/slog"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
)

type routerIn struct {
	fx.In
	Auth           auth.Authenticator
	Wallets        *app.WalletService
	Wagering       *app.WageringService
	Queries        *app.QueryService
	Reconciliation *app.ReconciliationService
	Metrics        *metrics.Metrics
	Readiness      *health.Readiness
	Log            *slog.Logger
}

type readinessIn struct {
	fx.In
	Checks []health.Check `group:"readiness"`
}

// Module provides the router and the server and starts it.
var Module = fx.Module("httpapi",
	fx.Provide(
		func(in readinessIn) *health.Readiness { return health.NewReadiness(in.Checks) },
		func(in routerIn) *gin.Engine {
			return NewRouter(RouterDeps{
				Auth: in.Auth, Wallets: in.Wallets, Wagering: in.Wagering, Queries: in.Queries,
				Reconciliation: in.Reconciliation, Metrics: in.Metrics, Readiness: in.Readiness, Log: in.Log,
			})
		},
		NewServer,
	),
	fx.Invoke(func(*Server) {}),
)
