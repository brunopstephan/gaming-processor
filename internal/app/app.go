package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// Clock returns the current instant in UTC. It is injected so tests control time.
type Clock func() time.Time

// SystemClock is the production Clock.
func SystemClock() time.Time { return time.Now().UTC() }

// Channel identifies how an operation entered the service.
type Channel string

// Channels.
const (
	ChannelHTTP   Channel = "http"
	ChannelSQS    Channel = "sqs"
	ChannelWorker Channel = "worker"
)

// Meta carries the tracing identifiers of one request or message into
// events and logs. CausationID is the SQS messageId (empty over HTTP).
type Meta struct {
	CorrelationID string
	CausationID   string
	Channel       Channel
}

// normalized fills a missing correlation id and channel.
func (m Meta) normalized() Meta {
	if m.CorrelationID == "" {
		m.CorrelationID = newID().String()
	}
	if m.Channel == "" {
		m.Channel = ChannelHTTP
	}
	return m
}

// Viewer is who reads a transaction: a provider sees only its own
// operations; the internal service sees everything.
type Viewer struct {
	ProviderID string
	Internal   bool
}

func (v Viewer) canSee(t *wagering.WagerTransaction) bool {
	return v.Internal || (v.ProviderID != "" && t.ProviderID() == v.ProviderID)
}

// Deps are the ports and services shared by the use cases.
type Deps struct {
	Tx           TxManager
	Wallets      WalletRepository
	Transactions TransactionRepository
	Ledger       LedgerRepository
	Outbox       OutboxRepository
	Clock        Clock
	Metrics      Metrics
	Log          *slog.Logger
}

// newID returns a time-ordered UUIDv7. It only panics if the system random
// source fails, which is not a business condition.
func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// run calls fn when it is set.
func run(ctx context.Context, fn func(ctx context.Context) error) error {
	if fn == nil {
		return nil
	}
	return fn(ctx)
}

// publish wraps recorded domain events in envelopes and writes them to the
// outbox inside the caller's transaction.
func publish(ctx context.Context, outbox OutboxRepository, now time.Time, data []events.Data, meta Meta) error {
	if len(data) == 0 {
		return nil
	}
	envs := make([]events.Envelope, 0, len(data))
	for _, d := range data {
		env, err := events.NewEnvelope(newID(), d, meta.CorrelationID, meta.CausationID, now)
		if err != nil {
			return err
		}
		envs = append(envs, env)
	}
	return outbox.Append(ctx, envs...)
}
