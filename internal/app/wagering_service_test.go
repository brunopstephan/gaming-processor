//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

func newWagering(t *testing.T, h *apptest.Harness) *app.WageringService {
	t.Helper()
	svc, err := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func process(t *testing.T, svc *app.WageringService, cmd wagering.Command) app.TransactionResult {
	t.Helper()
	res, err := svc.Process(context.Background(), cmd, app.Meta{CorrelationID: "corr-" + cmd.ExternalTransactionID}, nil)
	if err != nil {
		t.Fatalf("Process(%s %s): %v", cmd.Kind, cmd.Money, err)
	}
	return res
}

func balance(t *testing.T, h *apptest.Harness, id uuid.UUID) string {
	t.Helper()
	w, err := h.Wallets.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return w.Balance().Amount()
}

func ledgerLen(t *testing.T, h *apptest.Harness, id uuid.UUID) int {
	t.Helper()
	entries, err := h.Ledger.List(context.Background(), id, uuid.Nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestProcessBet(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "25.00", "")

	res, err := svc.Process(context.Background(), cmd,
		app.Meta{CorrelationID: "corr-1", CausationID: "msg-1", Channel: app.ChannelSQS}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tx := res.Transaction
	if res.Replay || tx.Status() != wagering.StatusProcessed || tx.ResultBalance().Amount() != "75.00" {
		t.Fatalf("result replay %v status %s balance %s", res.Replay, tx.Status(), tx.ResultBalance())
	}
	stored, err := h.Wallets.Get(context.Background(), w.ID())
	if err != nil || stored.Balance().Amount() != "75.00" || stored.Version() != 2 {
		t.Fatalf("wallet %+v err %v", stored, err)
	}
	if ledgerLen(t, h, w.ID()) != 2 {
		t.Fatal("want opening credit + bet debit")
	}
	persisted, err := h.Transactions.Get(context.Background(), tx.ID())
	if err != nil || persisted.State() != tx.State() {
		t.Fatalf("persisted state differs: %v", err)
	}
	rows := h.OutboxRows(t, w.ID())
	if got := eventTypes(rows[2:]); !slices.Equal(got, []string{"WagerTransactionProcessed", "WalletBalanceChanged"}) {
		t.Fatalf("bet events = %v", got)
	}
	if rows[2].CorrelationID != "corr-1" || rows[2].CausationID != "msg-1" {
		t.Fatalf("event metadata %+v", rows[2])
	}
	if h.Metrics.Count("completed:BET:PROCESSED:sqs") != 1 || h.Metrics.Count("duration:sqs") != 1 {
		t.Fatal("metrics not recorded")
	}
}

func TestProcessLossDoesNotMoveTheWallet(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	res := process(t, svc, apptest.Command(t, w, "provider-a", "LOSS", "0.00", ""))
	stored, _ := h.Wallets.Get(context.Background(), w.ID())
	if res.Transaction.Status() != wagering.StatusProcessed || stored.Version() != 1 || ledgerLen(t, h, w.ID()) != 1 {
		t.Fatalf("LOSS moved the wallet: status %s version %d", res.Transaction.Status(), stored.Version())
	}
	if got := eventTypes(h.OutboxRows(t, w.ID())[2:]); !slices.Equal(got, []string{"WagerTransactionProcessed"}) {
		t.Fatalf("loss events = %v", got)
	}
}

func TestProcessRejections(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")

	res := process(t, svc, apptest.Command(t, w, "provider-a", "BET", "150.00", ""))
	if res.Transaction.Status() != wagering.StatusRejected || res.Transaction.FailureCode() != wagering.FailureInsufficientFunds {
		t.Fatalf("status %s code %s", res.Transaction.Status(), res.Transaction.FailureCode())
	}
	if balance(t, h, w.ID()) != "100.00" || ledgerLen(t, h, w.ID()) != 1 {
		t.Fatal("rejection moved the wallet")
	}
	if got := eventTypes(h.OutboxRows(t, w.ID())[2:]); !slices.Equal(got, []string{"WagerTransactionRejected"}) {
		t.Fatalf("rejection events = %v", got)
	}

	brl, _ := money.Parse("0.00", "BRL")
	ghost, _, err := wallet.Open(wallet.OpenParams{ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl, Now: apptest.Clock()})
	if err != nil {
		t.Fatal(err)
	}
	missing := process(t, svc, apptest.Command(t, ghost, "provider-a", "BET", "1.00", ""))
	stored, err := h.Transactions.Get(context.Background(), missing.Transaction.ID())
	if err != nil || stored.Status() != wagering.StatusRejected || stored.FailureCode() != wagering.FailureWalletNotFound {
		t.Fatalf("wallet not found: %+v err %v", stored, err)
	}
}

func TestReplayReturnsTheOriginalResult(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	first := apptest.Command(t, w, "provider-a", "BET", "25.00", "")
	original := process(t, svc, first)
	process(t, svc, apptest.Command(t, w, "provider-a", "BET", "10.00", ""))

	replay := process(t, svc, first)
	if !replay.Replay || replay.Transaction.ID() != original.Transaction.ID() ||
		replay.Transaction.ResultBalance().Amount() != "75.00" {
		t.Fatalf("replay = %+v, want original transaction with balance 75.00", replay)
	}
	if balance(t, h, w.ID()) != "65.00" || ledgerLen(t, h, w.ID()) != 3 {
		t.Fatal("replay must not move the wallet")
	}
	if h.Metrics.Count("replay:http") != 1 {
		t.Fatal("replay metric not recorded")
	}
}

func TestIdempotencyConflicts(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "25.00", "")
	process(t, svc, cmd)

	changed := cmd
	changed.Money, _ = money.Parse("30.00", "BRL")
	if _, err := svc.Process(context.Background(), changed, app.Meta{}, nil); !errors.Is(err, app.ErrIdempotencyKeyConflict) {
		t.Fatalf("same key, other payload: err %v", err)
	}
	otherKey := cmd
	otherKey.IdempotencyKey = "provider-a:another-key"
	if _, err := svc.Process(context.Background(), otherKey, app.Meta{}, nil); !errors.Is(err, app.ErrExternalTransactionConflict) {
		t.Fatalf("same external id, other key: err %v", err)
	}
	otherProvider := cmd
	otherProvider.ProviderID = "provider-b"
	if res := process(t, svc, otherProvider); res.Replay || res.Transaction.ProviderID() != "provider-b" {
		t.Fatalf("keys are scoped by provider: %+v", res)
	}
	if balance(t, h, w.ID()) != "50.00" {
		t.Fatalf("balance %s, want 50.00 (two distinct bets)", balance(t, h, w.ID()))
	}
}

func TestAlongsideSharesTheTransaction(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	ctx := context.Background()

	zero, err := money.Parse("0.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	appendEvent := func(ctx context.Context, id uuid.UUID) error {
		env, err := events.NewEnvelope(id, events.WalletBalanceChanged{
			WalletID: w.ID(), TransactionID: uuid.New(), Direction: "DEBIT",
			Money: zero, BalanceBefore: zero, BalanceAfter: zero, WalletVersion: 1,
		}, "corr-hook", "", time.Now())
		if err != nil {
			return err
		}
		return h.Outbox.Append(ctx, env)
	}
	eventExists := func(id uuid.UUID) bool {
		var n int64
		if err := h.DB.Raw(`SELECT count(*) FROM outbox_events WHERE id = ?`, id).Scan(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n == 1
	}

	calls := 0
	okEvent := uuid.Must(uuid.NewV7())
	ok := func(ctx context.Context) error { calls++; return appendEvent(ctx, okEvent) }
	cmd := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
	if _, err := svc.Process(ctx, cmd, app.Meta{}, ok); err != nil || calls != 1 {
		t.Fatalf("hook on first processing: calls %d err %v", calls, err)
	}
	if !eventExists(okEvent) {
		t.Fatal("the hook's outbox write must commit with the transaction")
	}
	replayEvent := uuid.Must(uuid.NewV7())
	replayHook := func(ctx context.Context) error { calls++; return appendEvent(ctx, replayEvent) }
	if res, err := svc.Process(ctx, cmd, app.Meta{}, replayHook); err != nil || !res.Replay || calls != 2 {
		t.Fatalf("hook on replay: calls %d err %v", calls, err)
	}
	if !eventExists(replayEvent) {
		t.Fatal("the hook's outbox write must commit on replay")
	}

	boom := errors.New("inbox write failed")
	failing := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
	failEvent := uuid.Must(uuid.NewV7())
	_, err = svc.Process(ctx, failing, app.Meta{}, func(ctx context.Context) error {
		if err := appendEvent(ctx, failEvent); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("hook error must be returned: %v", err)
	}
	if eventExists(failEvent) {
		t.Fatal("hook failure must roll back the hook's outbox write")
	}
	if _, err := h.Transactions.FindByIdempotencyKey(ctx, "provider-a", failing.IdempotencyKey); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("hook failure must roll back the transaction row (and must not record FAILED): %v", err)
	}
	if balance(t, h, w.ID()) != "90.00" {
		t.Fatal("hook failure must roll back the debit")
	}
}

func TestNewWageringServiceValidatesPolicy(t *testing.T) {
	h := apptest.New(t)
	if _, err := app.NewWageringService(h.Deps, wagering.RetryPolicy{}); err == nil {
		t.Fatal("invalid retry policy must be rejected at construction")
	}
}

// lookupRacy hides the row from the FIRST FindByIdempotencyKey, as if the
// winner committed between the two lookups of Process.
type lookupRacy struct {
	app.TransactionRepository
	once sync.Once
}

func (l *lookupRacy) FindByIdempotencyKey(ctx context.Context, provider, key string) (*wagering.WagerTransaction, error) {
	hidden := false
	l.once.Do(func() { hidden = true })
	if hidden {
		return nil, fmt.Errorf("%w: idempotency key %q", app.ErrNotFound, key)
	}
	return l.TransactionRepository.FindByIdempotencyKey(ctx, provider, key)
}

func TestLookupRaceBetweenKeyAndExternalIDIsAReplay(t *testing.T) {
	h := apptest.New(t)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
	first := process(t, newWagering(t, h), cmd)

	racyService := func() *app.WageringService {
		d := h.Deps
		d.Transactions = &lookupRacy{TransactionRepository: h.Transactions}
		svc, err := app.NewWageringService(d, wagering.DefaultRetryPolicy())
		if err != nil {
			t.Fatal(err)
		}
		return svc
	}

	res, err := racyService().Process(context.Background(), cmd, app.Meta{}, nil)
	if err != nil || !res.Replay || res.Transaction.ID() != first.Transaction.ID() {
		t.Fatalf("same payload through the external-id path = %+v, %v", res, err)
	}

	eleven, err := money.Parse("11.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	other := cmd
	other.Money = eleven
	if _, err := racyService().Process(context.Background(), other, app.Meta{}, nil); !errors.Is(err, app.ErrIdempotencyKeyConflict) {
		t.Fatalf("different payload through the external-id path = %v, want ErrIdempotencyKeyConflict", err)
	}
	if balance(t, h, w.ID()) != "90.00" {
		t.Fatal("the bet must be applied once")
	}
}
