//go:build !unit && !e2e

package postgres

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

// seedWalletAndBet inserts a wallet and a processed BET row and returns the
// wallet, its player and the transaction ids.
func seedWalletAndBet(t *testing.T, db *gorm.DB) (walletID, playerID, txID uuid.UUID) {
	t.Helper()
	w := walletRow(uuid.New(), "BRL", 7500)
	if err := db.Table("wallets").Create(w).Error; err != nil {
		t.Fatal(err)
	}
	walletID, playerID = w["id"].(uuid.UUID), w["player_id"].(uuid.UUID)
	return walletID, playerID, seedBetOn(t, db, walletID, playerID)
}

// seedBetOn inserts another processed BET on an existing wallet.
func seedBetOn(t *testing.T, db *gorm.DB, walletID, playerID uuid.UUID) uuid.UUID {
	t.Helper()
	bet := processed(externalRow(walletID, playerID, "BET", 2500), 7500)
	if err := insertTx(db, bet); err != nil {
		t.Fatal(err)
	}
	return bet["id"].(uuid.UUID)
}

func ledgerRow(walletID, txID uuid.UUID, direction string, amount, before, after int64) map[string]any {
	return map[string]any{
		"id": uuid.New(), "wallet_id": walletID, "transaction_id": txID, "direction": direction,
		"amount_minor": amount, "currency": "BRL", "balance_before_minor": before,
		"balance_after_minor": after, "created_at": schemaNow,
	}
}

func TestLedgerConstraints(t *testing.T) {
	db := pgtest.AppDB(t)
	walletID, playerID, txID := seedWalletAndBet(t, db)

	entry := ledgerRow(walletID, txID, "DEBIT", 2500, 10000, 7500)
	if err := db.Table("wallet_ledger_entries").Create(entry).Error; err != nil {
		t.Fatalf("valid entry: %v", err)
	}
	dup := ledgerRow(walletID, txID, "DEBIT", 2500, 10000, 7500)
	pgtest.RequirePgError(t, db.Table("wallet_ledger_entries").Create(dup).Error,
		"23505", "wallet_ledger_entries_wallet_transaction_key")

	otherTx := seedBetOn(t, db, walletID, playerID)
	tests := []struct {
		name       string
		row        map[string]any
		constraint string
	}{
		{"wrong debit math", ledgerRow(walletID, otherTx, "DEBIT", 2500, 10000, 8000), "wallet_ledger_entries_balance_math"},
		{"credit math on debit", ledgerRow(walletID, otherTx, "DEBIT", 2500, 10000, 12500), "wallet_ledger_entries_balance_math"},
		{"zero amount", ledgerRow(walletID, otherTx, "CREDIT", 0, 100, 100), "wallet_ledger_entries_amount_minor_check"},
		{"negative after", ledgerRow(walletID, otherTx, "DEBIT", 200, 100, -100), "wallet_ledger_entries_balance_after_minor_check"},
		// An unknown direction also fails balance_math; the first violated
		// constraint reported is not guaranteed, so only the code is asserted.
		{"bad direction", ledgerRow(walletID, otherTx, "SIDEWAYS", 100, 0, 100), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgtest.RequirePgError(t, db.Table("wallet_ledger_entries").Create(tt.row).Error, "23514", tt.constraint)
		})
	}

	missing := ledgerRow(walletID, uuid.New(), "CREDIT", 100, 0, 100)
	pgtest.RequirePgError(t, db.Table("wallet_ledger_entries").Create(missing).Error,
		"23503", "wallet_ledger_entries_transaction_wallet_fkey")

	// An entry must belong to the wallet of its transaction.
	_, _, foreignTx := seedWalletAndBet(t, db)
	crossed := ledgerRow(walletID, foreignTx, "CREDIT", 100, 0, 100)
	pgtest.RequirePgError(t, db.Table("wallet_ledger_entries").Create(crossed).Error,
		"23503", "wallet_ledger_entries_transaction_wallet_fkey")

	badCurrency := ledgerRow(walletID, otherTx, "CREDIT", 100, 0, 100)
	badCurrency["currency"] = "brl"
	pgtest.RequirePgError(t, db.Table("wallet_ledger_entries").Create(badCurrency).Error,
		"23514", "wallet_ledger_entries_currency_check")
}

func TestLedgerIsAppendOnly(t *testing.T) {
	app := pgtest.AppDB(t)
	walletID, _, txID := seedWalletAndBet(t, app)
	entry := ledgerRow(walletID, txID, "DEBIT", 2500, 10000, 7500)
	if err := app.Table("wallet_ledger_entries").Create(entry).Error; err != nil {
		t.Fatal(err)
	}
	id := entry["id"]

	pgtest.RequirePgError(t, app.Exec("UPDATE wallet_ledger_entries SET amount_minor = 1 WHERE id = ?", id).Error, "42501", "")
	pgtest.RequirePgError(t, app.Exec("DELETE FROM wallet_ledger_entries WHERE id = ?", id).Error, "42501", "")
	pgtest.RequirePgError(t, app.Exec("TRUNCATE wallet_ledger_entries").Error, "42501", "")

	owner := pgtest.OwnerDB(t)
	for name, sql := range map[string]string{
		"owner update":   "UPDATE wallet_ledger_entries SET amount_minor = 1 WHERE id = ?",
		"owner delete":   "DELETE FROM wallet_ledger_entries WHERE id = ?",
		"owner truncate": "TRUNCATE wallet_ledger_entries",
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			if strings.Contains(sql, "?") {
				err = owner.Exec(sql, id).Error
			} else {
				err = owner.Exec(sql).Error
			}
			pgtest.RequirePgError(t, err, "P0001", "")
			if !strings.Contains(err.Error(), "append-only") {
				t.Fatalf("error = %v, want append-only message", err)
			}
		})
	}

	var count int64
	if err := app.Table("wallet_ledger_entries").Where("id = ?", id).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("entry must survive: count %d err %v", count, err)
	}
}
