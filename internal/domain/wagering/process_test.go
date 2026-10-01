//go:build !integration && !e2e

package wagering

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

type fixture struct {
	t      *testing.T
	wallet *wallet.Wallet
}

func newFixture(t *testing.T, balance string) *fixture {
	t.Helper()
	w, _, err := wallet.Open(wallet.OpenParams{
		ID: newID(t), PlayerID: newID(t), InitialBalance: brl(t, balance),
		OpeningTransactionID: newID(t), LedgerEntryID: newID(t), Now: testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{t: t, wallet: w}
}

// op builds a PENDING external transaction against the fixture wallet.
func (f *fixture) op(kind Kind, amount, ref string) *WagerTransaction {
	f.t.Helper()
	ext := newID(f.t).String()
	raw := RawCommand{
		ProviderID: "provider-a", ExternalTransactionID: ext, IdempotencyKey: "provider-a:" + ext,
		PlayerID: f.wallet.PlayerID().String(), WalletID: f.wallet.ID().String(),
		RoundID: "round-1", GameID: "game-1", Kind: string(kind), Amount: amount, Currency: "BRL",
		ReferenceExternalTransactionID: ref,
	}
	cmd, err := ParseCommand(raw)
	if err != nil {
		f.t.Fatal(err)
	}
	tx, err := NewExternal(newID(f.t), cmd, testNow)
	if err != nil {
		f.t.Fatal(err)
	}
	return tx
}

func (f *fixture) input(ref *Reference, reversed bool) ProcessInput {
	return ProcessInput{
		Wallet: f.wallet, Reference: ref, ReferenceAlreadyReversed: reversed,
		LedgerEntryID: newID(f.t), RetryPolicy: DefaultRetryPolicy(), Now: testNow.Add(time.Minute),
	}
}

// processed runs tx to PROCESSED and returns it as a reference.
func (f *fixture) processed(kind Kind, amount string, ref *Reference) Reference {
	f.t.Helper()
	refExt := ""
	if ref != nil {
		refExt = "ref"
	}
	tx := f.op(kind, amount, refExt)
	if _, err := tx.Process(f.input(ref, false)); err != nil {
		f.t.Fatal(err)
	}
	if tx.Status() != StatusProcessed {
		f.t.Fatalf("setup %s: status %s code %s", kind, tx.Status(), tx.FailureCode())
	}
	tx.PullEvents()
	return tx.AsReference()
}

func eventTypes(evs []events.Data) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventType()
	}
	return out
}

func assertOutcome(t *testing.T, tx *WagerTransaction, status Status, code FailureCode) {
	t.Helper()
	if tx.Status() != status || tx.FailureCode() != code {
		t.Fatalf("status %s code %q, want %s %q", tx.Status(), tx.FailureCode(), status, code)
	}
}

