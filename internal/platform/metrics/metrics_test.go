//go:build !integration && !e2e

package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

func TestMetricsImplementAppMetrics(t *testing.T) {
	m := New()
	var _ app.Metrics = m
	m.TransactionCompleted(wagering.KindBet, wagering.StatusProcessed, app.ChannelHTTP)
	m.TransactionCompleted(wagering.KindBet, wagering.StatusProcessed, app.ChannelHTTP)
	m.IdempotentReplay(app.ChannelSQS)
	m.ConcurrencyConflict(app.ConflictLockTimeout)
	m.ProcessingDuration(app.ChannelHTTP, 15*time.Millisecond)
	m.ReconciliationDivergence()
	m.InboxDuplicate()
	m.OutboxPublishAttempt(app.OutboxPublished)
	m.OutboxLag(250 * time.Millisecond)
	m.ReferenceRetry()
	m.SQSRetry()
	m.SQSDeadLetter("INVALID_MONEY")
	if testutil.ToFloat64(m.sqsDLQ.WithLabelValues("INVALID_MONEY")) != 1 ||
		testutil.ToFloat64(m.outboxAttempts.WithLabelValues("published")) != 1 {
		t.Fatal("messaging counters not recorded")
	}

	if got := testutil.ToFloat64(m.transactions.WithLabelValues("BET", "PROCESSED", "http")); got != 2 {
		t.Fatalf("wager_transactions_total = %v", got)
	}
	if testutil.ToFloat64(m.replays.WithLabelValues("sqs")) != 1 ||
		testutil.ToFloat64(m.conflicts.WithLabelValues("lock_timeout")) != 1 ||
		testutil.ToFloat64(m.divergences) != 1 {
		t.Fatal("counters not recorded")
	}

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, want := range []string{
		"wager_transactions_total", "idempotent_replays_total", "concurrency_conflicts_total",
		"processing_duration_seconds", "reconciliation_divergences_total", "go_goroutines",
		"inbox_duplicates_total", "sqs_retries_total", "sqs_dlq_total", "outbox_lag_seconds",
		"outbox_publish_attempts_total", "reference_retries_total",
	} {
		if !names[want] {
			t.Errorf("metric %s not registered", want)
		}
	}
}
