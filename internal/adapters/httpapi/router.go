// Package httpapi is the HTTP adapter: Gin router, authentication and
// authorization middleware, handlers over the application services and the
// error contract.
package httpapi

import (
	"log/slog"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
)

// RouterDeps are the collaborators of the HTTP API.
type RouterDeps struct {
	Auth           auth.Authenticator
	Wallets        *app.WalletService
	Wagering       *app.WageringService
	Queries        *app.QueryService
	Reconciliation *app.ReconciliationService
	Metrics        *metrics.Metrics
	Readiness      *health.Readiness
	Log            *slog.Logger
}

func init() { gin.SetMode(gin.ReleaseMode) }

// NewRouter builds the API.
func NewRouter(d RouterDeps) *gin.Engine {
	r := gin.New()
	r.Use(withCorrelation, withLogger(d.Log), accessLog, instrument(d.Metrics), recoverJSON)

	r.GET("/health/live", liveHandler)
	r.GET("/health/ready", readyHandler(d.Readiness))
	r.GET("/metrics", gin.WrapH(promhttp.HandlerFor(d.Metrics.Registry, promhttp.HandlerOpts{})))

	authn := authenticate(d.Auth)
	wallets := r.Group("/wallets", authn, requireAnyScope(auth.ScopeWallets))
	wh := walletHandlers{wallets: d.Wallets, queries: d.Queries, reconciliation: d.Reconciliation}
	wallets.POST("", wh.open)
	wallets.GET("/:walletId", wh.get)
	wallets.GET("/:walletId/ledger", wh.ledger)
	wallets.POST("/:walletId/reconciliation", wh.reconcile)

	return r
}