func TestBet(t *testing.T) {
	f := newFixture(t, "100.00")
	tx := f.op(KindBet, "25.00", "")
	entry, err := tx.Process(f.input(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, tx, StatusProcessed, "")
	if entry == nil || entry.Direction() != wallet.DirectionDebit || f.wallet.Balance().Amount() != "75.00" ||
		f.wallet.Version() != 2 || tx.ResultBalance().Amount() != "75.00" {
		t.Fatalf("entry %+v balance %s version %d", entry, f.wallet.Balance(), f.wallet.Version())
	}
	got := eventTypes(tx.PullEvents())
	if len(got) != 2 || got[0] != events.TypeWagerTransactionProcessed || got[1] != events.TypeWalletBalanceChanged {
		t.Fatalf("events = %v", got)
	}
}

func TestBetInsufficientFunds(t *testing.T) {
	f := newFixture(t, "100.00")
	first, second := f.op(KindBet, "80.00", ""), f.op(KindBet, "80.00", "")
	if _, err := first.Process(f.input(nil, false)); err != nil {
		t.Fatal(err)
	}
	entry, err := second.Process(f.input(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, first, StatusProcessed, "")
	assertOutcome(t, second, StatusRejected, FailureInsufficientFunds)
	if entry != nil || f.wallet.Balance().Amount() != "20.00" || f.wallet.Version() != 2 {
		t.Fatalf("balance %s version %d", f.wallet.Balance(), f.wallet.Version())
	}
	if got := eventTypes(second.PullEvents()); len(got) != 1 || got[0] != events.TypeWagerTransactionRejected {
		t.Fatalf("events = %v", got)
	}
}

func TestLossDoesNotMoveBalance(t *testing.T) {
	f := newFixture(t, "100.00")
	tx := f.op(KindLoss, "0.00", "")
	entry, err := tx.Process(f.input(nil, false))
	if err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, tx, StatusProcessed, "")
	if entry != nil || f.wallet.Version() != 1 || tx.ResultBalance().Amount() != "100.00" {
		t.Fatalf("LOSS moved the wallet: entry %+v version %d", entry, f.wallet.Version())
	}
	if got := eventTypes(tx.PullEvents()); len(got) != 1 || got[0] != events.TypeWagerTransactionProcessed {
		t.Fatalf("events = %v, want only Processed", got)
	}
}

func TestWin(t *testing.T) {
	f := newFixture(t, "100.00")
	plain := f.op(KindWin, "50.00", "")
	if _, err := plain.Process(f.input(nil, false)); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, plain, StatusProcessed, "")
	if f.wallet.Balance().Amount() != "150.00" {
		t.Fatalf("balance %s", f.wallet.Balance())
	}

	bet := f.processed(KindBet, "10.00", nil)
	withRef := f.op(KindWin, "35.00", "bet")
	if _, err := withRef.Process(f.input(&bet, false)); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, withRef, StatusProcessed, "")
	if withRef.ReferenceTransactionID() != bet.ID {
		t.Fatal("WIN must persist the resolved reference")
	}

	win := f.processed(KindWin, "5.00", nil)
	badKind := f.op(KindWin, "5.00", "win")
	if _, err := badKind.Process(f.input(&win, false)); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, badKind, StatusRejected, FailureReferenceKindInvalid)
}

func TestRefund(t *testing.T) {
	tests := []struct {
		name     string
		amount   string
		mutate   func(r *Reference)
		reversed bool
		status   Status
		code     FailureCode
	}{
		{"processed bet", "30.00", nil, false, StatusProcessed, ""},
		{"already reversed", "30.00", nil, true, StatusRejected, FailureReferenceAlreadyReversed},
		{"amount mismatch", "29.99", nil, false, StatusRejected, FailureAmountMismatch},
		{"round mismatch", "30.00", func(r *Reference) { r.RoundID = "other" }, false, StatusRejected, FailureReferenceMismatch},
		{"player mismatch", "30.00", func(r *Reference) { r.PlayerID = uuid.New() }, false, StatusRejected, FailureReferenceMismatch},
		{"wallet mismatch", "30.00", func(r *Reference) { r.WalletID = uuid.New() }, false, StatusRejected, FailureReferenceMismatch},
		{"reference rejected", "30.00", func(r *Reference) { r.Status = StatusRejected }, false, StatusRejected, FailureReferenceNotProcessed},
		{"reference failed", "30.00", func(r *Reference) { r.Status = StatusFailed }, false, StatusRejected, FailureReferenceNotProcessed},
		{"reference is a win", "30.00", func(r *Reference) { r.Kind = KindWin }, false, StatusRejected, FailureReferenceKindInvalid},
		{"reference still pending", "30.00", func(r *Reference) { r.Status = StatusPendingReference }, false, StatusPendingReference, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "100.00")
			bet := f.processed(KindBet, "30.00", nil)
			if tt.mutate != nil {
				tt.mutate(&bet)
			}
			before := f.wallet.Balance()
			tx := f.op(KindRefund, tt.amount, "bet")
			if _, err := tx.Process(f.input(&bet, tt.reversed)); err != nil {
				t.Fatal(err)
			}
			assertOutcome(t, tx, tt.status, tt.code)
			if tt.status == StatusProcessed {
				if f.wallet.Balance().Amount() != "100.00" {
					t.Fatalf("refund balance %s, want 100.00", f.wallet.Balance())
				}
			} else if !f.wallet.Balance().Equal(before) {
				t.Fatal("non-processed refund moved the balance")
			}
		})
	}
}

