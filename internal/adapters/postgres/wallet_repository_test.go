//go:build !unit && !e2e

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

func brlMoney(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func newWallet(t *testing.T, balance string) *wallet.Wallet {
	t.Helper()
	id, _ := uuid.NewV7()
	w, _, err := wallet.Open(wallet.OpenParams{
		ID: id, PlayerID: uuid.New(), InitialBalance: brlMoney(t, balance),
		OpeningTransactionID: uuid.New(), LedgerEntryID: uuid.New(),
		Now: time.Date(2026, 10, 1, 9, 0, 0, 123456000, time.FixedZone("BRT", -3*3600)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWalletCreateAndGet(t *testing.T) {
	db := newTestDB(t)
	repo := NewWalletRepository(db)
	ctx := context.Background()
	w := newWallet(t, "100.00")

	if err := repo.Create(ctx, w); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, w.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != w.ID() || got.PlayerID() != w.PlayerID() || !got.Balance().Equal(w.Balance()) ||
		got.Version() != 1 || !got.CreatedAt().Equal(w.CreatedAt()) || !got.UpdatedAt().Equal(w.UpdatedAt()) {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, w)
	}
	if got.CreatedAt().Location() != time.UTC {
		t.Fatalf("CreatedAt location %v, want UTC", got.CreatedAt().Location())
	}

	if _, err := repo.Get(ctx, uuid.New()); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing wallet error = %v, want ErrNotFound", err)
	}

	dup, _, _ := wallet.Open(wallet.OpenParams{ID: uuid.New(), PlayerID: w.PlayerID(), InitialBalance: brlMoney(t, "0.00"), Now: time.Now()})
	if err := repo.Create(ctx, dup); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("duplicate (player, currency) error = %v, want ErrConflict", err)
	}
}

func TestWalletGetForUpdateRequiresTransaction(t *testing.T) {
	repo := NewWalletRepository(newTestDB(t))
	if _, err := repo.GetForUpdate(context.Background(), uuid.New()); err == nil {
		t.Fatal("GetForUpdate outside a transaction must fail")
	}
}

func TestWalletUpdateWithVersionGuard(t *testing.T) {
	db := newTestDB(t)
	repo := NewWalletRepository(db)
	tm := testTxManager(db, time.Second)
	ctx := context.Background()
	w := newWallet(t, "100.00")
	if err := repo.Create(ctx, w); err != nil {
		t.Fatal(err)
	}

	err := tm.WithinTx(ctx, func(ctx context.Context) error {
		locked, err := repo.GetForUpdate(ctx, w.ID())
		if err != nil {
			return err
		}
		before := locked.Version()
		if _, err := locked.Debit(uuid.New(), uuid.New(), brlMoney(t, "25.00"), time.Now()); err != nil {
			return err
		}
		return repo.Update(ctx, locked, before)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(ctx, w.ID())
	if got.Balance().Amount() != "75.00" || got.Version() != 2 {
		t.Fatalf("after update: balance %s version %d", got.Balance(), got.Version())
	}

	// Two writers read version 2; the first commits, the second is stale.
	first, _ := repo.Get(ctx, w.ID())
	second, _ := repo.Get(ctx, w.ID())
	for _, copy := range []*wallet.Wallet{first, second} {
		if _, err := copy.Credit(uuid.New(), uuid.New(), brlMoney(t, "1.00"), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Update(ctx, first, 2); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, second, 2); !errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("stale version error = %v, want ErrVersionConflict", err)
	}
	if err := repo.Update(ctx, second, second.Version()); err == nil || errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("version must advance by exactly one: err %v", err)
	}
	final, _ := repo.Get(ctx, w.ID())
	if final.Balance().Amount() != "76.00" || final.Version() != 3 {
		t.Fatalf("final: balance %s version %d", final.Balance(), final.Version())
	}
}

// Two concurrent debits of 80.00 on 100.00: the lock serializes them, the
// second sees 20.00 and is refused by the domain; no update is lost.
func TestWalletConcurrentDebitsDoNotLoseUpdates(t *testing.T) {
	db := newTestDB(t)
	repo := NewWalletRepository(db)
	tm := testTxManager(db, 5*time.Second)
	ctx := context.Background()
	w := newWallet(t, "100.00")
	if err := repo.Create(ctx, w); err != nil {
		t.Fatal(err)
	}

	amount := brlMoney(t, "80.00")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- tm.WithinTx(ctx, func(ctx context.Context) error {
				locked, err := repo.GetForUpdate(ctx, w.ID())
				if err != nil {
					return err
				}
				before := locked.Version()
				if _, err := locked.Debit(uuid.New(), uuid.New(), amount, time.Now()); err != nil {
					return err
				}
				return repo.Update(ctx, locked, before)
			})
		}()
	}
	wg.Wait()
	close(results)

	var ok, insufficient int
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, wallet.ErrInsufficientFunds):
			insufficient++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	got, _ := repo.Get(ctx, w.ID())
	if ok != 1 || insufficient != 1 || got.Balance().Amount() != "20.00" || got.Version() != 2 {
		t.Fatalf("ok %d insufficient %d balance %s version %d", ok, insufficient, got.Balance(), got.Version())
	}
}
