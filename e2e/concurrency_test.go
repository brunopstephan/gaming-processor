//go:build !unit && !integration

package e2e

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
)

func threeInstances(t *testing.T, opts clusterOptions) (*cluster, []*proc) {
	t.Helper()
	c := newCluster(t, opts)
	return c, []*proc{c.start(t, "a", nil), c.start(t, "b", nil), c.start(t, "c", nil)}
}

// Scenario 1: the same bet 50 times in parallel across 3 instances → one debit.
func TestSameBetFiftyTimesAcrossInstances(t *testing.T) {
	_, ps := threeInstances(t, clusterOptions{})
	w := openWallet(t, ps[0], "100.00")
	token := kctest.Token(t, "provider-a")
	req := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "25.00", W: w}

	type result struct {
		code int
		body map[string]any
	}
	results := make([]result, 50)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			code, body := postTxNoFatal(ps[i%3], token, req)
			results[i] = result{code, body}
		}()
	}
	close(start)
	wg.Wait()

	fresh, id := 0, ""
	for i, r := range results {
		if r.code != http.StatusOK || r.body["status"] != "PROCESSED" || r.body["balance"].(map[string]any)["amount"] != "75.00" {
			t.Fatalf("request %d = %d %v", i, r.code, r.body)
		}
		if r.body["idempotentReplay"] == false {
			fresh++
		}
		if id == "" {
			id = r.body["transactionId"].(string)
		} else if r.body["transactionId"] != id {
			t.Fatalf("request %d answered another transaction %v", i, r.body["transactionId"])
		}
	}
	if fresh != 1 {
		t.Fatalf("%d requests applied the bet, want exactly 1 (49 replays)", fresh)
	}
	if b := balance(t, ps[1], w); b != "75.00" {
		t.Fatalf("balance = %s, want one debit", b)
	}
	reconcile(t, ps[2], w)
}

// postTxNoFatal is postTx for goroutines: it never calls t.Fatal.
func postTxNoFatal(p *proc, token string, r txRequest) (int, map[string]any) {
	req, _ := http.NewRequest("POST", p.url()+"/wagering/transactions", stringsReader(r.json()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", r.Provider+":"+r.Ext)
	resp, err := httpClient.Do(req)
	if err != nil {
		return -1, map[string]any{"error": err.Error()}
	}
	defer resp.Body.Close()
	return resp.StatusCode, decodeBody(resp)
}

// Scenario 2: 80 + 80 on 100 from two instances while the wallet row is held
// by an outside transaction: both wait on the database lock across
// processes; one is processed, the other rejected; resends are unchanged.
func TestTwoBetsOfEightyOnOneHundred(t *testing.T) {
	c, ps := threeInstances(t, clusterOptions{Env: map[string]string{"DB_LOCK_TIMEOUT": "15s"}})
	token := kctest.Token(t, "provider-a")
	for round := range 3 {
		w := openWallet(t, ps[0], "100.00")
		first := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "80.00", W: w}
		second := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "80.00", W: w}

		holder := c.db.Begin()
		if holder.Error != nil {
			t.Fatal(holder.Error)
		}
		if err := holder.Exec(`SELECT id FROM wallets WHERE id = ? FOR UPDATE`, w.id).Error; err != nil {
			holder.Rollback()
			t.Fatal(err)
		}
		defer holder.Rollback()
		codes := make([]int, 2)
		bodies := make([]map[string]any, 2)
		var finished atomic.Int32
		var wg sync.WaitGroup
		for i, r := range []txRequest{first, second} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes[i], bodies[i] = postTxNoFatal(ps[i], token, r)
				finished.Add(1)
			}()
		}
		eventually(t, 15*time.Second, "both requests blocked on the wallet lock", func() bool {
			var n int
			if err := c.db.Raw(`SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND wait_event_type = 'Lock'`).Scan(&n).Error; err != nil {
				return false
			}
			return n >= 2
		})
		if n := finished.Load(); n != 0 {
			holder.Rollback()
			t.Fatalf("round %d: %d requests finished while the wallet lock was held", round, n)
		}
		holder.Rollback()
		wg.Wait()

		processed, rejected := -1, -1
		for i := range codes {
			switch {
			case codes[i] == http.StatusOK && bodies[i]["status"] == "PROCESSED":
				processed = i
			case codes[i] == http.StatusUnprocessableEntity && bodies[i]["failureCode"] == "INSUFFICIENT_FUNDS":
				rejected = i
			}
		}
		if processed < 0 || rejected < 0 {
			t.Fatalf("round %d: responses %d %v / %d %v, want one PROCESSED and one INSUFFICIENT_FUNDS", round, codes[0], bodies[0], codes[1], bodies[1])
		}
		if b := balance(t, ps[2], w); b != "20.00" {
			t.Fatalf("round %d: balance = %s, want 20.00", round, b)
		}
		for i, r := range []txRequest{first, second} {
			code, body := postTx(t, ps[2], token, r)
			if code != codes[i] || body["transactionId"] != bodies[i]["transactionId"] || body["idempotentReplay"] != true {
				t.Fatalf("round %d: resend %d = %d %v, want the original %d %v", round, i, code, body, codes[i], bodies[i])
			}
		}
		reconcile(t, ps[1], w)
	}
}