func TestRollback(t *testing.T) {
	t.Run("of bet credits", func(t *testing.T) {
		f := newFixture(t, "100.00")
		bet := f.processed(KindBet, "40.00", nil)
		tx := f.op(KindRollback, "40.00", "bet")
		entry, err := tx.Process(f.input(&bet, false))
		if err != nil {
			t.Fatal(err)
		}
		assertOutcome(t, tx, StatusProcessed, "")
		if entry.Direction() != wallet.DirectionCredit || f.wallet.Balance().Amount() != "100.00" {
			t.Fatalf("direction %s balance %s", entry.Direction(), f.wallet.Balance())
		}
	})
	t.Run("of win debits", func(t *testing.T) {
		f := newFixture(t, "100.00")
		win := f.processed(KindWin, "50.00", nil)
		tx := f.op(KindRollback, "50.00", "win")
		entry, err := tx.Process(f.input(&win, false))
		if err != nil {
			t.Fatal(err)
		}
		assertOutcome(t, tx, StatusProcessed, "")
		if entry.Direction() != wallet.DirectionDebit || f.wallet.Balance().Amount() != "100.00" {
			t.Fatalf("direction %s balance %s", entry.Direction(), f.wallet.Balance())
		}
	})
	t.Run("of refund debits", func(t *testing.T) {
		f := newFixture(t, "100.00")
		bet := f.processed(KindBet, "30.00", nil)
		refund := f.processed(KindRefund, "30.00", &bet)
		tx := f.op(KindRollback, "30.00", "refund")
		if _, err := tx.Process(f.input(&refund, false)); err != nil {
			t.Fatal(err)
		}
		assertOutcome(t, tx, StatusProcessed, "")
		if f.wallet.Balance().Amount() != "70.00" {
			t.Fatalf("balance %s, want 70.00 (bet stands again)", f.wallet.Balance())
		}
	})
	t.Run("of win without funds", func(t *testing.T) {
		f := newFixture(t, "0.00")
		win := f.processed(KindWin, "50.00", nil)
		f.processed(KindBet, "45.00", nil)
		tx := f.op(KindRollback, "50.00", "win")
		if _, err := tx.Process(f.input(&win, false)); err != nil {
			t.Fatal(err)
		}
		assertOutcome(t, tx, StatusRejected, FailureInsufficientFundsForReversal)
		if f.wallet.Balance().Amount() != "5.00" {
			t.Fatalf("balance %s", f.wallet.Balance())
		}
	})
	t.Run("of loss is invalid", func(t *testing.T) {
		f := newFixture(t, "100.00")
		loss := f.processed(KindLoss, "0.00", nil)
		loss.Money = brl(t, "10.00")
		tx := f.op(KindRollback, "10.00", "loss")
		if _, err := tx.Process(f.input(&loss, false)); err != nil {
			t.Fatal(err)
		}
		assertOutcome(t, tx, StatusRejected, FailureReferenceKindInvalid)
	})
}

func TestWalletChecks(t *testing.T) {
	f := newFixture(t, "100.00")
	missing := f.op(KindBet, "10.00", "")
	in := f.input(nil, false)
	in.Wallet = nil
	if _, err := missing.Process(in); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, missing, StatusRejected, FailureWalletNotFound)

	other, _, _ := wallet.Open(wallet.OpenParams{
		ID: f.wallet.ID(), PlayerID: newID(t), InitialBalance: brl(t, "0.00"), Now: testNow,
	})
	wrongPlayer := f.op(KindBet, "10.00", "")
	in = f.input(nil, false)
	in.Wallet = other
	if _, err := wrongPlayer.Process(in); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, wrongPlayer, StatusRejected, FailureWalletPlayerMismatch)

	usd, _ := money.Parse("100.00", "USD")
	usdWallet, _, _ := wallet.Open(wallet.OpenParams{
		ID: f.wallet.ID(), PlayerID: f.wallet.PlayerID(), InitialBalance: usd,
		OpeningTransactionID: newID(t), LedgerEntryID: newID(t), Now: testNow,
	})
	wrongCurrency := f.op(KindBet, "10.00", "")
	in = f.input(nil, false)
	in.Wallet = usdWallet
	if _, err := wrongCurrency.Process(in); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, wrongCurrency, StatusRejected, FailureCurrencyMismatch)
	if usdWallet.Version() != 1 {
		t.Fatal("currency mismatch must not move the wallet")
	}
}

