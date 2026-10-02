//go:build !integration && !e2e

package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// metricName matches the service's metric names inside PromQL expressions.
var metricName = regexp.MustCompile(`\b([a-z_]+_(?:total|seconds)(?:_bucket|_sum|_count)?)\b`)

func dashboardExprs(t *testing.T) []string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "deploy", "grafana", "dashboards", "wallet.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dash struct {
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatalf("dashboard is not valid JSON: %v", err)
	}
	var exprs []string
	for _, p := range dash.Panels {
		if len(p.Targets) == 0 {
			t.Errorf("panel %q has no query", p.Title)
		}
		for _, target := range p.Targets {
			exprs = append(exprs, target.Expr)
		}
	}
	return exprs
}

func TestDashboardQueriesUseRegisteredMetrics(t *testing.T) {
	m := New()
	m.TransactionCompleted(wagering.KindBet, wagering.StatusProcessed, app.ChannelHTTP)
	m.IdempotentReplay(app.ChannelHTTP)
	m.ConcurrencyConflict(app.ConflictVersion)
	m.ProcessingDuration(app.ChannelHTTP, time.Millisecond)
	m.ReconciliationDivergence()
	m.InboxDuplicate()
	m.OutboxPublishAttempt(app.OutboxPublished)
	m.OutboxLag(time.Millisecond)
	m.ReferenceRetry()
	m.SQSRetry()
	m.SQSDeadLetter("INVALID_MONEY")
	m.HTTPRequests.WithLabelValues("GET", "/health/live", "200").Inc()
	m.HTTPDuration.WithLabelValues("GET", "/health/live").Observe(0.01)

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, f := range families {
		registered[f.GetName()] = true
	}
	exprs := dashboardExprs(t)
	if len(exprs) < 10 {
		t.Fatalf("dashboard has %d queries, want at least 10", len(exprs))
	}
	used := map[string]bool{}
	for _, expr := range exprs {
		for _, match := range metricName.FindAllStringSubmatch(expr, -1) {
			name := match[1]
			for _, suffix := range []string{"_bucket", "_sum", "_count"} {
				if base := strings.TrimSuffix(name, suffix); base != name && registered[base] {
					name = base
				}
			}
			if !registered[name] {
				t.Errorf("query %q uses %s, which the service does not register", expr, name)
			}
			used[name] = true
		}
	}
	for _, want := range []string{
		"wager_transactions_total", "idempotent_replays_total", "inbox_duplicates_total", "sqs_retries_total",
		"sqs_dlq_total", "concurrency_conflicts_total", "outbox_lag_seconds", "outbox_publish_attempts_total",
		"processing_duration_seconds", "reconciliation_divergences_total", "reference_retries_total",
		"http_requests_total", "http_request_duration_seconds",
	} {
		if !used[want] {
			t.Errorf("no dashboard panel shows %s", want)
		}
	}
}
