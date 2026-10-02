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
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
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

func mustGet(t *testing.T, repo *WalletRepository, ctx context.Context, id uuid.UUID) *wallet.Wallet {
	t.Helper()
	w, err := repo.Get(ctx, id)
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

	dup, _, err := wallet.Open(wallet.OpenParams{ID: uuid.New(), PlayerID: w.PlayerID(), InitialBalance: brlMoney(t, "0.00"), Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, dup); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("duplicate (player, currency) error = %v, want ErrConflict", err)
	}
}

func TestWalletGetForUpdateRequiresTransaction(t *testing.T) {
	repo := NewWalletRepository(newTestDB(t))
	if _, err := repo.GetForUpdate(context.Background(), uuid.New()); !errors.Is(err, errNoTransaction) {
		t.Fatalf("GetForUpdate outside a transaction error = %v, want errNoTransaction", err)
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
	got := mustGet(t, repo, ctx, w.ID())
	if got.Balance().Amount() != "75.00" || got.Version() != 2 {
		t.Fatalf("after update: balance %s version %d", got.Balance(), got.Version())
	}

	// Two writers read version 2; the first commits, the second is stale.
	first := mustGet(t, repo, ctx, w.ID())
	second := mustGet(t, repo, ctx, w.ID())
	for _, c := range []*wallet.Wallet{first, second} {
		if _, err := c.Credit(uuid.New(), uuid.New(), brlMoney(t, "1.00"), time.Now()); err != nil {
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
	final := mustGet(t, repo, ctx, w.ID())
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
	got := mustGet(t, repo, ctx, w.ID())
	if ok != 1 || insufficient != 1 || got.Balance().Amount() != "20.00" || got.Version() != 2 {
		t.Fatalf("ok %d insufficient %d balance %s version %d", ok, insufficient, got.Balance(), got.Version())
	}
}

// holdLock starts a transaction that locks id and holds it until release is
// closed. It returns once the lock is held; done delivers the tx result.
func holdLock(t *testing.T, repo *WalletRepository, tm *TxManager, id uuid.UUID) (release func(), done <-chan error) {
	t.Helper()
	locked := make(chan struct{})
	rel := make(chan struct{})
	res := make(chan error, 1)
	go func() {
		res <- tm.WithinTx(context.Background(), func(ctx context.Context) error {
			if _, err := repo.GetForUpdate(ctx, id); err != nil {
				return err
			}
			close(locked)
			<-rel
			return nil
		})
	}()
	select {
	case <-locked:
	case err := <-res:
		t.Fatalf("holder transaction ended before locking: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("holder transaction did not acquire the lock")
	}
	var once sync.Once
	return func() { once.Do(func() { close(rel) }) }, res
}

func TestWalletGetForUpdateHonorsPerCallContext(t *testing.T) {
	db := newTestDB(t)
	repo := NewWalletRepository(db)
	ctx := context.Background()
	w := newWallet(t, "10.00")
	if err := repo.Create(ctx, w); err != nil {
		t.Fatal(err)
	}
	release, done := holdLock(t, repo, testTxManager(db, 5*time.Second), w.ID())
	defer release()

	start := time.Now()
	err := testTxManager(db, 5*time.Second).WithinTx(ctx, func(ctx context.Context) error {
		child, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		_, err := repo.GetForUpdate(child, w.ID())
		return err
	})
	if err == nil {
		t.Fatal("GetForUpdate on a locked wallet must fail when the call context expires")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("returned after %v, per-call context ignored", elapsed)
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWalletLockIsPerWalletAndTimesOutAsTransient(t *testing.T) {
	db := newTestDB(t)
	repo := NewWalletRepository(db)
	ctx := context.Background()
	w1, w2 := newWallet(t, "10.00"), newWallet(t, "10.00")
	for _, w := range []*wallet.Wallet{w1, w2} {
		if err := repo.Create(ctx, w); err != nil {
			t.Fatal(err)
		}
	}
	release, done := holdLock(t, repo, testTxManager(db, 5*time.Second), w1.ID())
	defer release()

	// Tx B: same wallet, short lock_timeout -> transient.
	errB := testTxManager(db, 200*time.Millisecond).WithinTx(ctx, func(ctx context.Context) error {
		_, err := repo.GetForUpdate(ctx, w1.ID())
		return err
	})
	if !errors.Is(errB, app.ErrTransient) {
		t.Fatalf("contended lock error = %v, want ErrTransient", errB)
	}

	// Tx C: a different wallet is not blocked while A still holds w1.
	resC := make(chan error, 1)
	go func() {
		resC <- testTxManager(db, 5*time.Second).WithinTx(ctx, func(ctx context.Context) error {
			_, err := repo.GetForUpdate(ctx, w2.ID())
			return err
		})
	}()
	select {
	case err := <-resC:
		if err != nil {
			t.Fatalf("lock on a different wallet failed: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("lock on a different wallet blocked: locking is not per wallet")
	}
	release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
