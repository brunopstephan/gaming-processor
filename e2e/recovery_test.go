//go:build !unit && !integration

package e2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/faultinject"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

// Scenario 5: a consumer crashes after the commit and before deleting the
// message; another instance receives it again and drops it via the inbox.
func TestCrashAfterCommitBeforeDelete(t *testing.T) {
	c := newCluster(t, clusterOptions{VisibilityTimeout: 3})
	crashing := c.start(t, "crashing", map[string]string{"FAULT": faultinject.CrashAfterCommitBeforeDelete})
	w := openWallet(t, crashing, "100.00")
	c.sendSQS(t, sqsBody("msg-"+uuid.NewString(), txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "40.00", W: w}), w.id)
	if code := crashing.waitExit(t, 30*time.Second); code != faultinject.ExitCode {
		t.Fatalf("exit code = %d, want the injected crash %d", code, faultinject.ExitCode)
	}

	survivor := c.start(t, "survivor", nil)
	if b := balance(t, survivor, w); b != "60.00" {
		t.Fatalf("balance after crash = %s, want the committed debit", b)
	}
	eventually(t, 30*time.Second, "redelivery handled", func() bool { return c.queueEmpty(t, c.queues.Input.URL) })
	if d := survivor.metric(t, "inbox_duplicates_total"); d < 1 {
		t.Fatalf("inbox duplicates = %v, the redelivery must be dropped by the inbox", d)
	}
	if b := balance(t, survivor, w); b != "60.00" {
		t.Fatalf("balance after redelivery = %s, want one debit", b)
	}
	reconcile(t, survivor, w)
}

// Scenario 6: two publishers share the outbox; one crashes after publishing
// and before marking; the other republishes with the same eventId after the
// lease expires. SQS FIFO deduplication keeps a single copy in the queue.
func TestCrashAfterPublishBeforeMark(t *testing.T) {
	c := newCluster(t, clusterOptions{Env: map[string]string{"OUTBOX_LEASE": "2s", "CONSUMER_ENABLED": "false", "REFWORKER_ENABLED": "false"}})
	// A slower poll on the crashing publisher leaves time for the HTTP response
	// of the wallet opening before its relay claims the OPENING events and crashes.
	crashing := c.start(t, "crashing", map[string]string{"FAULT": faultinject.CrashAfterPublishBeforeMark, "OUTBOX_POLL_INTERVAL": "1s"})
	w := openWallet(t, crashing, "100.00")
	if code := crashing.waitExit(t, 30*time.Second); code != faultinject.ExitCode {
		t.Fatalf("exit code = %d, want the injected crash %d", code, faultinject.ExitCode)
	}

	b1 := c.start(t, "publisher-b", nil)
	c.start(t, "publisher-c", nil)
	if code, body := postTx(t, b1, kctest.Token(t, "provider-a"), txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "5.00", W: w}); code != 200 {
		t.Fatalf("bet = %d %v", code, body)
	}

	var rows []struct {
		ID       string
		Attempts int
	}
	eventually(t, 30*time.Second, "every event published", func() bool {
		var pending int64
		if err := c.db.Raw(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&pending).Error; err != nil {
			t.Fatal(err)
		}
		return pending == 0
	})
	if err := c.db.Raw(`SELECT id::text AS id, attempts FROM outbox_events`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	republished := 0
	for _, r := range rows {
		if r.Attempts >= 2 {
			republished++
		}
	}
	if republished < 1 {
		t.Fatalf("no event was republished after the crash: %+v", rows)
	}

	seen := map[string]int{}
	owner := sqstest.Owner(t)
	for deadline := time.Now().Add(20 * time.Second); len(seen) < len(rows) && time.Now().Before(deadline); {
		for _, m := range sqstest.Receive(t, owner, c.queues.Events.URL, 2*time.Second) {
			var env struct {
				EventID string `json:"eventId"`
			}
			if err := json.Unmarshal([]byte(aws.ToString(m.Body)), &env); err != nil {
				t.Fatal(err)
			}
			seen[env.EventID]++
			sqstest.Delete(t, owner, c.queues.Events.URL, m)
		}
	}
	for _, r := range rows {
		if seen[r.ID] != 1 {
			t.Fatalf("event %s delivered %d times, want exactly once (republished with the same eventId)", r.ID, seen[r.ID])
		}
	}
	eventually(t, 5*time.Second, "events queue drained with no late duplicate", func() bool { return c.queueEmpty(t, c.queues.Events.URL) })
	reconcile(t, b1, w)
}

