//go:build !unit && !e2e

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// A BET persisted atomically: transaction, balance, ledger and outbox in one commit.
func TestBetPersistsAtomically(t *testing.T) {
	db := newTestDB(t)
	tm := testTxManager(db, time.Second)
	wallets, txs := NewWalletRepository(db), NewTransactionRepository(db)
	ledger, outbox := NewLedgerRepository(db), NewOutboxRepository(db)
	ctx := context.Background()

	w := newWallet(t, "100.00")
	if err := wallets.Create(ctx, w); err != nil {
		t.Fatal(err)
	}
	bet, err := wagering.NewExternal(uuid.New(), externalCommand(t, w, "BET", "25.00", ""), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	var envs []events.Envelope
	err = tm.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := txs.Insert(ctx, bet); err != nil {
			return err
		}
		locked, err := wallets.GetForUpdate(ctx, w.ID())
		if err != nil {
			return err
		}
		before := locked.Version()
		entry, err := bet.Process(wagering.ProcessInput{
			Wallet: locked, LedgerEntryID: uuid.New(), RetryPolicy: wagering.DefaultRetryPolicy(), Now: time.Now(),
		})
		if err != nil {
			return err
		}
		if err := wallets.Update(ctx, locked, before); err != nil {
			return err
		}
		if err := txs.Update(ctx, bet); err != nil {
			return err
		}
		if err := ledger.Append(ctx, *entry); err != nil {
			return err
		}
		for _, data := range bet.PullEvents() {
			env, err := events.NewEnvelope(uuid.New(), data, "corr-1", "", time.Now())
			if err != nil {
				return err
			}
			envs = append(envs, env)
		}
		return outbox.Append(ctx, envs...)
	})
	if err != nil {
		t.Fatal(err)
	}

	var ledgerCount int64
	if err := db.Table("wallet_ledger_entries").Where("transaction_id = ?", bet.ID()).Count(&ledgerCount).Error; err != nil {
		t.Fatal(err)
	}
	if ledgerCount != 1 {
		t.Fatalf("ledger entries = %d, want 1", ledgerCount)
	}
	for _, env := range envs {
		var row outboxEventModel
		if err := db.Where("id = ?", env.EventID).Take(&row).Error; err != nil {
			t.Fatalf("outbox %s: %v", env.EventType, err)
		}
		want, err := json.Marshal(env)
		if err != nil {
			t.Fatal(err)
		}
		var gotJSON, wantJSON map[string]any
		if err := json.Unmarshal(row.Payload, &gotJSON); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(want, &wantJSON); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(gotJSON, wantJSON) || row.EventType != env.EventType ||
			row.AggregateID != env.AggregateID || row.AggregateType != OutboxAggregateWallet ||
			row.PublishedAt != nil || row.Attempts != 0 || !row.NextAttemptAt.Equal(row.OccurredAt) {
			t.Fatalf("outbox row %+v does not match envelope %+v", row, env)
		}
	}
	if len(envs) != 2 {
		t.Fatalf("events = %d, want Processed + BalanceChanged", len(envs))
	}

	if err := outbox.Append(ctx, envs[0]); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("republishing the same eventId into the outbox: err %v, want ErrConflict", err)
	}
}

func TestLedgerAppendRollsBackWithTransaction(t *testing.T) {
	db := newTestDB(t)
	tm := testTxManager(db, time.Second)
	wallets, txs, ledger := NewWalletRepository(db), NewTransactionRepository(db), NewLedgerRepository(db)
	ctx := context.Background()

	w := newWallet(t, "100.00")
	bet, err := wagering.NewExternal(uuid.New(), externalCommand(t, w, "BET", "10.00", ""), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("crash before commit")
	err = tm.WithinTx(ctx, func(ctx context.Context) error {
		if err := wallets.Create(ctx, w); err != nil {
			return err
		}
		if _, err := txs.Insert(ctx, bet); err != nil {
			return err
		}
		entry, err := bet.Process(wagering.ProcessInput{Wallet: w, LedgerEntryID: uuid.New(), RetryPolicy: wagering.DefaultRetryPolicy(), Now: time.Now()})
		if err != nil {
			return err
		}
		if err := ledger.Append(ctx, *entry); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := wallets.Get(ctx, w.ID()); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("wallet must not exist after rollback: %v", err)
	}
	var n int64
	if err := db.Table("wallet_ledger_entries").Where("transaction_id = ?", bet.ID()).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("ledger entry must not survive the rollback")
	}
}
