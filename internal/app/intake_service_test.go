//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

func newIntake(t *testing.T, h *apptest.Harness) (*app.IntakeService, *app.WageringService) {
	t.Helper()
	ws, err := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return app.NewIntakeService(h.Deps, h.Inbox, ws), ws
}

func TestIntakeHandlesOnceAndRecordsInbox(t *testing.T) {
	h := apptest.New(t)
	intake, _ := newIntake(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "25.00", "")
	msgID := "msg-" + uuid.NewString()
	ctx := context.Background()

	res, err := intake.Handle(ctx, msgID, cmd, app.Meta{CorrelationID: "corr-1"})
	if err != nil || res.Outcome != app.IntakeHandled || res.Replay || res.Transaction.Status() != wagering.StatusProcessed {
		t.Fatalf("first = %+v %v", res, err)
	}
	inbox, err := h.Inbox.Get(ctx, app.ConsumerWagerTransactions, msgID)
	if err != nil || inbox.PayloadHash != app.InboxHash(cmd) {
		t.Fatalf("inbox = %+v %v", inbox, err)
	}
	rows := h.OutboxRows(t, w.ID())
	last := rows[len(rows)-1]
	if last.CausationID != msgID || last.CorrelationID != "corr-1" {
		t.Fatalf("event causation/correlation = %q/%q", last.CausationID, last.CorrelationID)
	}
	if h.Metrics.Count("completed:BET:PROCESSED:sqs") != 1 {
		t.Fatal("completion not counted on the sqs channel")
	}

	again, err := intake.Handle(ctx, msgID, cmd, app.Meta{})
	if err != nil || again.Outcome != app.IntakeDuplicate || h.Metrics.Count("inbox_duplicate") != 1 {
		t.Fatalf("redelivery = %+v %v", again, err)
	}
	if again.Transaction == nil || again.Transaction.Status() != wagering.StatusProcessed {
		t.Fatalf("duplicate must carry the persisted transaction, got %+v", again.Transaction)
	}
	if got := balance(t, h, w.ID()); got != "75.00" {
		t.Fatalf("balance = %s, want one debit (75.00)", got)
	}
}

func TestIntakeRejectsReusedMessageID(t *testing.T) {
	h := apptest.New(t)
	intake, _ := newIntake(t, h)
	w := h.OpenWallet(t, "100.00")
	msgID := "msg-" + uuid.NewString()
	ctx := context.Background()
	if _, err := intake.Handle(ctx, msgID, apptest.Command(t, w, "provider-a", "BET", "10.00", ""), app.Meta{}); err != nil {
		t.Fatal(err)
	}
	other := apptest.Command(t, w, "provider-a", "BET", "20.00", "")
	res, err := intake.Handle(ctx, msgID, other, app.Meta{})
	if err != nil || res.Outcome != app.IntakeMessageReused {
		t.Fatalf("reused = %+v %v", res, err)
	}
	if _, err := h.Transactions.FindByIdempotencyKey(ctx, "provider-a", other.IdempotencyKey); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("a reused messageId must not process its payload: %v", err)
	}
	if got := balance(t, h, w.ID()); got != "90.00" {
		t.Fatalf("balance = %s", got)
	}
}

func TestIntakeAfterHTTPIsReplayAndRecordsInbox(t *testing.T) {
	h := apptest.New(t)
	intake, ws := newIntake(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "30.00", "")
	ctx := context.Background()
	if _, err := ws.Process(ctx, cmd, app.Meta{Channel: app.ChannelHTTP}, nil); err != nil {
		t.Fatal(err)
	}
	msgID := "msg-" + uuid.NewString()
	res, err := intake.Handle(ctx, msgID, cmd, app.Meta{})
	if err != nil || res.Outcome != app.IntakeHandled || !res.Replay {
		t.Fatalf("cross-channel = %+v %v", res, err)
	}
	if _, err := h.Inbox.Get(ctx, app.ConsumerWagerTransactions, msgID); err != nil {
		t.Fatalf("a replayed message must be recorded in the inbox: %v", err)
	}
	if got := balance(t, h, w.ID()); got != "70.00" {
		t.Fatalf("balance = %s, want one debit", got)
	}
}

func TestIntakeOutcomesAreRecorded(t *testing.T) {
	h := apptest.New(t)
	intake, _ := newIntake(t, h)
	w := h.OpenWallet(t, "10.00")
	ctx := context.Background()
	rejected, err := intake.Handle(ctx, "msg-"+uuid.NewString(), apptest.Command(t, w, "provider-a", "BET", "50.00", ""), app.Meta{})
	if err != nil || rejected.Transaction.Status() != wagering.StatusRejected {
		t.Fatalf("rejected = %+v %v", rejected, err)
	}
	pending, err := intake.Handle(ctx, "msg-"+uuid.NewString(), apptest.Command(t, w, "provider-a", "REFUND", "5.00", "missing-"+uuid.NewString()), app.Meta{})
	if err != nil || pending.Transaction.Status() != wagering.StatusPendingReference {
		t.Fatalf("pending = %+v %v", pending, err)
	}
}

func TestIntakeConcurrentDeliveriesApplyOnce(t *testing.T) {
	h := apptest.New(t)
	intake, _ := newIntake(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "40.00", "")
	msgID := "msg-" + uuid.NewString()

	const n = 10
	results := make([]app.IntakeResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = intake.Handle(context.Background(), msgID, cmd, app.Meta{})
		}()
	}
	wg.Wait()
	handled := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("delivery %d: %v", i, errs[i])
		}
		switch results[i].Outcome {
		case app.IntakeHandled:
			if results[i].Replay {
				t.Fatalf("delivery %d replayed instead of being dropped by the inbox", i)
			}
			handled++
		case app.IntakeDuplicate:
		default:
			t.Fatalf("delivery %d outcome %s", i, results[i].Outcome)
		}
	}
	if handled != 1 {
		t.Fatalf("handled = %d, want exactly 1", handled)
	}
	if got := balance(t, h, w.ID()); got != "60.00" {
		t.Fatalf("balance = %s, want one debit", got)
	}
}