// Scenario 7: reversals delivered before their reference are resolved by the
// worker (on any instance) or expire with REFERENCE_NOT_FOUND.
func TestReversalBeforeReference(t *testing.T) {
	c := newCluster(t, clusterOptions{Env: map[string]string{
		"REFERENCE_RETRY_BASE_DELAY": "200ms", "REFERENCE_RETRY_MAX_DELAY": "200ms", "REFERENCE_RETRY_MAX_ATTEMPTS": "50",
	}})
	a, b := c.start(t, "a", nil), c.start(t, "b", nil)
	token := kctest.Token(t, "provider-a")
	w := openWallet(t, a, "100.00")

	bet := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "30.00", W: w}
	rollback := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "ROLLBACK", Amount: "30.00", Ref: bet.Ext, W: w}
	c.sendSQS(t, sqsBody("msg-"+uuid.NewString(), rollback), w.id)
	eventually(t, 20*time.Second, "rollback pending", func() bool {
		v := getByExternal(t, a, "provider-a", rollback.Ext)
		return v != nil && v["status"] == "PENDING_REFERENCE"
	})
	if code, body := postTx(t, b, token, bet); code != 200 {
		t.Fatalf("bet = %d %v", code, body)
	}
	eventually(t, 20*time.Second, "rollback resolved", func() bool {
		v := getByExternal(t, b, "provider-a", rollback.Ext)
		return v != nil && v["status"] == "PROCESSED"
	})
	if bal := balance(t, a, w); bal != "100.00" {
		t.Fatalf("balance = %s, want the bet rolled back", bal)
	}

	orphan := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "REFUND", Amount: "10.00", Ref: "never-" + uuid.NewString(), W: w}
	if code, body := postTx(t, a, token, orphan); code != 202 {
		t.Fatalf("orphan refund = %d %v, want 202 PENDING_REFERENCE", code, body)
	}
	eventually(t, 30*time.Second, "orphan expired", func() bool {
		v := getByExternal(t, b, "provider-a", orphan.Ext)
		return v != nil && v["status"] == "REJECTED" && v["failureCode"] == "REFERENCE_NOT_FOUND"
	})
	reconcile(t, b, w)
}

// Scenario 8: one instance is killed with SIGKILL and the other stopped with
// SIGTERM (the pending refund was created on the latter); a new instance
// preserves idempotency, resumes the pending operation and keeps the books
// consistent. In-flight crashes are covered by scenarios 5 and 6.
func TestRestartPreservesIdempotencyAndPendings(t *testing.T) {
	c := newCluster(t, clusterOptions{Env: map[string]string{
		"REFERENCE_RETRY_BASE_DELAY": "1s", "REFERENCE_RETRY_MAX_DELAY": "1s", "REFERENCE_RETRY_MAX_ATTEMPTS": "60",
	}})
	a, b := c.start(t, "a", nil), c.start(t, "b", nil)
	token := kctest.Token(t, "provider-a")
	w := openWallet(t, a, "100.00")
	bet := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "20.00", W: w}
	code, original := postTx(t, a, token, bet)
	if code != 200 {
		t.Fatalf("original bet = %d %v", code, original)
	}
	laterBet := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "15.00", W: w}
	refund := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "REFUND", Amount: "15.00", Ref: laterBet.Ext, W: w}
	if code, body := postTx(t, b, token, refund); code != 202 {
		t.Fatalf("refund = %d %v, want pending", code, body)
	}

	a.kill(t)
	if code := b.stop(t); code != 0 {
		t.Fatalf("graceful stop exit code = %d, want 0", code)
	}

	n := c.start(t, "restarted", nil)
	code, replay := postTx(t, n, token, bet)
	replayBalance, ok := replay["balance"].(map[string]any)
	if !ok {
		t.Fatalf("replay after restart = %d %v, balance missing or malformed", code, replay)
	}
	if code != 200 || replay["idempotentReplay"] != true || replay["transactionId"] != original["transactionId"] ||
		replayBalance["amount"] != "80.00" {
		t.Fatalf("replay after restart = %d %v, want the original result %v", code, replay, original)
	}
	if code, body := postTx(t, n, token, laterBet); code != 200 {
		t.Fatalf("later bet = %d %v", code, body)
	}
	eventually(t, 20*time.Second, "pending refund resumed", func() bool {
		v := getByExternal(t, n, "provider-a", refund.Ext)
		return v != nil && v["status"] == "PROCESSED"
	})
	if bal := balance(t, n, w); bal != "80.00" {
		t.Fatalf("balance = %s, want 100 − 20 − 15 + 15", bal)
	}
	reconcile(t, n, w)
}
