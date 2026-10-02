package app

import (
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// Concurrency conflict reasons reported to Metrics.
const (
	ConflictLockTimeout = "lock_timeout"
	ConflictVersion     = "version"
	ConflictUnique      = "unique"
	ConflictTransient   = "transient"
)

// Outbox publish results reported to Metrics.
const (
	OutboxPublished = "published"
	OutboxFailed    = "failed"
)

// Metrics records business and operational measurements. The HTTP plan
// implements it with Prometheus; NopMetrics discards everything.
type Metrics interface {
	TransactionCompleted(kind wagering.Kind, status wagering.Status, channel Channel)
	IdempotentReplay(channel Channel)
	ConcurrencyConflict(reason string)
	ProcessingDuration(channel Channel, d time.Duration)
	ReconciliationDivergence()
	// InboxDuplicate counts a message already handled with the same payload.
	InboxDuplicate()
	// OutboxPublishAttempt counts one publish attempt of an outbox event by result.
	OutboxPublishAttempt(result string)
	// OutboxLag observes the delay between an event's occurrence and its publication.
	OutboxLag(d time.Duration)
	// ReferenceRetry counts a worker attempt that left an operation waiting for its reference.
	ReferenceRetry()
}

// NopMetrics is a Metrics that records nothing.
type NopMetrics struct{}

var _ Metrics = NopMetrics{}

// TransactionCompleted implements Metrics.
func (NopMetrics) TransactionCompleted(wagering.Kind, wagering.Status, Channel) {}

// IdempotentReplay implements Metrics.
func (NopMetrics) IdempotentReplay(Channel) {}

// ConcurrencyConflict implements Metrics.
func (NopMetrics) ConcurrencyConflict(string) {}

// ProcessingDuration implements Metrics.
func (NopMetrics) ProcessingDuration(Channel, time.Duration) {}

// ReconciliationDivergence implements Metrics.
func (NopMetrics) ReconciliationDivergence() {}

// InboxDuplicate implements Metrics.
func (NopMetrics) InboxDuplicate() {}

// OutboxPublishAttempt implements Metrics.
func (NopMetrics) OutboxPublishAttempt(string) {}

// OutboxLag implements Metrics.
func (NopMetrics) OutboxLag(time.Duration) {}

// ReferenceRetry implements Metrics.
func (NopMetrics) ReferenceRetry() {}
