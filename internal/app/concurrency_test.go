//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

type outcome struct {
	res app.TransactionResult
	err error
}

func processConcurrently(svc *app.WageringService, cmds []wagering.Command) []outcome {
	out := make([]outcome, len(cmds))
	var wg sync.WaitGroup
	for i, cmd := range cmds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.Process(context.Background(), cmd, app.Meta{}, nil)
			out[i] = outcome{res: res, err: err}
		}()
	}
	wg.Wait()
	return out
}

func assertLedgerMatchesBalance(t *testing.T, h *apptest.Harness, walletID uuid.UUID) {
	t.Helper()
	w, err := h.Wallets.Get(context.Background(), walletID)
	if err != nil {
		t.Fatal(err)
	}
	net, _, err := h.Ledger.Totals(context.Background(), walletID)
	if err != nil || net != w.Balance().MinorUnits() {
		t.Fatalf("ledger net %d, stored balance %d (err %v)", net, w.Balance().MinorUnits(), err)
	}
}

func TestSameBetFiftyTimesInParallel(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
	cmds := make([]wagering.Command, 50)
	for i := range cmds {
		cmds[i] = cmd
	}

	originals, replays := 0, 0
	var id uuid.UUID
	for _, o := range processConcurrently(svc, cmds) {
		if o.err != nil {
			t.Fatalf("unexpected error: %v", o.err)
		}
		if o.res.Replay {
			replays++
		} else {
			originals++
		}
		if id == uuid.Nil {
			id = o.res.Transaction.ID()
		} else if o.res.Transaction.ID() != id {
			t.Fatal("every answer must be the same transaction")
		}
	}
	if originals != 1 || replays != 49 {
		t.Fatalf("originals %d replays %d, want 1 and 49", originals, replays)
	}
	if balance(t, h, w.ID()) != "90.00" || ledgerLen(t, h, w.ID()) != 2 {
		t.Fatalf("balance %s ledger %d, want one debit", balance(t, h, w.ID()), ledgerLen(t, h, w.ID()))
	}
	assertLedgerMatchesBalance(t, h, w.ID())
}

func TestTwoBetsOfEightyOnAHundred(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	cmds := []wagering.Command{
		apptest.Command(t, w, "provider-a", "BET", "80.00", ""),
		apptest.Command(t, w, "provider-a", "BET", "80.00", ""),
	}

	statuses := map[wagering.Status]int{}
	for _, o := range processConcurrently(svc, cmds) {
		if o.err != nil {
			t.Fatal(o.err)
		}
		statuses[o.res.Transaction.Status()]++
		if o.res.Transaction.Status() == wagering.StatusRejected && o.res.Transaction.FailureCode() != wagering.FailureInsufficientFunds {
			t.Fatalf("rejection code %s", o.res.Transaction.FailureCode())
		}
	}
	if statuses[wagering.StatusProcessed] != 1 || statuses[wagering.StatusRejected] != 1 {
		t.Fatalf("statuses %v, want one PROCESSED and one REJECTED", statuses)
	}
	if balance(t, h, w.ID()) != "20.00" || ledgerLen(t, h, w.ID()) != 2 {
		t.Fatalf("balance %s ledger %d", balance(t, h, w.ID()), ledgerLen(t, h, w.ID()))
	}

	for _, o := range processConcurrently(svc, cmds) {
		if o.err != nil || !o.res.Replay {
			t.Fatalf("resend must replay: %+v %v", o.res, o.err)
		}
	}
	if balance(t, h, w.ID()) != "20.00" || ledgerLen(t, h, w.ID()) != 2 {
		t.Fatal("resends changed the outcome")
	}
	assertLedgerMatchesBalance(t, h, w.ID())
}

func TestDistinctWalletsProceedWhileOneIsLocked(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	locked, free := h.OpenWallet(t, "100.00"), h.OpenWallet(t, "100.00")

	holding, release := make(chan struct{}), make(chan struct{})
	held := make(chan error, 1)
	go func() {
		held <- h.Tx.WithinTx(context.Background(), func(ctx context.Context) error {
			if _, err := h.Wallets.GetForUpdate(ctx, locked.ID()); err != nil {
				return err
			}
			close(holding)
			<-release
			return nil
		})
	}()
	select {
	case <-holding:
	case err := <-held:
		t.Fatalf("holder failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("holder never locked the wallet")
	}

	start := time.Now()
	res := process(t, svc, apptest.Command(t, free, "provider-a", "BET", "5.00", ""))
	if res.Transaction.Status() != wagering.StatusProcessed || time.Since(start) > 2*time.Second {
		t.Fatalf("a different wallet must not wait: %s after %s", res.Transaction.Status(), time.Since(start))
	}

	lockedCmd := apptest.Command(t, locked, "provider-a", "BET", "5.00", "")
	blocked := make(chan outcome, 1)
	go func() {
		r, err := svc.Process(context.Background(), lockedCmd, app.Meta{}, nil)
		blocked <- outcome{res: r, err: err}
	}()
	select {
	case o := <-blocked:
		t.Fatalf("the locked wallet must wait, got %+v", o)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case o := <-blocked:
		if o.err != nil || o.res.Transaction.Status() != wagering.StatusProcessed {
			t.Fatalf("after release: %+v %v", o.res, o.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the locked wallet never proceeded")
	}
	if err := <-held; err != nil {
		t.Fatal(err)
	}
}

func TestManyWalletsInParallel(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	var cmds []wagering.Command
	var ids []uuid.UUID
	for range 10 {
		w := h.OpenWallet(t, "100.00")
		ids = append(ids, w.ID())
		for range 5 {
			cmds = append(cmds, apptest.Command(t, w, "provider-a", "BET", "1.00", ""))
		}
	}
	for _, o := range processConcurrently(svc, cmds) {
		if o.err != nil || o.res.Transaction.Status() != wagering.StatusProcessed {
			t.Fatalf("%+v %v", o.res, o.err)
		}
	}
	for _, id := range ids {
		if balance(t, h, id) != "95.00" || ledgerLen(t, h, id) != 6 {
			t.Fatalf("wallet %s balance %s", id, balance(t, h, id))
		}
		assertLedgerMatchesBalance(t, h, id)
	}
}

func TestSameExternalIDDifferentKeysInParallel(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	base := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
	cmds := make([]wagering.Command, 10)
	for i := range cmds {
		cmds[i] = base
		cmds[i].IdempotencyKey = fmt.Sprintf("provider-a:%s:k-%d", base.ExternalTransactionID, i)
	}

	winners := 0
	for _, o := range processConcurrently(svc, cmds) {
		switch {
		case o.err == nil:
			winners++
			if o.res.Replay || o.res.Transaction.Status() != wagering.StatusProcessed {
				t.Fatalf("winner must be an original PROCESSED, got %+v", o.res)
			}
		case !errors.Is(o.err, app.ErrExternalTransactionConflict):
			t.Fatalf("unexpected error: %v", o.err)
		}
	}
	if winners != 1 {
		t.Fatalf("winners %d, want 1", winners)
	}
	if balance(t, h, w.ID()) != "90.00" || ledgerLen(t, h, w.ID()) != 2 {
		t.Fatalf("balance %s ledger %d, want one debit", balance(t, h, w.ID()), ledgerLen(t, h, w.ID()))
	}
	assertLedgerMatchesBalance(t, h, w.ID())
}
