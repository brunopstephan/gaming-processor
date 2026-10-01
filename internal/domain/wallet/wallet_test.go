//go:build !integration && !e2e

package wallet

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

func openWallet(t *testing.T, balanceMinor int64) *Wallet {
	t.Helper()
	w, _, err := Open(OpenParams{
		ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl(t, balanceMinor),
		OpeningTransactionID: uuid.New(), LedgerEntryID: uuid.New(), Now: testNow,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return w
}

func TestOpenWithPositiveBalance(t *testing.T) {
	p := OpenParams{
		ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl(t, 100000),
		OpeningTransactionID: uuid.New(), LedgerEntryID: uuid.New(), Now: testNow,
	}
	w, entry, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if w.Version() != InitialVersion || w.Version() != 1 {
		t.Fatalf("Version() = %d, want 1 (opening credit must not bump the version)", w.Version())
	}
	if !w.Balance().Equal(p.InitialBalance) || w.Currency() != money.BRL {
		t.Fatalf("Balance() = %v", w.Balance())
	}
	if entry == nil {
		t.Fatal("positive opening must produce a ledger entry")
	}
	if entry.Direction() != DirectionCredit || !entry.BalanceBefore().IsZero() ||
		!entry.BalanceAfter().Equal(p.InitialBalance) || entry.TransactionID() != p.OpeningTransactionID ||
		entry.ID() != p.LedgerEntryID || entry.WalletID() != p.ID {
		t.Fatalf("unexpected opening entry %+v", entry)
	}
}

func TestOpenWithZeroBalance(t *testing.T) {
	w, entry, err := Open(OpenParams{ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl(t, 0), Now: testNow})
	if err != nil {
		t.Fatal(err)
	}
	if entry != nil {
		t.Fatal("zero opening must not produce a ledger entry")
	}
	if w.Version() != 1 || !w.Balance().IsZero() {
		t.Fatalf("wallet = %+v", w)
	}
}

func TestOpenInvalid(t *testing.T) {
	tests := []struct {
		name string
		p    OpenParams
	}{
		{"nil id", OpenParams{PlayerID: uuid.New(), InitialBalance: brl(t, 0), Now: testNow}},
		{"nil player", OpenParams{ID: uuid.New(), InitialBalance: brl(t, 0), Now: testNow}},
		{"negative", OpenParams{ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl(t, -1), Now: testNow}},
		{"uninitialized", OpenParams{ID: uuid.New(), PlayerID: uuid.New(), Now: testNow}},
		{"zero time", OpenParams{ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl(t, 0)}},
		{"positive without entry ids", OpenParams{ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl(t, 100), Now: testNow}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := Open(tt.p); err == nil {
				t.Fatal("Open succeeded, want error")
			}
		})
	}
}

func TestRehydrate(t *testing.T) {
	valid := RehydrateParams{
		ID: uuid.New(), PlayerID: uuid.New(), Balance: brl(t, 2000), Version: 7,
		CreatedAt: testNow, UpdatedAt: testNow.Add(time.Hour),
	}
	w, err := Rehydrate(valid)
	if err != nil {
		t.Fatal(err)
	}
	if w.Version() != 7 || !w.Balance().Equal(valid.Balance) || !w.UpdatedAt().Equal(valid.UpdatedAt) {
		t.Fatalf("rehydrated wallet = %+v", w)
	}

	tests := []struct {
		name   string
		mutate func(p *RehydrateParams)
	}{
		{"version 0", func(p *RehydrateParams) { p.Version = 0 }},
		{"negative balance", func(p *RehydrateParams) { p.Balance = brl(t, -1) }},
		{"uninitialized balance", func(p *RehydrateParams) { p.Balance = money.Money{} }},
		{"nil id", func(p *RehydrateParams) { p.ID = uuid.Nil }},
		{"nil player", func(p *RehydrateParams) { p.PlayerID = uuid.Nil }},
		{"zero createdAt", func(p *RehydrateParams) { p.CreatedAt = time.Time{} }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := valid
			tt.mutate(&p)
			if _, err := Rehydrate(p); !errors.Is(err, ErrInvalidWallet) {
				t.Fatalf("error = %v, want ErrInvalidWallet", err)
			}
		})
	}
}

func TestDebitAndCredit(t *testing.T) {
	w := openWallet(t, 10000)
	later := testNow.Add(time.Minute)

	entry, err := w.Debit(uuid.New(), uuid.New(), brl(t, 8000), later)
	if err != nil {
		t.Fatal(err)
	}
	if w.Balance().MinorUnits() != 2000 || w.Version() != 2 || !w.UpdatedAt().Equal(later) {
		t.Fatalf("after debit: balance %v version %d", w.Balance(), w.Version())
	}
	if entry.Direction() != DirectionDebit || entry.BalanceBefore().MinorUnits() != 10000 ||
		entry.BalanceAfter().MinorUnits() != 2000 {
		t.Fatalf("debit entry %+v", entry)
	}

	if _, err := w.Credit(uuid.New(), uuid.New(), brl(t, 500), later); err != nil {
		t.Fatal(err)
	}
	if w.Balance().MinorUnits() != 2500 || w.Version() != 3 {
		t.Fatalf("after credit: balance %v version %d", w.Balance(), w.Version())
	}
}

func TestDebitExactBalance(t *testing.T) {
	w := openWallet(t, 10000)
	if _, err := w.Debit(uuid.New(), uuid.New(), brl(t, 10000), testNow); err != nil {
		t.Fatal(err)
	}
	if !w.Balance().IsZero() {
		t.Fatalf("balance = %v, want 0.00", w.Balance())
	}
}

func TestMovementRejectionsLeaveWalletUntouched(t *testing.T) {
	usd, _ := money.FromMinor(100, money.USD)
	maxBRL, _ := money.FromMinor(1<<62, money.BRL)
	tests := []struct {
		name    string
		balance int64
		op      func(w *Wallet) error
		want    error
	}{
		{"insufficient", 10000, func(w *Wallet) error {
			_, err := w.Debit(uuid.New(), uuid.New(), brl(t, 10001), testNow)
			return err
		}, ErrInsufficientFunds},
		{"currency mismatch", 10000, func(w *Wallet) error {
			_, err := w.Credit(uuid.New(), uuid.New(), usd, testNow)
			return err
		}, ErrCurrencyMismatch},
		{"zero amount", 10000, func(w *Wallet) error {
			_, err := w.Debit(uuid.New(), uuid.New(), brl(t, 0), testNow)
			return err
		}, ErrInvalidAmount},
		{"credit overflow", 1 << 62, func(w *Wallet) error {
			_, err := w.Credit(uuid.New(), uuid.New(), maxBRL, testNow)
			return err
		}, money.ErrOverflow},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := openWallet(t, tt.balance)
			before, version := w.Balance(), w.Version()
			if err := tt.op(w); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
			if !w.Balance().Equal(before) || w.Version() != version {
				t.Fatal("rejected movement must not change the wallet")
			}
		})
	}
}
