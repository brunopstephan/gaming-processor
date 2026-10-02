//go:build !unit && !e2e

package postgres

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

// expectedTables grows as later tasks add migrations.
var expectedTables = []string{"wallets", "wager_transactions", "wallet_ledger_entries", "inbox_messages", "outbox_events"}

// expectedVersion is the latest migration number.
const expectedVersion = 5

func publicTables(t *testing.T, url string) []string {
	t.Helper()
	var names []string
	err := pgtest.Open(t, url).Raw(
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name <> 'schema_migrations' ORDER BY table_name`,
	).Scan(&names).Error
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestMigrationsUpDownUp(t *testing.T) {
	owner := pgtest.OwnerDB(t)
	name := fmt.Sprintf("wallet_mig_%d", time.Now().UnixNano())
	if err := owner.Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)") })

	url := pgtest.WithDatabase(pgtest.OwnerURL(), name)
	m, err := migrate.New("file://"+pgtest.MigrationsPath, strings.Replace(url, "postgres://", "pgx5://", 1))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if err := m.Up(); err != nil {
		t.Fatalf("up: %v", err)
	}
	if v, dirty, err := m.Version(); err != nil || dirty || v != expectedVersion {
		t.Fatalf("version = %d dirty %v err %v, want %d clean", v, dirty, err, expectedVersion)
	}
	want := slices.Sorted(slices.Values(expectedTables))
	if got := publicTables(t, url); !slices.Equal(got, want) {
		t.Fatalf("tables after up = %v, want %v", got, want)
	}

	if err := m.Down(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if got := publicTables(t, url); len(got) != 0 {
		t.Fatalf("tables after down = %v, want none", got)
	}

	if err := m.Up(); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if got := publicTables(t, url); !slices.Equal(got, want) {
		t.Fatalf("tables after second up = %v, want %v", got, want)
	}

	// Prove the re-applied schema is usable and enforces its constraints.
	fresh := pgtest.Open(t, url)
	if err := fresh.Table("wallets").Create(walletRow(uuid.New(), "BRL", 0)).Error; err != nil {
		t.Fatalf("insert wallet on fresh schema: %v", err)
	}
	pgtest.RequirePgError(t, fresh.Table("wallets").Create(walletRow(uuid.New(), "BRL", -1)).Error,
		"23514", "wallets_balance_minor_check")
}
