//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

// faultyTransactions fails the next Update with err, once.
type faultyTransactions struct {
	app.TransactionRepository
	mu  sync.Mutex
	err error
}

func (f *faultyTransactions) failNextUpdate(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }

func (f *faultyTransactions) Update(ctx context.Context, t *wagering.WagerTransaction) error {
	f.mu.Lock()
	err := f.err
	f.err = nil
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return f.TransactionRepository.Update(ctx, t)
}

// faultyWallets fails the next Update with err, once.
type faultyWallets struct {
	app.WalletRepository
	mu  sync.Mutex
	err error
}

func (f *faultyWallets) Update(ctx context.Context, w *wallet.Wallet, expected int64) error {
	f.mu.Lock()
	err := f.err
	f.err = nil
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return f.WalletRepository.Update(ctx, w, expected)
}

func faultyService(t *testing.T, h *apptest.Harness) (*app.WageringService, *faultyTransactions, *faultyWallets) {
	t.Helper()
	txs := &faultyTransactions{TransactionRepository: h.Transactions}
	wallets := &faultyWallets{WalletRepository: h.Wallets}
	d := h.Deps
	d.Transactions, d.Wallets = txs, wallets
	svc, err := app.NewWageringService(d, wagering.DefaultRetryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return svc, txs, wallets
}

func TestPermanentFailureIsRecordedAsFailed(t *testing.T) {
	h := apptest.New(t)
	svc, txs, _ := faultyService(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "25.00", "")

	txs.failNextUpdate(errors.New("disk on fire"))
	res, err := svc.Process(context.Background(), cmd, app.Meta{}, nil)
	if err != nil {
		t.Fatalf("a permanent failure is a recorded outcome, not an error: %v", err)
	}
	assertStatus(t, wagering.StatusFailed, wagering.FailureInfrastructure, res.Transaction)
	stored, err := h.Transactions.Get(context.Background(), res.Transaction.ID())
	if err != nil || stored.Status() != wagering.StatusFailed {
		t.Fatalf("FAILED must be persisted for audit: %+v %v", stored, err)
	}
	if balance(t, h, w.ID()) != "100.00" || ledgerLen(t, h, w.ID()) != 1 || len(h.OutboxRows(t, w.ID())) != 2 {
		t.Fatal("the failed attempt must leave no balance, ledger or event effects")
	}
	replay, err := svc.Process(context.Background(), cmd, app.Meta{}, nil)
	if err != nil || !replay.Replay || replay.Transaction.Status() != wagering.StatusFailed {
		t.Fatalf("replay of a FAILED operation = %+v, %v", replay, err)
	}
	if h.Metrics.Count("completed:BET:FAILED:http") != 1 {
		t.Fatal("FAILED outcome not counted")
	}
}

func TestTransientFailuresLeaveNoTrace(t *testing.T) {
	h := apptest.New(t)
	svc, txs, wallets := faultyService(t, h)
	w := h.OpenWallet(t, "100.00")
	ctx := context.Background()

	cases := []struct {
		name   string
		inject func()
		metric string
	}{
		{"transient", func() { txs.failNextUpdate(fmt.Errorf("db: %w", app.ErrTransient)) }, "conflict:transient"},
		{"lock timeout", func() { txs.failNextUpdate(fmt.Errorf("db: %w", app.ErrLockTimeout)) }, "conflict:lock_timeout"},
		{"version", func() { wallets.mu.Lock(); wallets.err = app.ErrVersionConflict; wallets.mu.Unlock() }, "conflict:version"},
		{"unique", func() { txs.failNextUpdate(fmt.Errorf("db: %w", app.ErrConflict)) }, "conflict:unique"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := apptest.Command(t, w, "provider-a", "BET", "1.00", "")
			tc.inject()
			_, err := svc.Process(ctx, cmd, app.Meta{}, nil)
			if !errors.Is(err, app.ErrTransient) {
				t.Fatalf("error = %v, want ErrTransient", err)
			}
			if _, err := h.Transactions.FindByIdempotencyKey(ctx, "provider-a", cmd.IdempotencyKey); !errors.Is(err, app.ErrNotFound) {
				t.Fatalf("a transient failure must leave no row (no FAILED): %v", err)
			}
			if h.Metrics.Count(tc.metric) == 0 {
				t.Fatalf("metric %s not recorded", tc.metric)
			}
			res, err := svc.Process(ctx, cmd, app.Meta{}, nil)
			if err != nil || res.Replay || res.Transaction.Status() != wagering.StatusProcessed {
				t.Fatalf("retry after a transient failure = %+v, %v", res, err)
			}
		})
	}
	if balance(t, h, w.ID()) != "96.00" {
		t.Fatalf("balance %s, want 96.00 (each bet applied once)", balance(t, h, w.ID()))
	}
}

func TestCanceledRequestLeavesNoTrace(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "10.00", "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Process(ctx, cmd, app.Meta{}, nil); !errors.Is(err, app.ErrTransient) {
		t.Fatalf("canceled request error = %v, want ErrTransient", err)
	}
	if _, err := h.Transactions.FindByIdempotencyKey(context.Background(), "provider-a", cmd.IdempotencyKey); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("a canceled request must not be recorded as FAILED: %v", err)
	}
	if balance(t, h, w.ID()) != "100.00" {
		t.Fatal("a canceled request must not move the wallet")
	}
}

func TestAlongsideRunsInTheFailedTransaction(t *testing.T) {
	h := apptest.New(t)
	svc, txs, _ := faultyService(t, h)
	w := h.OpenWallet(t, "100.00")
	calls := 0
	txs.failNextUpdate(errors.New("permanent"))
	res, err := svc.Process(context.Background(), apptest.Command(t, w, "provider-a", "BET", "1.00", ""), app.Meta{},
		func(context.Context) error { calls++; return nil })
	if err != nil || res.Transaction.Status() != wagering.StatusFailed || calls != 1 {
		t.Fatalf("status %v calls %d err %v; the hook must run once, with the FAILED record", res.Transaction, calls, err)
	}
}
