// Package workers wires the background jobs: the outbox relay and the
// PENDING_REFERENCE worker.
package workers

import (
	"context"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/awssqs"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/background"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

// OutboxModule relays committed outbox events to the events queue.
var OutboxModule = fx.Module("outbox",
	fx.Provide(
		fx.Annotate(awssqs.NewPublisher, fx.As(new(app.EventPublisher))),
		newOutboxRelay,
	),
	fx.Invoke(registerOutbox),
)

func newOutboxRelay(cfg config.Config, repo app.OutboxRelayRepository, pub app.EventPublisher, clock app.Clock, m app.Metrics, log *slog.Logger) (*app.OutboxRelay, error) {
	return app.NewOutboxRelay(repo, pub, clock, m, log.With("component", "outbox"), app.RelaySettings{
		Owner: instanceID(), BatchSize: cfg.Outbox.BatchSize, Lease: cfg.Outbox.Lease,
		RetryBaseDelay: cfg.Outbox.RetryBaseDelay, RetryMaxDelay: cfg.Outbox.RetryMaxDelay,
	})
}

// outboxJob runs one batch; a full batch means there may be more.
func outboxJob(relay *app.OutboxRelay, batch int) background.Job {
	return func(ctx context.Context) (bool, error) {
		n, err := relay.RunOnce(ctx)
		return n == batch, err
	}
}

func registerOutbox(lc fx.Lifecycle, cfg config.Config, relay *app.OutboxRelay, log *slog.Logger) {
	loop := background.NewLoop("outbox", cfg.Outbox.PollInterval, outboxJob(relay, cfg.Outbox.BatchSize), log)
	background.Register(lc, loop, cfg.Outbox.ShutdownTimeout)
}

// instanceID names this process's outbox leases.
func instanceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "wallet"
	}
	return host + "-" + uuid.NewString()[:8]
}
