package app

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/faultinject"
)

// RelaySettings tune the outbox relay. Owner identifies this instance's
// leases; Lease must exceed the time to publish one batch.
type RelaySettings struct {
	Owner          string
	BatchSize      int
	Lease          time.Duration
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
}

// OutboxRelay publishes committed outbox events. Each batch is leased to this
// instance; an event is marked published only after the broker accepted it,
// and a failed publish is rescheduled with capped backoff. If the instance
// dies between publishing and marking, the lease expires and another
// instance republishes the event with the same eventId: delivery is at least
// once and consumers deduplicate by eventId. Events are never dropped.
type OutboxRelay struct {
	repo    OutboxRelayRepository
	pub     EventPublisher
	clock   Clock
	metrics Metrics
	log     *slog.Logger
	s       RelaySettings
}

// NewOutboxRelay validates the settings and builds the relay.
func NewOutboxRelay(repo OutboxRelayRepository, pub EventPublisher, clock Clock, metrics Metrics, log *slog.Logger, s RelaySettings) (*OutboxRelay, error) {
	switch {
	case s.Owner == "":
		return nil, errors.New("app: relay owner is required")
	case s.BatchSize < 1 || s.Lease <= 0 || s.RetryBaseDelay <= 0 || s.RetryMaxDelay < s.RetryBaseDelay:
		return nil, errors.New("app: relay batch size, lease and retry delays must be positive (max >= base)")
	}
	return &OutboxRelay{repo: repo, pub: pub, clock: clock, metrics: metrics, log: log, s: s}, nil
}

// RunOnce publishes one batch and returns how many events it claimed.
// Events left unpublished when ctx ends are retried after their lease.
func (r *OutboxRelay) RunOnce(ctx context.Context) (int, error) {
	events, err := r.repo.Claim(ctx, r.s.Owner, r.s.Lease, r.s.BatchSize)
	if err != nil {
		return 0, err
	}
	for _, e := range events {
		if ctx.Err() != nil {
			break
		}
		r.publish(ctx, e)
	}
	return len(events), nil
}

func (r *OutboxRelay) publish(ctx context.Context, e OutboxEvent) {
	log := r.log.With("eventId", e.ID.String(), "eventType", e.EventType, "walletId", e.AggregateID.String(), "attempts", e.Attempts)
	// A hung publish must not outlive the lease another relay could take over.
	pctx, cancel := context.WithTimeout(ctx, r.s.Lease/2)
	defer cancel()
	if err := r.pub.Publish(pctx, e); err != nil {
		r.metrics.OutboxPublishAttempt(OutboxFailed)
		delay := backoff(e.Attempts, r.s.RetryBaseDelay, r.s.RetryMaxDelay)
		log.WarnContext(ctx, "publish failed; rescheduled", "error", err.Error(), "retryIn", delay.String())
		if err := r.repo.Reschedule(context.WithoutCancel(ctx), e.ID, r.s.Owner, delay, err.Error()); err != nil {
			log.ErrorContext(ctx, "reschedule failed; the lease will expire", "error", err.Error())
		}
		return
	}
	r.metrics.OutboxPublishAttempt(OutboxPublished)
	r.metrics.OutboxLag(r.clock().Sub(e.OccurredAt))
	faultinject.Hit(faultinject.CrashAfterPublishBeforeMark)
	ok, err := r.repo.MarkPublished(context.WithoutCancel(ctx), e.ID, r.s.Owner)
	switch {
	case err != nil:
		log.ErrorContext(ctx, "mark failed; the event will be republished with the same eventId", "error", err.Error())
	case !ok:
		log.InfoContext(ctx, "lease lost before marking; another relay owns the event")
	}
}

// backoff is base×2^(attempt−1), capped at maxDelay.
func backoff(attempt int, base, maxDelay time.Duration) time.Duration {
	d := base
	for i := 1; i < attempt && d < maxDelay; i++ {
		d *= 2
	}
	return min(d, maxDelay)
}