// Scenario 3: distinct wallets processed simultaneously across instances.
func TestDistinctWalletsInParallel(t *testing.T) {
	_, ps := threeInstances(t, clusterOptions{})
	token := kctest.Token(t, "provider-a")
	wallets := make([]wallet, 10)
	for i := range wallets {
		wallets[i] = openWallet(t, ps[i%3], "50.00")
	}
	var wg sync.WaitGroup
	failures := make(chan string, 100)
	for i, w := range wallets {
		for j := range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "1.00", W: w}
				if code, body := postTxNoFatal(ps[(i+j)%3], token, r); code != http.StatusOK {
					failures <- fmt.Sprintf("wallet %d bet %d = %d %v", i, j, code, body)
				}
			}()
		}
	}
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
	for _, w := range wallets {
		if b := balance(t, ps[0], w); b != "40.00" {
			t.Fatalf("wallet %s balance = %s, want 40.00", w.id, b)
		}
		reconcile(t, ps[1], w)
	}
}

// Cross-channel: the same operation by HTTP and by SQS (any instance) → one debit.
func TestSameOperationOverHTTPAndSQS(t *testing.T) {
	c, ps := threeInstances(t, clusterOptions{})
	w := openWallet(t, ps[0], "100.00")
	r := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "30.00", W: w}
	if code, body := postTx(t, ps[0], kctest.Token(t, "provider-a"), r); code != http.StatusOK {
		t.Fatalf("http = %d %v", code, body)
	}
	c.sendSQS(t, sqsBody("msg-"+uuid.NewString(), r), w.id)
	eventually(t, 20*time.Second, "SQS message consumed", func() bool { return c.queueEmpty(t, c.queues.Input.URL) })
	replays := 0.0
	for _, p := range ps {
		replays += p.metric(t, "idempotent_replays_total")
	}
	if replays < 1 {
		t.Fatal("the SQS delivery must be answered as a replay")
	}
	if b := balance(t, ps[1], w); b != "70.00" {
		t.Fatalf("balance = %s, want one debit", b)
	}
	reconcile(t, ps[2], w)
}

// Repeated SQS receptions of one message (new deduplication id each time)
// are dropped by the application's inbox, not by SQS.
func TestRepeatedSQSReceptionsAreDeduplicated(t *testing.T) {
	c, ps := threeInstances(t, clusterOptions{})
	w := openWallet(t, ps[0], "100.00")
	body := sqsBody("msg-"+uuid.NewString(), txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "10.00", W: w})
	for range 5 {
		c.sendSQS(t, body, w.id)
	}
	eventually(t, 30*time.Second, "all receptions handled", func() bool { return c.queueEmpty(t, c.queues.Input.URL) })
	duplicates := 0.0
	for _, p := range ps {
		duplicates += p.metric(t, "inbox_duplicates_total")
	}
	if duplicates != 4 {
		t.Fatalf("inbox duplicates = %v, want 4 (5 receptions, 1 applied)", duplicates)
	}
	if b := balance(t, ps[1], w); b != "90.00" {
		t.Fatalf("balance = %s, want one debit", b)
	}
	reconcile(t, ps[2], w)
}
