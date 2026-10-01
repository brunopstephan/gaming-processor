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

var testNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func newID(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func brl(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestNewExternal(t *testing.T) {
	cmd, err := ParseCommand(validRaw())
	if err != nil {
		t.Fatal(err)
	}
	id := newID(t)
	tx, err := NewExternal(id, cmd, testNow)
	if err != nil {
		t.Fatal(err)
	}
	s := tx.State()
	if s.ID != id || s.Origin != OriginExternal || s.Status != StatusPending || s.Kind != KindBet ||
		s.PayloadHash != cmd.PayloadHash() || s.IdempotencyKey != cmd.IdempotencyKey ||
		s.ProviderID != "provider-a" || !s.CreatedAt.Equal(testNow) || s.Attempts != 0 {
		t.Fatalf("state = %+v", s)
	}
	if len(tx.PullEvents()) != 0 {
		t.Fatal("creation must not record events")
	}
	if _, err := NewExternal(uuid.Nil, cmd, testNow); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("nil id error = %v", err)
	}
	if _, err := NewExternal(id, Command{}, testNow); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("empty command error = %v", err)
	}
}

func TestOpeningLifecycle(t *testing.T) {
	walletID, playerID, txID, entryID := newID(t), newID(t), newID(t), newID(t)
	initial := brl(t, "1000.00")
	w, entry, err := wallet.Open(wallet.OpenParams{
		ID: walletID, PlayerID: playerID, InitialBalance: initial,
		OpeningTransactionID: txID, LedgerEntryID: entryID, Now: testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := NewOpening(txID, walletID, playerID, initial, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CompleteOpening(w, *entry, testNow); err != nil {
		t.Fatal(err)
	}
	s := tx.State()
	if s.Status != StatusProcessed || s.Origin != OriginInternal || s.Kind != KindOpening ||
		!s.ResultBalance.Equal(initial) || s.ProviderID != "" || s.ExternalTransactionID != "" ||
		s.IdempotencyKey != "" || s.PayloadHash != "" || s.RoundID != "" || s.GameID != "" {
		t.Fatalf("opening state = %+v", s)
	}
	evs := tx.PullEvents()
	if len(evs) != 2 {
		t.Fatalf("events = %d, want Processed + BalanceChanged", len(evs))
	}
	processed, ok := evs[0].(events.WagerTransactionProcessed)
	if !ok || processed.Origin != "INTERNAL" || processed.Kind != "OPENING" || processed.ProviderID != "" ||
		!processed.BalanceAfter.Equal(initial) {
		t.Fatalf("processed event = %+v", evs[0])
	}
	changed, ok := evs[1].(events.WalletBalanceChanged)
	if !ok || changed.WalletVersion != 1 || changed.Direction != "CREDIT" || !changed.BalanceBefore.IsZero() ||
		!changed.BalanceAfter.Equal(initial) || changed.TransactionID != txID {
		t.Fatalf("balance changed event = %+v", evs[1])
	}
	if len(tx.PullEvents()) != 0 {
		t.Fatal("PullEvents must clear recorded events")
	}
}

func TestNewOpeningInvalid(t *testing.T) {
	if _, err := NewOpening(newID(t), newID(t), newID(t), brl(t, "0.00"), testNow); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("zero opening error = %v, want ErrInvalidTransaction (zero balance creates no OPENING)", err)
	}
	if _, err := NewOpening(newID(t), uuid.Nil, newID(t), brl(t, "1.00"), testNow); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("nil wallet error = %v", err)
	}
}

func TestMarkFailed(t *testing.T) {
	cmd, _ := ParseCommand(validRaw())
	tx, _ := NewExternal(newID(t), cmd, testNow)
	if err := tx.MarkFailed(testNow); err != nil {
		t.Fatal(err)
	}
	if tx.Status() != StatusFailed || tx.FailureCode() != FailureInfrastructure {
		t.Fatalf("status %s code %s", tx.Status(), tx.FailureCode())
	}
	if err := tx.MarkFailed(testNow); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("second MarkFailed error = %v, want ErrInvalidTransition (terminal)", err)
	}
	if len(tx.PullEvents()) != 0 {
		t.Fatal("FAILED records no integration event")
	}
}

func TestRehydrateRoundTripAndNoEvents(t *testing.T) {
	cmd, _ := ParseCommand(validRaw())
	tx, _ := NewExternal(newID(t), cmd, testNow)
	if err := tx.MarkFailed(testNow); err != nil {
		t.Fatal(err)
	}
	back, err := Rehydrate(tx.State())
	if err != nil {
		t.Fatal(err)
	}
	if back.State() != tx.State() {
		t.Fatalf("round trip mismatch\n got %+v\nwant %+v", back.State(), tx.State())
	}
	if len(back.PullEvents()) != 0 {
		t.Fatal("rehydration must not record events")
	}
	if err := back.MarkFailed(testNow); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("rehydrated terminal transaction must reject transitions")
	}
}

