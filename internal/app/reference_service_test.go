//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

// worker builds a ReferenceService whose clock runs ahead by skew, so
// pending operations are already due.
func worker(t *testing.T, h *apptest.Harness, policy wagering.RetryPolicy, skew time.Duration, outbox app.OutboxRepository) *app.ReferenceService {
	t.Helper()
	d := h.Deps
	d.Clock = func() time.Time { return apptest.Clock().Add(skew) }
	if outbox != nil {
		d.Outbox = outbox
	}
	ws, err := app.NewWageringService(d, policy)
	if err != nil {
		t.Fatal(err)
	}
	return app.NewReferenceService(d, ws)
}

func processCmd(t *testing.T, h *apptest.Harness, policy wagering.RetryPolicy, cmd wagering.Command) *wagering.WagerTransaction {
	t.Helper()
	ws, err := app.NewWageringService(h.Deps, policy)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ws.Process(context.Background(), cmd, app.Meta{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Transaction
}

func reload(t *testing.T, h *apptest.Harness, tx *wagering.WagerTransaction) *wagering.WagerTransaction {
	t.Helper()
	got, err := h.Transactions.Get(context.Background(), tx.ID())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWorkerResolvesWhenReferenceArrives(t *testing.T) {
	h := apptest.NewIsolated(t)
	policy := wagering.DefaultRetryPolicy()
	w := h.OpenWallet(t, "100.00")
	bet := apptest.Command(t, w, "provider-a", "BET", "25.00", "")
	refund := processCmd(t, h, policy, apptest.Command(t, w, "provider-a", "REFUND", "25.00", bet.ExternalTransactionID))
	if refund.Status() != wagering.StatusPendingReference {
		t.Fatalf("refund = %s", refund.Status())
	}
	betTx := processCmd(t, h, policy, bet)

	rs := worker(t, h, policy, time.Hour, nil)
	found, err := rs.ResumeNext(context.Background())
	if err != nil || !found {
		t.Fatalf("ResumeNext = %v %v", found, err)
	}
	got := reload(t, h, refund)
	if got.Status() != wagering.StatusProcessed || got.ReferenceTransactionID() != betTx.ID() || got.Attempts() != 1 {
		t.Fatalf("refund after worker = %+v", got.State())
	}
	if b, _ := h.Wallets.Get(context.Background(), w.ID()); b.Balance().Amount() != "100.00" {
		t.Fatalf("balance = %s, want the bet refunded", b.Balance().Amount())
	}
	rows := h.OutboxRows(t, w.ID())
	last := rows[len(rows)-1]
	if last.CausationID != refund.ID().String() || h.Metrics.Count("completed:REFUND:PROCESSED:worker") != 1 {
		t.Fatalf("last event %+v, worker completion not recorded", last)
	}
	if found, err := rs.ResumeNext(context.Background()); err != nil || found {
		t.Fatalf("nothing left: %v %v", found, err)
	}
}

func TestWorkerKeepsWaitingThenExpires(t *testing.T) {
	h := apptest.NewIsolated(t)
	policy := wagering.RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Second, MaxAttempts: 3}
	w := h.OpenWallet(t, "100.00")
	pending := processCmd(t, h, policy, apptest.Command(t, w, "provider-a", "ROLLBACK", "5.00", "never-arrives"))

	// Each attempt schedules the next one from the worker's clock, so every
	// later attempt needs a clock further ahead.
	if found, err := worker(t, h, policy, time.Hour, nil).ResumeNext(context.Background()); err != nil || !found {
		t.Fatalf("second attempt = %v %v", found, err)
	}
	got := reload(t, h, pending)
	if got.Status() != wagering.StatusPendingReference || got.Attempts() != 2 || h.Metrics.Count("reference_retry") != 1 {
		t.Fatalf("after retry = %+v (retries %d)", got.State(), h.Metrics.Count("reference_retry"))
	}

	if found, err := worker(t, h, policy, 2*time.Hour, nil).ResumeNext(context.Background()); err != nil || !found {
		t.Fatalf("last attempt = %v %v", found, err)
	}
	got = reload(t, h, pending)
	if got.Status() != wagering.StatusRejected || got.FailureCode() != wagering.FailureReferenceNotFound {
		t.Fatalf("expired = %+v", got.State())
	}
	types := map[string]int{}
	for _, r := range h.OutboxRows(t, w.ID()) {
		types[r.EventType]++
	}
	if types["WagerTransactionPendingReference"] != 1 || types["WagerTransactionRejected"] != 1 {
		t.Fatalf("event types = %v, want one pending and one rejected", types)
	}
}

func TestWorkerSkipsOperationsNotYetDue(t *testing.T) {
	h := apptest.NewIsolated(t)
	w := h.OpenWallet(t, "100.00")
	processCmd(t, h, wagering.DefaultRetryPolicy(), apptest.Command(t, w, "provider-a", "REFUND", "5.00", "later"))
	if found, err := worker(t, h, wagering.DefaultRetryPolicy(), 0, nil).ResumeNext(context.Background()); err != nil || found {
		t.Fatalf("not due = %v %v", found, err)
	}
}

func TestConcurrentWorkersResumeEachOperationOnce(t *testing.T) {
	h := apptest.NewIsolated(t)
	policy := wagering.DefaultRetryPolicy()
	const n = 6
	refunds := make([]*wagering.WagerTransaction, n)
	for i := range n {
		w := h.OpenWallet(t, "100.00")
		bet := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
		refunds[i] = processCmd(t, h, policy, apptest.Command(t, w, "provider-a", "REFUND", "10.00", bet.ExternalTransactionID))
		processCmd(t, h, policy, bet)
	}
	var resumed atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		rs := worker(t, h, policy, time.Hour, nil) // built here: t.Fatal only in the test goroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				found, err := rs.ResumeNext(context.Background())
				if err != nil {
					errs <- err
					return
				}
				if !found {
					return
				}
				resumed.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if resumed.Load() != n {
		t.Fatalf("resumed = %d, want %d (each exactly once)", resumed.Load(), n)
	}
	for _, r := range refunds {
		if reload(t, h, r).Status() != wagering.StatusProcessed {
			t.Fatalf("refund %s not processed", r.ID())
		}
	}
}

type failingOutbox struct{}

func (failingOutbox) Append(context.Context, ...events.Envelope) error {
	return errors.New("disk full")
}

func TestPermanentFailureMarksThePendingRowFailed(t *testing.T) {
	h := apptest.NewIsolated(t)
	policy := wagering.DefaultRetryPolicy()
	w := h.OpenWallet(t, "100.00")
	bet := apptest.Command(t, w, "provider-a", "BET", "20.00", "")
	refund := processCmd(t, h, policy, apptest.Command(t, w, "provider-a", "REFUND", "20.00", bet.ExternalTransactionID))
	processCmd(t, h, policy, bet)

	found, err := worker(t, h, policy, time.Hour, failingOutbox{}).ResumeNext(context.Background())
	if err != nil || !found {
		t.Fatalf("ResumeNext = %v %v", found, err)
	}
	got := reload(t, h, refund)
	if got.Status() != wagering.StatusFailed || got.FailureCode() != wagering.FailureInfrastructure {
		t.Fatalf("refund = %+v, want FAILED/INFRASTRUCTURE_FAILURE", got.State())
	}
	if b, _ := h.Wallets.Get(context.Background(), w.ID()); b.Balance().Amount() != "80.00" {
		t.Fatalf("balance = %s, a failed resume must not move money", b.Balance().Amount())
	}
}
