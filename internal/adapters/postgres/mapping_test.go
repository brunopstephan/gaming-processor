//go:build !integration && !e2e

package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

func unitMoney(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func command(t *testing.T, w *wallet.Wallet, kind, amount, ref string) wagering.Command {
	t.Helper()
	ext := uuid.NewString()
	cmd, err := wagering.ParseCommand(wagering.RawCommand{
		ProviderID: "provider-a", ExternalTransactionID: ext, IdempotencyKey: "provider-a:" + ext,
		PlayerID: w.PlayerID().String(), WalletID: w.ID().String(), RoundID: "round-1", GameID: "game-1",
		Kind: kind, Amount: amount, Currency: "BRL", ReferenceExternalTransactionID: ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func roundTrip(t *testing.T, tx *wagering.WagerTransaction) {
	t.Helper()
	back, err := transactionFromModel(transactionToModel(tx))
	if err != nil {
		t.Fatal(err)
	}
	if back.State() != tx.State() {
		t.Fatalf("mapping round trip\n got %+v\nwant %+v", back.State(), tx.State())
	}
}

func TestTransactionMappingRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	w, entry, err := wallet.Open(wallet.OpenParams{
		ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: unitMoney(t, "100.00"),
		OpeningTransactionID: uuid.New(), LedgerEntryID: uuid.New(), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	opening, _ := wagering.NewOpening(entry.TransactionID(), w.ID(), w.PlayerID(), unitMoney(t, "100.00"), now)
	if err := opening.CompleteOpening(w, *entry, now); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, opening)

	bet, _ := wagering.NewExternal(uuid.New(), command(t, w, "BET", "30.00", ""), now)
	if _, err := bet.Process(wagering.ProcessInput{Wallet: w, LedgerEntryID: uuid.New(), RetryPolicy: wagering.DefaultRetryPolicy(), Now: now}); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, bet)

	pending, _ := wagering.NewExternal(uuid.New(), command(t, w, "REFUND", "30.00", "not-yet"), now)
	if _, err := pending.Process(wagering.ProcessInput{Wallet: w, LedgerEntryID: uuid.New(), RetryPolicy: wagering.DefaultRetryPolicy(), Now: now}); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, pending)

	ref := bet.AsReference()
	refund, _ := wagering.NewExternal(uuid.New(), command(t, w, "REFUND", "30.00", bet.ExternalTransactionID()), now)
	if _, err := refund.Process(wagering.ProcessInput{Wallet: w, Reference: &ref, LedgerEntryID: uuid.New(), RetryPolicy: wagering.DefaultRetryPolicy(), Now: now}); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, refund)

	rejected, _ := wagering.NewExternal(uuid.New(), command(t, w, "BET", "999.00", ""), now)
	if _, err := rejected.Process(wagering.ProcessInput{Wallet: w, LedgerEntryID: uuid.New(), RetryPolicy: wagering.DefaultRetryPolicy(), Now: now}); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, rejected)
}
