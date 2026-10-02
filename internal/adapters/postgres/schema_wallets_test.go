//go:build !unit && !e2e

package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

var schemaNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func walletRow(playerID uuid.UUID, currency string, balance int64) map[string]any {
	return map[string]any{
		"id": uuid.New(), "player_id": playerID, "currency": currency,
		"balance_minor": balance, "version": 1, "created_at": schemaNow, "updated_at": schemaNow,
	}
}

func TestWalletsSchema(t *testing.T) {
	db := pgtest.AppDB(t)
	player := uuid.New()

	if err := db.Table("wallets").Create(walletRow(player, "BRL", 0)).Error; err != nil {
		t.Fatalf("valid wallet: %v", err)
	}
	if err := db.Table("wallets").Create(walletRow(player, "USD", 100)).Error; err != nil {
		t.Fatalf("same player, other currency: %v", err)
	}

	err := db.Table("wallets").Create(walletRow(player, "BRL", 100)).Error
	pgtest.RequirePgError(t, err, "23505", "wallets_player_currency_key")

	err = db.Table("wallets").Create(walletRow(uuid.New(), "BRL", -1)).Error
	pgtest.RequirePgError(t, err, "23514", "wallets_balance_minor_check")

	bad := walletRow(uuid.New(), "BRL", 0)
	bad["version"] = 0
	pgtest.RequirePgError(t, db.Table("wallets").Create(bad).Error, "23514", "wallets_version_check")

	bad = walletRow(uuid.New(), "brl", 0)
	pgtest.RequirePgError(t, db.Table("wallets").Create(bad).Error, "23514", "wallets_currency_check")

	err = db.Exec("DELETE FROM wallets WHERE player_id = ?", player).Error
	pgtest.RequirePgError(t, err, "42501", "")
}
