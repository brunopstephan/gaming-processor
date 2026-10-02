package sqsconsumer

import (
	"context"
	"log/slog"

	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/awssqs"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
)

// Module runs the consumer inside the Fx lifecycle. It starts after the
// queues are resolved and stops before the clients and the database pool.
var Module = fx.Module("sqsconsumer",
	fx.Provide(newConsumer),
	fx.Invoke(register),
)

func newConsumer(cfg config.Config, clients *awssqs.Clients, intake *app.IntakeService, m *metrics.Metrics, log *slog.Logger) *Consumer {
	s := cfg.SQS
	return New(clients.Consumer, intake, m, log.With("component", "sqsconsumer"), Settings{
		Workers: s.Workers, WaitTime: s.WaitTime, MaxMessages: s.MaxMessages,
		RetryBaseDelay: s.RetryBaseDelay, RetryMaxDelay: s.RetryMaxDelay,
		ShutdownTimeout: s.ShutdownTimeout, SenderProviders: s.SenderProviders,
	})
}

func register(lc fx.Lifecycle, c *Consumer, q *awssqs.Queues) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			c.s.InputURL, c.s.DLQURL = q.Input, q.InputDLQ
			c.Start()
			return nil
		},
		OnStop: c.Stop,
	})
}
