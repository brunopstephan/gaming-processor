//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

func eventTypes(rows []apptest.OutboxRow) []string {
	types := make([]string, len(rows))
	for i, r := range rows {
		types[i] = r.EventType
	}
	return types
}

func TestOpenWalletWithPositiveBalance(t *testing.T) {
	h := apptest.New(t)
	ctx := context.Background()
	cmd, err := app.ParseOpenWallet(uuid.NewString(), "1000.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	w, err := app.NewWalletService(h.Deps).Open(ctx, cmd, app.Meta{CorrelationID: "corr-open"})
	if err != nil {
		t.Fatal(err)
	}

	stored, err := h.Wallets.Get(ctx, w.ID())
	if err != nil || stored.Balance().Amount() != "1000.00" || stored.Version() != 1 {
		t.Fatalf("stored wallet %+v err %v", stored, err)
	}
	entries, err := h.Ledger.List(ctx, w.ID(), uuid.Nil, 10)
	if err != nil || len(entries) != 1 || entries[0].Direction() != wallet.DirectionCredit ||
		!entries[0].BalanceBefore().IsZero() || entries[0].BalanceAfter().Amount() != "1000.00" {
		t.Fatalf("ledger %+v err %v", entries, err)
	}
	opening, err := h.Transactions.Get(ctx, entries[0].TransactionID())
	if err != nil || opening.Kind() != wagering.KindOpening || opening.Status() != wagering.StatusProcessed {
		t.Fatalf("opening transaction %+v err %v", opening, err)
	}

	rows := h.OutboxRows(t, w.ID())
	if got := eventTypes(rows); !slices.Equal(got, []string{"WagerTransactionProcessed", "WalletBalanceChanged"}) {
		t.Fatalf("events = %v", got)
	}
	if rows[0].CorrelationID != "corr-open" || rows[0].Data["origin"] != "INTERNAL" || rows[0].Data["kind"] != "OPENING" {
		t.Fatalf("processed event %+v", rows[0])
	}
	if _, ok := rows[0].Data["providerId"]; ok {
		t.Fatal("OPENING event must not carry providerId")
	}
	if rows[1].Data["walletVersion"] != float64(1) {
		t.Fatalf("balance changed event walletVersion = %v, want 1", rows[1].Data["walletVersion"])
	}
}

func TestOpenWalletWithZeroBalance(t *testing.T) {
	h := apptest.New(t)
	ctx := context.Background()
	cmd, _ := app.ParseOpenWallet(uuid.NewString(), "0.00", "BRL")
	w, err := app.NewWalletService(h.Deps).Open(ctx, cmd, app.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := h.Ledger.List(ctx, w.ID(), uuid.Nil, 10)
	if err != nil || len(entries) != 0 || len(h.OutboxRows(t, w.ID())) != 0 || w.Version() != 1 {
		t.Fatalf("zero opening must create no ledger and no events: entries %d err %v", len(entries), err)
	}
}

func TestOpenWalletDuplicate(t *testing.T) {
	h := apptest.New(t)
	ctx := context.Background()
	svc := app.NewWalletService(h.Deps)
	player := uuid.NewString()
	brl, _ := app.ParseOpenWallet(player, "10.00", "BRL")
	if _, err := svc.Open(ctx, brl, app.Meta{}); err != nil {
		t.Fatal(err)
	}
	again, _ := app.ParseOpenWallet(player, "99.00", "BRL")
	if _, err := svc.Open(ctx, again, app.Meta{}); !errors.Is(err, app.ErrWalletAlreadyExists) {
		t.Fatalf("duplicate error = %v, want ErrWalletAlreadyExists", err)
	}
	usd, _ := app.ParseOpenWallet(player, "5.00", "USD")
	if _, err := svc.Open(ctx, usd, app.Meta{}); err != nil {
		t.Fatalf("another currency is a different wallet: %v", err)
	}
	var wallets, openings int64
	if err := h.DB.Table("wallets").Where("player_id = ?", player).Count(&wallets).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Table("wager_transactions").Where("player_id = ? AND kind = 'OPENING'", player).Count(&openings).Error; err != nil {
		t.Fatal(err)
	}
	if wallets != 2 || openings != 2 {
		t.Fatalf("wallets %d openings %d, want 2 and 2 (duplicate rolled back)", wallets, openings)
	}
}
