//go:build !integration && !e2e

package wallet

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

var testNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func brl(t *testing.T, minor int64) money.Money {
	t.Helper()
	m, err := money.FromMinor(minor, money.BRL)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func validEntryParams(t *testing.T) LedgerEntryParams {
	return LedgerEntryParams{
		ID:            uuid.New(),
		WalletID:      uuid.New(),
		TransactionID: uuid.New(),
		Direction:     DirectionDebit,
		Amount:        brl(t, 2500),
		BalanceBefore: brl(t, 100000),
		BalanceAfter:  brl(t, 97500),
		CreatedAt:     testNow,
	}
}

func TestNewLedgerEntryValid(t *testing.T) {
	p := validEntryParams(t)
	e, err := NewLedgerEntry(p)
	if err != nil {
		t.Fatalf("NewLedgerEntry: %v", err)
	}
	if e.ID() != p.ID || e.WalletID() != p.WalletID || e.TransactionID() != p.TransactionID ||
		e.Direction() != DirectionDebit || !e.Amount().Equal(p.Amount) ||
		!e.BalanceBefore().Equal(p.BalanceBefore) || !e.BalanceAfter().Equal(p.BalanceAfter) ||
		!e.CreatedAt().Equal(testNow) {
		t.Fatalf("entry fields do not match params: %+v", e)
	}

	credit := validEntryParams(t)
	credit.Direction = DirectionCredit
	credit.BalanceAfter = brl(t, 102500)
	if _, err := NewLedgerEntry(credit); err != nil {
		t.Fatalf("credit entry: %v", err)
	}
}

func TestNewLedgerEntryInvalid(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(p *LedgerEntryParams)
	}{
		{"nil id", func(p *LedgerEntryParams) { p.ID = uuid.Nil }},
		{"nil wallet", func(p *LedgerEntryParams) { p.WalletID = uuid.Nil }},
		{"nil transaction", func(p *LedgerEntryParams) { p.TransactionID = uuid.Nil }},
		{"bad direction", func(p *LedgerEntryParams) { p.Direction = "SIDEWAYS" }},
		{"zero amount", func(p *LedgerEntryParams) { p.Amount = brl(t, 0) }},
		{"negative amount", func(p *LedgerEntryParams) { p.Amount = brl(t, -2500) }},
		{"uninitialized amount", func(p *LedgerEntryParams) { p.Amount = money.Money{} }},
		{"wrong after", func(p *LedgerEntryParams) { p.BalanceAfter = brl(t, 97400) }},
		{"credit math on debit", func(p *LedgerEntryParams) { p.BalanceAfter = brl(t, 102500) }},
		{"negative after", func(p *LedgerEntryParams) {
			p.BalanceBefore, p.BalanceAfter = brl(t, 1000), brl(t, -1500)
		}},
		{"currency mismatch", func(p *LedgerEntryParams) {
			usd, _ := money.FromMinor(97500, money.USD)
			p.BalanceAfter = usd
		}},
		{"zero time", func(p *LedgerEntryParams) { p.CreatedAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := validEntryParams(t)
			tt.mutate(&p)
			if _, err := NewLedgerEntry(p); !errors.Is(err, ErrInvalidLedgerEntry) {
				t.Fatalf("error = %v, want ErrInvalidLedgerEntry", err)
			}
		})
	}
}