func TestRehydrateInvalid(t *testing.T) {
	cmd, _ := ParseCommand(validRaw())
	tx, _ := NewExternal(newID(t), cmd, testNow)
	valid := tx.State()

	// Create a valid OPENING state for internal-specific tests
	walletID, playerID, txID, entryID := newID(t), newID(t), newID(t), newID(t)
	initial := brl(t, "1000.00")
	w, entry, _ := wallet.Open(wallet.OpenParams{
		ID: walletID, PlayerID: playerID, InitialBalance: initial,
		OpeningTransactionID: txID, LedgerEntryID: entryID, Now: testNow,
	})
	openingTx, _ := NewOpening(txID, walletID, playerID, initial, testNow)
	openingTx.CompleteOpening(w, *entry, testNow)
	validOpening := openingTx.State()

	tests := []struct {
		name   string
		state  State
		mutate func(s *State)
	}{
		{"nil id", valid, func(s *State) { s.ID = uuid.Nil }},
		{"bad status", valid, func(s *State) { s.Status = "DONE" }},
		{"bad kind", valid, func(s *State) { s.Kind = "JACKPOT" }},
		{"external opening", valid, func(s *State) { s.Kind = KindOpening }},
		{"internal bet", valid, func(s *State) { s.Origin = OriginInternal }},
		{"missing provider", valid, func(s *State) { s.ProviderID = "" }},
		{"missing hash", valid, func(s *State) { s.PayloadHash = "" }},
		{"invalid money", valid, func(s *State) { s.Money = money.Money{} }},
		{"rejected without code", valid, func(s *State) { s.Status = StatusRejected }},
		{"processed without balance", valid, func(s *State) { s.Status = StatusProcessed }},
		{"pending reference without next attempt", valid, func(s *State) { s.Status = StatusPendingReference }},
		{"zero opening amount", validOpening, func(s *State) { s.Money = brl(t, "0.00") }},
		{"failed with business code", valid, func(s *State) {
			s.Status = StatusFailed
			s.FailureCode = FailureInsufficientFunds
			s.ProcessedAt = testNow
		}},
		{"rejected with infrastructure code", valid, func(s *State) {
			s.Status = StatusRejected
			s.FailureCode = FailureInfrastructure
			s.ProcessedAt = testNow
		}},
		{"pending with failure code", valid, func(s *State) { s.FailureCode = FailureInfrastructure }},
		{"pending reference with failure code", valid, func(s *State) {
			s.Status = StatusPendingReference
			s.NextAttemptAt = testNow.Add(1 * time.Hour)
			s.FailureCode = FailureInfrastructure
		}},
		{"processed with failure code", valid, func(s *State) {
			s.Status = StatusProcessed
			s.FailureCode = FailureInsufficientFunds
		}},
		{"failed without processedAt", valid, func(s *State) {
			s.Status = StatusFailed
			s.FailureCode = FailureInfrastructure
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := tt.state
			tt.mutate(&s)
			if _, err := Rehydrate(s); !errors.Is(err, ErrInvalidTransaction) {
				t.Fatalf("error = %v, want ErrInvalidTransaction", err)
			}
		})
	}
}

func TestRehydrateOpeningRoundTrip(t *testing.T) {
	walletID, playerID, txID, entryID := newID(t), newID(t), newID(t), newID(t)
	initial := brl(t, "1000.00")
	w, entry, err := wallet.Open(wallet.OpenParams{
		ID: walletID, PlayerID: playerID, InitialBalance: initial,
		OpeningTransactionID: txID, LedgerEntryID: entryID, Now: testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := NewOpening(txID, walletID, playerID, initial, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.CompleteOpening(w, *entry, testNow); err != nil {
		t.Fatal(err)
	}
	originalState := tx.State()

	// Rehydrate the PROCESSED OPENING
	back, err := Rehydrate(originalState)
	if err != nil {
		t.Fatal(err)
	}

	// Verify round-trip fidelity
	if back.State() != originalState {
		t.Fatalf("opening round trip mismatch\n got %+v\nwant %+v", back.State(), originalState)
	}

	// Verify no events on rehydration
	if len(back.PullEvents()) != 0 {
		t.Fatal("rehydrated opening must not record events")
	}

	// Verify terminal state
	if err := back.MarkFailed(testNow); !errors.Is(err, ErrInvalidTransition) {
		t.Fatal("rehydrated processed opening must reject transitions")
	}
}