func TestPendingReferenceLifecycle(t *testing.T) {
	f := newFixture(t, "100.00")
	refund := f.op(KindRefund, "30.00", "bet-not-yet")
	first := f.input(nil, false)
	if _, err := refund.Process(first); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, refund, StatusPendingReference, "")
	if refund.Attempts() != 1 || !refund.NextAttemptAt().Equal(first.Now.Add(2*time.Second)) {
		t.Fatalf("attempts %d next %s", refund.Attempts(), refund.NextAttemptAt())
	}
	evs := refund.PullEvents()
	if got := eventTypes(evs); len(got) != 1 || got[0] != events.TypeWagerTransactionPendingReference {
		t.Fatalf("events = %v", got)
	}
	if p := evs[0].(events.WagerTransactionPendingReference); p.ReferenceExternalTransactionID != "bet-not-yet" || p.Attempts != 1 {
		t.Fatalf("pending event = %+v", p)
	}

	second := f.input(nil, false)
	if _, err := refund.Process(second); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, refund, StatusPendingReference, "")
	if refund.Attempts() != 2 || !refund.NextAttemptAt().Equal(second.Now.Add(4*time.Second)) {
		t.Fatalf("attempts %d next %s", refund.Attempts(), refund.NextAttemptAt())
	}
	if len(refund.PullEvents()) != 0 {
		t.Fatal("a retry must not emit a new pending event")
	}

	bet := f.processed(KindBet, "30.00", nil)
	if _, err := refund.Process(f.input(&bet, false)); err != nil {
		t.Fatal(err)
	}
	assertOutcome(t, refund, StatusProcessed, "")
	if f.wallet.Balance().Amount() != "100.00" || !refund.NextAttemptAt().IsZero() {
		t.Fatalf("balance %s next %s", f.wallet.Balance(), refund.NextAttemptAt())
	}
}

func TestPendingReferenceExhaustion(t *testing.T) {
	policy := RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Minute, MaxAttempts: 3}

	f := newFixture(t, "100.00")
	missing := f.op(KindRollback, "30.00", "never")
	for i := 0; i < 3; i++ {
		in := f.input(nil, false)
		in.RetryPolicy = policy
		if _, err := missing.Process(in); err != nil {
			t.Fatal(err)
		}
	}
	assertOutcome(t, missing, StatusRejected, FailureReferenceNotFound)
	if got := eventTypes(missing.PullEvents()); len(got) != 2 || got[1] != events.TypeWagerTransactionRejected {
		t.Fatalf("events = %v, want PendingReference then Rejected", got)
	}

	stuck := f.op(KindRefund, "30.00", "stuck")
	pendingRef := Reference{ID: newID(t), Kind: KindBet, Status: StatusPendingReference,
		WalletID: f.wallet.ID(), PlayerID: f.wallet.PlayerID(), RoundID: "round-1", Money: brl(t, "30.00")}
	for i := 0; i < 3; i++ {
		in := f.input(&pendingRef, false)
		in.RetryPolicy = policy
		if _, err := stuck.Process(in); err != nil {
			t.Fatal(err)
		}
	}
	assertOutcome(t, stuck, StatusRejected, FailureReferenceNotProcessed)
}

func TestProcessTerminalIsRejected(t *testing.T) {
	f := newFixture(t, "100.00")
	tx := f.op(KindBet, "10.00", "")
	if _, err := tx.Process(f.input(nil, false)); err != nil {
		t.Fatal(err)
	}
	balance, version := f.wallet.Balance(), f.wallet.Version()
	if _, err := tx.Process(f.input(nil, false)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("error = %v, want ErrInvalidTransition", err)
	}
	if !f.wallet.Balance().Equal(balance) || f.wallet.Version() != version {
		t.Fatal("re-processing a terminal transaction moved the wallet")
	}
}

func TestProcessRejectsOpening(t *testing.T) {
	f := newFixture(t, "100.00")
	opening, err := NewOpening(newID(t), f.wallet.ID(), f.wallet.PlayerID(), brl(t, "1.00"), testNow)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := opening.Process(f.input(nil, false)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("error = %v, want ErrInvalidTransition", err)
	}
}
