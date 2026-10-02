//go:build !unit && !e2e

package postgres

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

// seedLedger persists a wallet opened with 100.00 and four 1.00 bets: five
// ledger entries, net 96.00. It returns the wallet id and the entry ids.
func seedLedger(t *testing.T) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	db := newTestDB(t)
	ctx := context.Background()
	wallets, txs, ledger := NewWalletRepository(db), NewTransactionRepository(db), NewLedgerRepository(db)

	openingID, entryID := mustV7(t), mustV7(t)
	w, opening, err := wallet.Open(wallet.OpenParams{
		ID: mustV7(t), PlayerID: uuid.New(), InitialBalance: brlMoney(t, "100.00"),
		OpeningTransactionID: openingID, LedgerEntryID: entryID, Now: nowUS(),
	})
	if err != nil {
		t.Fatal(err)
	}
	openingTx, err := wagering.NewOpening(openingID, w.ID(), w.PlayerID(), brlMoney(t, "100.00"), nowUS())
	if err != nil {
		t.Fatal(err)
	}
	if err := openingTx.CompleteOpening(w, *opening, nowUS()); err != nil {
		t.Fatal(err)
	}
	entries := []wallet.LedgerEntry{*opening}
	persisted := []*wagering.WagerTransaction{openingTx}
	for range 4 {
		bet, err := wagering.NewExternal(mustV7(t), externalCommand(t, w, "BET", "1.00", ""), nowUS())
		if err != nil {
			t.Fatal(err)
		}
		entry, err := bet.Process(wagering.ProcessInput{
			Wallet: w, LedgerEntryID: mustV7(t), RetryPolicy: wagering.DefaultRetryPolicy(), Now: nowUS(),
		})
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, *entry)
		persisted = append(persisted, bet)
	}
	if err := wallets.Create(ctx, w); err != nil {
		t.Fatal(err)
	}
	for _, tx := range persisted {
		if _, err := txs.Insert(ctx, tx); err != nil {
			t.Fatal(err)
		}
	}
	ids := make([]uuid.UUID, 0, len(entries))
	for _, e := range entries {
		if err := ledger.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID())
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return w.ID(), ids
}

func mustV7(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func entryIDs(entries []wallet.LedgerEntry) []uuid.UUID {
	ids := make([]uuid.UUID, len(entries))
	for i, e := range entries {
		ids[i] = e.ID()
	}
	return ids
}

func TestLedgerListPagesInIDOrder(t *testing.T) {
	walletID, want := seedLedger(t)
	ledger := NewLedgerRepository(newTestDB(t))
	ctx := context.Background()

	var got []uuid.UUID
	after := uuid.Nil
	for page := 0; ; page++ {
		entries, err := ledger.List(ctx, walletID, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			break
		}
		if len(entries) > 2 {
			t.Fatalf("page %d has %d entries, limit 2", page, len(entries))
		}
		got = append(got, entryIDs(entries)...)
		after = entries[len(entries)-1].ID()
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paged ids %v, want %v", got, want)
	}

	all, err := ledger.List(ctx, walletID, uuid.Nil, 50)
	if err != nil || len(all) != 5 || all[0].Direction() != wallet.DirectionCredit || all[0].Amount().Amount() != "100.00" {
		t.Fatalf("List all = %d entries, err %v", len(all), err)
	}
	empty, err := ledger.List(ctx, uuid.New(), uuid.Nil, 50)
	if err != nil || len(empty) != 0 {
		t.Fatalf("unknown wallet: %d entries, err %v", len(empty), err)
	}
}

func TestLedgerTotals(t *testing.T) {
	walletID, _ := seedLedger(t)
	ledger := NewLedgerRepository(newTestDB(t))
	net, count, err := ledger.Totals(context.Background(), walletID)
	if err != nil || net != 9600 || count != 5 {
		t.Fatalf("Totals = %d, %d, %v; want 9600, 5", net, count, err)
	}
	net, count, err = ledger.Totals(context.Background(), uuid.New())
	if err != nil || net != 0 || count != 0 {
		t.Fatalf("Totals(unknown) = %d, %d, %v; want 0, 0", net, count, err)
	}
}
