//go:build !unit && !e2e

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	pgtest.AppDB(t) // fails fast with setup instructions when unreachable
	db, err := Open(config.Database{URL: pgtest.AppURL(), MaxOpenConns: 10, LockTimeout: time.Second, StatementTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func testTxManager(db *gorm.DB, lockTimeout time.Duration) *TxManager {
	return NewTxManager(db, config.Config{Database: config.Database{LockTimeout: lockTimeout, StatementTimeout: 5 * time.Second}})
}

func walletExists(t *testing.T, db *gorm.DB, id any) bool {
	t.Helper()
	var n int64
	if err := db.Table("wallets").Where("id = ?", id).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestWithinTxCommitsAndRollsBack(t *testing.T) {
	db := newTestDB(t)
	tm := testTxManager(db, time.Second)
	ctx := context.Background()

	committed := walletRow(uuid.New(), "BRL", 0)
	err := tm.WithinTx(ctx, func(ctx context.Context) error {
		return conn(ctx, db).Table("wallets").Create(committed).Error
	})
	if err != nil || !walletExists(t, db, committed["id"]) {
		t.Fatalf("commit: err %v", err)
	}

	rolledBack := walletRow(uuid.New(), "BRL", 0)
	boom := errors.New("boom")
	err = tm.WithinTx(ctx, func(ctx context.Context) error {
		if err := conn(ctx, db).Table("wallets").Create(rolledBack).Error; err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || walletExists(t, db, rolledBack["id"]) {
		t.Fatalf("rollback: err %v, row must not exist", err)
	}
}

func TestWithinTxNestedJoinsOuter(t *testing.T) {
	db := newTestDB(t)
	tm := testTxManager(db, time.Second)
	inner := walletRow(uuid.New(), "BRL", 0)
	boom := errors.New("outer fails")
	err := tm.WithinTx(context.Background(), func(ctx context.Context) error {
		if err := tm.WithinTx(ctx, func(ctx context.Context) error {
			return conn(ctx, db).Table("wallets").Create(inner).Error
		}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || walletExists(t, db, inner["id"]) {
		t.Fatalf("nested work must roll back with the outer transaction: err %v", err)
	}
}

func TestWithinTxAppliesTimeouts(t *testing.T) {
	db := newTestDB(t)
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1) // the "outside" check reuses the same connection
	tm := NewTxManager(db, config.Config{Database: config.Database{LockTimeout: 1500 * time.Millisecond, StatementTimeout: 7 * time.Second}})
	err = tm.WithinTx(context.Background(), func(ctx context.Context) error {
		var lock, stmt string
		if err := conn(ctx, db).Raw("SHOW lock_timeout").Scan(&lock).Error; err != nil {
			return err
		}
		if err := conn(ctx, db).Raw("SHOW statement_timeout").Scan(&stmt).Error; err != nil {
			return err
		}
		if lock != "1500ms" || stmt != "7s" {
			t.Errorf("lock_timeout %q statement_timeout %q, want 1500ms and 7s", lock, stmt)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var outside string
	if err := db.Raw("SHOW lock_timeout").Scan(&outside).Error; err != nil || outside == "1500ms" {
		t.Fatalf("timeouts must be transaction-local, got %q (%v)", outside, err)
	}
}

func TestLockTimeoutIsTransient(t *testing.T) {
	db := newTestDB(t)
	row := walletRow(uuid.New(), "BRL", 0)
	if err := db.Table("wallets").Create(row).Error; err != nil {
		t.Fatal(err)
	}
	holder := testTxManager(db, 5*time.Second)
	waiter := testTxManager(db, 200*time.Millisecond)

	locked, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- holder.WithinTx(context.Background(), func(ctx context.Context) error {
			if err := conn(ctx, db).Exec("SELECT 1 FROM wallets WHERE id = ? FOR UPDATE", row["id"]).Error; err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	select {
	case <-locked:
	case err := <-done:
		t.Fatalf("holder returned before locking: %v", err)
	}

	start := time.Now()
	err := waiter.WithinTx(context.Background(), func(ctx context.Context) error {
		return conn(ctx, db).Exec("SELECT 1 FROM wallets WHERE id = ? FOR UPDATE", row["id"]).Error
	})
	close(release)
	if !errors.Is(err, app.ErrTransient) {
		t.Fatalf("error = %v, want app.ErrTransient (lock_timeout)", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("waited %s, lock_timeout not applied", elapsed)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestConnHonorsPerCallContextInTx(t *testing.T) {
	db := newTestDB(t)
	tm := testTxManager(db, time.Second)
	start := time.Now()
	err := tm.WithinTx(context.Background(), func(ctx context.Context) error {
		child, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
		defer cancel()
		return conn(child, db).Exec("SELECT pg_sleep(5)").Error
	})
	if err == nil {
		t.Fatal("pg_sleep succeeded, want error from child context timeout")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("took %s, per-call context not honored", elapsed)
	}
}
