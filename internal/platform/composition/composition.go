// Package composition is the composition root: every Fx module of the
// service, shared by cmd/wallet and the composition tests.
package composition

import (
	"log/slog"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/httpapi"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/postgres"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/logging"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
)

// Modules returns the full application graph. Fx starts modules in
// dependency order and stops them in reverse: the HTTP server drains before
// the database pool closes.
func Modules() fx.Option {
	return fx.Options(
		config.Module, logging.Module, metrics.Module, postgres.Module, app.Module, auth.Module, httpapi.Module,
	)
}

// Logger routes Fx's own events to the service logger.
func Logger() fx.Option {
	return fx.WithLogger(func(l *slog.Logger) fxevent.Logger { return &fxevent.SlogLogger{Logger: l} })
}
