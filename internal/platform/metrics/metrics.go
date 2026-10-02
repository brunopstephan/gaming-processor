// Package metrics exposes Prometheus metrics on a dedicated registry and
// implements app.Metrics.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// Metrics holds every collector of the service.
type Metrics struct {
	Registry     *prometheus.Registry
	HTTPRequests *prometheus.CounterVec
	HTTPDuration *prometheus.HistogramVec

	transactions *prometheus.CounterVec
	replays      *prometheus.CounterVec
	conflicts    *prometheus.CounterVec
	processing   *prometheus.HistogramVec
	divergences  prometheus.Counter

	inboxDuplicates  prometheus.Counter
	sqsRetries       prometheus.Counter
	sqsDLQ           *prometheus.CounterVec
	outboxLag        prometheus.Histogram
	outboxAttempts   *prometheus.CounterVec
	referenceRetries prometheus.Counter
}

var _ app.Metrics = (*Metrics)(nil)

// New registers all collectors on a fresh registry.
func New() *Metrics {
	m := &Metrics{
		Registry: prometheus.NewRegistry(),
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total", Help: "HTTP requests by method, route and status.",
		}, []string{"method", "route", "status"}),
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "http_request_duration_seconds", Help: "HTTP request latency.", Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		transactions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_transactions_total", Help: "Completed wager operations by kind, status and channel.",
		}, []string{"kind", "status", "channel"}),
		replays: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "idempotent_replays_total", Help: "Operations answered from the persisted result.",
		}, []string{"channel"}),
		conflicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "concurrency_conflicts_total", Help: "Transient concurrency conflicts by type.",
		}, []string{"type"}),
		processing: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "processing_duration_seconds", Help: "Wager processing latency by channel.", Buckets: prometheus.DefBuckets,
		}, []string{"channel"}),
		divergences: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "reconciliation_divergences_total", Help: "Reconciliations whose ledger disagrees with the balance.",
		}),
		inboxDuplicates: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "inbox_duplicates_total", Help: "SQS messages dropped because the inbox already handled them.",
		}),
		sqsRetries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sqs_retries_total", Help: "SQS messages left for redelivery after a transient failure.",
		}),
		sqsDLQ: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sqs_dlq_total", Help: "SQS messages sent to the dead-letter queue by reason.",
		}, []string{"reason"}),
		outboxLag: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "outbox_lag_seconds", Help: "Delay between an event's occurrence and its publication.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300},
		}),
		outboxAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbox_publish_attempts_total", Help: "Outbox publish attempts by result.",
		}, []string{"result"}),
		referenceRetries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "reference_retries_total", Help: "Worker attempts that left an operation waiting for its reference.",
		}),
	}
	m.Registry.MustRegister(
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.HTTPRequests, m.HTTPDuration, m.transactions, m.replays, m.conflicts, m.processing, m.divergences,
		m.inboxDuplicates, m.sqsRetries, m.sqsDLQ, m.outboxLag, m.outboxAttempts, m.referenceRetries,
	)
	return m
}

// TransactionCompleted implements app.Metrics.
func (m *Metrics) TransactionCompleted(kind wagering.Kind, status wagering.Status, channel app.Channel) {
	m.transactions.WithLabelValues(string(kind), string(status), string(channel)).Inc()
}

// IdempotentReplay implements app.Metrics.
func (m *Metrics) IdempotentReplay(channel app.Channel) {
	m.replays.WithLabelValues(string(channel)).Inc()
}

// ConcurrencyConflict implements app.Metrics.
func (m *Metrics) ConcurrencyConflict(reason string) { m.conflicts.WithLabelValues(reason).Inc() }

// ProcessingDuration implements app.Metrics.
func (m *Metrics) ProcessingDuration(channel app.Channel, d time.Duration) {
	m.processing.WithLabelValues(string(channel)).Observe(d.Seconds())
}

// ReconciliationDivergence implements app.Metrics.
func (m *Metrics) ReconciliationDivergence() { m.divergences.Inc() }

// InboxDuplicate implements app.Metrics.
func (m *Metrics) InboxDuplicate() { m.inboxDuplicates.Inc() }

// OutboxPublishAttempt implements app.Metrics.
func (m *Metrics) OutboxPublishAttempt(result string) { m.outboxAttempts.WithLabelValues(result).Inc() }

// OutboxLag implements app.Metrics.
func (m *Metrics) OutboxLag(d time.Duration) { m.outboxLag.Observe(d.Seconds()) }

// ReferenceRetry implements app.Metrics.
func (m *Metrics) ReferenceRetry() { m.referenceRetries.Inc() }

// SQSRetry counts a message left for redelivery.
func (m *Metrics) SQSRetry() { m.sqsRetries.Inc() }

// SQSDeadLetter counts a message sent to the DLQ; reason is a failure code.
func (m *Metrics) SQSDeadLetter(reason string) { m.sqsDLQ.WithLabelValues(reason).Inc() }

// Module provides *Metrics and app.Metrics.
var Module = fx.Module("metrics",
	fx.Provide(New, func(m *Metrics) app.Metrics { return m }),
)
