//go:build !unit && !e2e

package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/awssqs"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/background"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

func TestOutboxLoopDeliversEventsToTheQueue(t *testing.T) {
	h := apptest.NewIsolated(t)
	q := sqstest.NewQueues(t, sqstest.Options{})
	owner := sqstest.Owner(t)
	relay, err := app.NewOutboxRelay(h.Outbox, awssqs.NewPublisher(&awssqs.Clients{Publisher: owner}, &awssqs.Queues{Events: q.Events.URL}),
		h.Deps.Clock, h.Metrics, slog.New(slog.DiscardHandler),
		app.RelaySettings{Owner: "relay-test", BatchSize: 50, Lease: 30 * time.Second, RetryBaseDelay: time.Second, RetryMaxDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	loop := background.NewLoop("outbox", 100*time.Millisecond, outboxJob(relay, 50), slog.New(slog.DiscardHandler))
	loop.Start()
	defer func() {
		if err := loop.Stop(context.Background(), 5*time.Second); err != nil {
			t.Error(err)
		}
	}()

	w := h.OpenWallet(t, "100.00")
	ws, _ := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	if _, err := ws.Process(context.Background(), apptest.Command(t, w, "provider-a", "BET", "10.00", ""), app.Meta{}, nil); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"WagerTransactionProcessed": false, "WalletBalanceChanged": false}
	deadline := time.Now().Add(15 * time.Second)
	seen := 0
	for seen < 4 && time.Now().Before(deadline) { // OPENING and BET: Processed + BalanceChanged each
		for _, m := range sqstest.Receive(t, owner, q.Events.URL, 2*time.Second) {
			var env struct {
				EventType   string `json:"eventType"`
				AggregateID string `json:"aggregateId"`
			}
			if err := json.Unmarshal([]byte(aws.ToString(m.Body)), &env); err != nil {
				t.Fatal(err)
			}
			if env.AggregateID != w.ID().String() || m.Attributes["MessageGroupId"] != w.ID().String() {
				t.Fatalf("event %+v in group %s", env, m.Attributes["MessageGroupId"])
			}
			want[env.EventType] = true
			seen++
			sqstest.Delete(t, owner, q.Events.URL, m)
		}
	}
	if seen != 4 || !want["WagerTransactionProcessed"] || !want["WalletBalanceChanged"] {
		t.Fatalf("seen %d events, types %v", seen, want)
	}
	var pending int64
	if err := h.DB.Raw(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&pending).Error; err != nil || pending != 0 {
		t.Fatalf("unpublished = %d %v", pending, err)
	}
}
