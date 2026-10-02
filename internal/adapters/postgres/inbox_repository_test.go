//go:build !unit && !e2e

package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func TestInboxRepository(t *testing.T) {
	db := pgtest.AppDB(t)
	repo := NewInboxRepository(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	msg := app.InboxMessage{Consumer: "wager-transactions", MessageID: "msg-" + uuid.NewString(),
		PayloadHash: strings.Repeat("a", 64), ReceivedAt: now, ProcessedAt: now.Add(time.Millisecond)}

	if inserted, err := repo.Insert(ctx, msg); err != nil || !inserted {
		t.Fatalf("insert = %v %v", inserted, err)
	}
	got, err := repo.Get(ctx, msg.Consumer, msg.MessageID)
	if err != nil || got != msg {
		t.Fatalf("get = %+v %v, want %+v", got, err, msg)
	}
	if inserted, err := repo.Insert(ctx, msg); err != nil || inserted {
		t.Fatalf("duplicate insert = %v %v, want false nil", inserted, err)
	}
	other := msg
	other.Consumer = "another-consumer"
	if inserted, err := repo.Insert(ctx, other); err != nil || !inserted {
		t.Fatalf("same messageId for another consumer = %v %v", inserted, err)
	}
	if _, err := repo.Get(ctx, msg.Consumer, "missing-"+uuid.NewString()); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing = %v, want ErrNotFound", err)
	}
}

func TestInboxInsertRollsBackWithItsTransaction(t *testing.T) {
	db := pgtest.AppDB(t)
	repo := NewInboxRepository(db)
	tm := NewTxManager(db, config.Config{Database: config.Database{LockTimeout: time.Second, StatementTimeout: 5 * time.Second}})
	ctx := context.Background()
	id := "msg-" + uuid.NewString()
	boom := errors.New("boom")
	err := tm.WithinTx(ctx, func(ctx context.Context) error {
		now := time.Now().UTC()
		if _, err := repo.Insert(ctx, app.InboxMessage{Consumer: "c", MessageID: id,
			PayloadHash: strings.Repeat("0", 64), ReceivedAt: now, ProcessedAt: now}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, "c", id); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("rolled-back inbox entry is visible: %v", err)
	}
}
