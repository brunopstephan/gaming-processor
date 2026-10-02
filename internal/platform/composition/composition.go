// Package composition is the composition root: the Fx modules of the
// service, chosen by the component toggles, shared by cmd/wallet and the
// composition tests.
package composition

import (
	"log/slog"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/awssqs"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/httpapi"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/postgres"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/sqsconsumer"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/workers"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/logging"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
)

// stopMargin leaves room, after every drain, to close SQS and PostgreSQL.
const stopMargin = 10 * time.Second

// Modules returns the application graph for cfg. Fx runs OnStop hooks in the
// reverse of construction order: the HTTP server (constructed last) stops
// first, then the workers and the consumer drain, and the database pool
// closes last.
func Modules(cfg config.Config) fx.Option {
	opts := []fx.Option{fx.Supply(cfg), logging.Module, metrics.Module, postgres.Module, app.Module}
	if cfg.Toggles.Consumer || cfg.Toggles.Outbox {
		opts = append(opts, awssqs.Module)
	}
	if cfg.Toggles.Consumer {
		opts = append(opts, sqsconsumer.Module)
	}
	if cfg.Toggles.Outbox {
		opts = append(opts, workers.OutboxModule)
	}
	if cfg.Toggles.RefWorker {
		opts = append(opts, workers.ReferenceModule)
	}
	// Last, so the server is constructed last and its OnStop runs first: HTTP
	// stops accepting (readiness draining) before the consumer and workers drain.
	if cfg.Toggles.HTTP {
		opts = append(opts, auth.Module, httpapi.Module)
	}
	return fx.Options(opts...)
}

// StopTimeout is the Fx stop budget. OnStop hooks run one after another, so
// it is the sum of the enabled components' drain timeouts plus a margin.
func StopTimeout(cfg config.Config) time.Duration {
	d := stopMargin
	if cfg.Toggles.HTTP {
		d += cfg.HTTP.ShutdownTimeout
	}
	if cfg.Toggles.Consumer {
		d += cfg.SQS.ShutdownTimeout + sqsconsumer.AbortGrace
	}
	if cfg.Toggles.Outbox {
		d += cfg.Outbox.ShutdownTimeout
	}
	if cfg.Toggles.RefWorker {
		d += cfg.RefWorker.ShutdownTimeout
	}
	return d
}

// Logger routes Fx's own events to the service logger.
func Logger() fx.Option {
	return fx.WithLogger(func(l *slog.Logger) fxevent.Logger { return &fxevent.SlogLogger{Logger: l} })
}
