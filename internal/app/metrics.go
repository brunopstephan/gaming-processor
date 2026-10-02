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

// Metrics records business and operational measurements. The HTTP plan
// implements it with Prometheus; NopMetrics discards everything.
type Metrics interface {
	TransactionCompleted(kind wagering.Kind, status wagering.Status, channel Channel)
	IdempotentReplay(channel Channel)
	ConcurrencyConflict(reason string)
	ProcessingDuration(channel Channel, d time.Duration)
	ReconciliationDivergence()
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
