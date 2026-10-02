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
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

func externalCommand(t *testing.T, w *wallet.Wallet, kind, amount, ref string) wagering.Command {
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

// nowUS matches PostgreSQL's microsecond timestamp precision, so State
// round trips compare equal.
func nowUS() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func process(t *testing.T, tx *wagering.WagerTransaction, w *wallet.Wallet, ref *wagering.Reference) {
	t.Helper()
	_, err := tx.Process(wagering.ProcessInput{
		Wallet: w, Reference: ref, LedgerEntryID: uuid.New(),
		RetryPolicy: wagering.DefaultRetryPolicy(), Now: nowUS(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTransactionInsertGetAndFind(t *testing.T) {
	repo := NewTransactionRepository(newTestDB(t))
	ctx := context.Background()
	w := newWallet(t, "100.00")
	cmd := externalCommand(t, w, "BET", "25.00", "")
	tx, _ := wagering.NewExternal(uuid.New(), cmd, nowUS())

	inserted, err := repo.Insert(ctx, tx)
	if err != nil || !inserted {
		t.Fatalf("Insert = %v, %v", inserted, err)
	}
	again, _ := wagering.NewExternal(uuid.New(), cmd, time.Now())
	if inserted, err := repo.Insert(ctx, again); err != nil || inserted {
		t.Fatalf("same idempotency key Insert = %v, %v; want false, nil", inserted, err)
	}

	for name, get := range map[string]func() (*wagering.WagerTransaction, error){
		"by id": func() (*wagering.WagerTransaction, error) { return repo.Get(ctx, tx.ID()) },
		"by key": func() (*wagering.WagerTransaction, error) {
			return repo.FindByIdempotencyKey(ctx, "provider-a", cmd.IdempotencyKey)
		},
		"by ext": func() (*wagering.WagerTransaction, error) {
			return repo.FindByExternalID(ctx, "provider-a", cmd.ExternalTransactionID)
		},
	} {
		got, err := get()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.State() != tx.State() {
			t.Fatalf("%s round trip\n got %+v\nwant %+v", name, got.State(), tx.State())
		}
	}

	if _, err := repo.FindByIdempotencyKey(ctx, "provider-b", cmd.IdempotencyKey); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("keys are scoped by provider: err %v", err)
	}
	if _, err := repo.Get(ctx, uuid.New()); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing: err %v", err)
	}
}

func TestTransactionConcurrentInsertSameKey(t *testing.T) {
	repo := NewTransactionRepository(newTestDB(t))
	w := newWallet(t, "100.00")
	cmd := externalCommand(t, w, "BET", "25.00", "")

	var wg sync.WaitGroup
	results := make(chan bool, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, _ := wagering.NewExternal(uuid.New(), cmd, time.Now())
			inserted, err := repo.Insert(context.Background(), tx)
			if err != nil {
				t.Error(err)
			}
			results <- inserted
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for inserted := range results {
		if inserted {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("inserted %d times, want exactly 1", count)
	}
}

func TestTransactionUpdateAndReversalLookup(t *testing.T) {
	repo := NewTransactionRepository(newTestDB(t))
	ctx := context.Background()
	w := newWallet(t, "100.00")

	bet, _ := wagering.NewExternal(uuid.New(), externalCommand(t, w, "BET", "30.00", ""), time.Now())
	if _, err := repo.Insert(ctx, bet); err != nil {
		t.Fatal(err)
	}
	process(t, bet, w, nil)
	if err := repo.Update(ctx, bet); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.Get(ctx, bet.ID())
	if stored.Status() != wagering.StatusProcessed || stored.ResultBalance().Amount() != "70.00" {
		t.Fatalf("stored = %s %s", stored.Status(), stored.ResultBalance())
	}

	for _, tt := range []struct {
		kind wagering.Kind
		want bool
	}{{wagering.KindRefund, false}, {wagering.KindRollback, false}} {
		if got, err := repo.HasProcessedReversal(ctx, bet.ID(), tt.kind, wagering.KindBet); err != nil || got != tt.want {
			t.Fatalf("before refund %s: %v %v", tt.kind, got, err)
		}
	}

	ref := bet.AsReference()
	refund, _ := wagering.NewExternal(uuid.New(), externalCommand(t, w, "REFUND", "30.00", bet.ExternalTransactionID()), time.Now())
	process(t, refund, w, &ref)
	if _, err := repo.Insert(ctx, refund); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.HasProcessedReversal(ctx, bet.ID(), wagering.KindRefund, wagering.KindBet); !got {
		t.Fatal("refund of the bet must be found")
	}
	if got, _ := repo.HasProcessedReversal(ctx, bet.ID(), wagering.KindRollback, wagering.KindBet); !got {
		t.Fatal("any reversal of a BET blocks a ROLLBACK of it")
	}
	if got, _ := repo.HasProcessedReversal(ctx, refund.ID(), wagering.KindRollback, wagering.KindRefund); got {
		t.Fatal("the refund itself has no reversal yet")
	}

	if err := repo.Update(ctx, bet); err == nil {
		t.Fatal("updating a terminal transaction must be refused by the database")
	}
	missing, _ := wagering.NewExternal(uuid.New(), externalCommand(t, w, "BET", "1.00", ""), time.Now())
	if err := repo.Update(ctx, missing); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("update of missing row: err %v", err)
	}
}
