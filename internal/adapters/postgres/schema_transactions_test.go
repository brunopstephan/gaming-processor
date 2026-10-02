//go:build !unit && !e2e

package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

// externalRow is a valid PENDING external operation.
func externalRow(walletID, playerID uuid.UUID, kind string, amount int64) map[string]any {
	ext := uuid.NewString()
	return map[string]any{
		"id": uuid.New(), "origin": "EXTERNAL", "kind": kind, "status": "PENDING",
		"wallet_id": walletID, "player_id": playerID, "amount_minor": amount, "currency": "BRL",
		"provider_id": "provider-a", "external_transaction_id": ext, "idempotency_key": "provider-a:" + ext,
		"payload_hash": strings.Repeat("a", 64), "round_id": "round-1", "game_id": "game-1",
		"attempts": 0, "created_at": schemaNow, "updated_at": schemaNow,
	}
}

func processed(row map[string]any, balance int64) map[string]any {
	row["status"] = "PROCESSED"
	row["result_balance_minor"] = balance
	row["processed_at"] = schemaNow
	return row
}

func reversal(walletID, playerID uuid.UUID, kind string, ref map[string]any) map[string]any {
	row := externalRow(walletID, playerID, kind, ref["amount_minor"].(int64))
	row["reference_external_transaction_id"] = ref["external_transaction_id"]
	row["reference_transaction_id"] = ref["id"]
	row["reference_kind"] = ref["kind"]
	return row
}

func insertTx(db *gorm.DB, row map[string]any) error {
	return db.Table("wager_transactions").Create(row).Error
}

func TestTransactionShapeConstraints(t *testing.T) {
	db := pgtest.AppDB(t)
	w, p := uuid.New(), uuid.New()

	if err := insertTx(db, processed(externalRow(w, p, "BET", 2500), 7500)); err != nil {
		t.Fatalf("valid processed bet: %v", err)
	}
	opening := map[string]any{
		"id": uuid.New(), "origin": "INTERNAL", "kind": "OPENING", "status": "PROCESSED",
		"wallet_id": w, "player_id": p, "amount_minor": int64(10000), "currency": "BRL",
		"result_balance_minor": int64(10000), "attempts": 0,
		"created_at": schemaNow, "updated_at": schemaNow, "processed_at": schemaNow,
	}
	if err := insertTx(db, opening); err != nil {
		t.Fatalf("valid opening: %v", err)
	}

	refBet := processed(externalRow(w, p, "BET", 400), 100)
	if err := insertTx(db, refBet); err != nil {
		t.Fatalf("reference bet: %v", err)
	}

	tests := []struct {
		name       string
		row        func() map[string]any
		code       string
		constraint string
	}{
		{"processed without result balance", func() map[string]any {
			r := processed(externalRow(w, p, "BET", 100), 0)
			delete(r, "result_balance_minor")
			return r
		}, "23514", "wager_transactions_status_shape"},
		{"rejected without failure code", func() map[string]any {
			r := externalRow(w, p, "BET", 100)
			r["status"], r["processed_at"] = "REJECTED", schemaNow
			return r
		}, "23514", "wager_transactions_status_shape"},
		{"pending reference without next attempt", func() map[string]any {
			r := externalRow(w, p, "REFUND", 100)
			r["reference_external_transaction_id"] = "x"
			r["status"] = "PENDING_REFERENCE"
			return r
		}, "23514", "wager_transactions_status_shape"},
		{"failed with business code", func() map[string]any {
			r := externalRow(w, p, "BET", 100)
			r["status"], r["failure_code"], r["processed_at"] = "FAILED", "INSUFFICIENT_FUNDS", schemaNow
			return r
		}, "23514", "wager_transactions_failed_code"},
		{"external without provider", func() map[string]any {
			r := externalRow(w, p, "BET", 100)
			delete(r, "provider_id")
			return r
		}, "23514", "wager_transactions_origin_shape"},
		{"internal with provider", func() map[string]any {
			r := externalRow(uuid.New(), p, "OPENING", 100)
			r["origin"] = "INTERNAL"
			return r
		}, "23514", "wager_transactions_origin_shape"},
		{"external opening", func() map[string]any {
			return externalRow(w, p, "OPENING", 100)
		}, "23514", "wager_transactions_origin_shape"},
		{"loss with amount", func() map[string]any {
			return externalRow(w, p, "LOSS", 100)
		}, "23514", "wager_transactions_kind_amount"},
		{"bet with zero", func() map[string]any {
			return externalRow(w, p, "BET", 0)
		}, "23514", "wager_transactions_kind_amount"},
		{"refund without reference", func() map[string]any {
			return externalRow(w, p, "REFUND", 100)
		}, "23514", "wager_transactions_reversal_reference"},
		{"processed refund unresolved", func() map[string]any {
			r := processed(externalRow(w, p, "REFUND", 100), 100)
			r["reference_external_transaction_id"] = "x"
			return r
		}, "23514", "wager_transactions_processed_reversal_resolved"},
		{"processed WIN with unresolved reference", func() map[string]any {
			r := processed(externalRow(w, p, "WIN", 100), 100)
			r["reference_external_transaction_id"] = "x"
			return r
		}, "23514", "wager_transactions_processed_reversal_resolved"},
		{"rejected with infrastructure code", func() map[string]any {
			r := externalRow(w, p, "BET", 100)
			r["status"], r["failure_code"], r["processed_at"] = "REJECTED", "INFRASTRUCTURE_FAILURE", schemaNow
			return r
		}, "23514", "wager_transactions_rejected_code"},
		{"bet with reference", func() map[string]any {
			r := externalRow(w, p, "BET", 100)
			r["reference_external_transaction_id"] = "x"
			return r
		}, "23514", "wager_transactions_reference_not_allowed"},
		{"reference kind differs from referenced row", func() map[string]any {
			r := reversal(w, p, "REFUND", refBet)
			r["reference_kind"] = "WIN"
			return r
		}, "23503", "wager_transactions_reference_fkey"},
		{"reference id without reference kind", func() map[string]any {
			r := reversal(w, p, "REFUND", refBet)
			delete(r, "reference_kind")
			return r
		}, "23503", "wager_transactions_reference_fkey"},
		// Also fails status_shape; which violation is reported first is not
		// guaranteed, so only the code is asserted.
		{"unknown status", func() map[string]any {
			r := externalRow(w, p, "BET", 100)
			r["status"] = "DONE"
			return r
		}, "23514", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgtest.RequirePgError(t, insertTx(db, tt.row()), tt.code, tt.constraint)
		})
	}
}

func TestTransactionUniqueness(t *testing.T) {
	db := pgtest.AppDB(t)
	w, p := uuid.New(), uuid.New()

	base := externalRow(w, p, "BET", 100)
	if err := insertTx(db, base); err != nil {
		t.Fatal(err)
	}
	sameKey := externalRow(w, p, "BET", 100)
	sameKey["idempotency_key"] = base["idempotency_key"]
	pgtest.RequirePgError(t, insertTx(db, sameKey), "23505", "wager_transactions_provider_idempotency_key")

	sameExt := externalRow(w, p, "BET", 100)
	sameExt["external_transaction_id"] = base["external_transaction_id"]
	pgtest.RequirePgError(t, insertTx(db, sameExt), "23505", "wager_transactions_provider_external_id")

	otherProvider := externalRow(w, p, "BET", 100)
	otherProvider["provider_id"] = "provider-b"
	otherProvider["idempotency_key"] = base["idempotency_key"]
	otherProvider["external_transaction_id"] = base["external_transaction_id"]
	if err := insertTx(db, otherProvider); err != nil {
		t.Fatalf("keys are scoped by provider: %v", err)
	}

	open := func() map[string]any {
		return map[string]any{
			"id": uuid.New(), "origin": "INTERNAL", "kind": "OPENING", "status": "PROCESSED",
			"wallet_id": w, "player_id": p, "amount_minor": int64(100), "currency": "BRL",
			"result_balance_minor": int64(100), "attempts": 0,
			"created_at": schemaNow, "updated_at": schemaNow, "processed_at": schemaNow,
		}
	}
	if err := insertTx(db, open()); err != nil {
		t.Fatal(err)
	}
	pgtest.RequirePgError(t, insertTx(db, open()), "23505", "wager_transactions_one_opening")
}

func TestReversalUniqueness(t *testing.T) {
	db := pgtest.AppDB(t)
	w, p := uuid.New(), uuid.New()

	bet := processed(externalRow(w, p, "BET", 3000), 7000)
	win := processed(externalRow(w, p, "WIN", 5000), 12000)
	for _, r := range []map[string]any{bet, win} {
		if err := insertTx(db, r); err != nil {
			t.Fatal(err)
		}
	}

	rejected := reversal(w, p, "REFUND", bet)
	rejected["status"], rejected["failure_code"], rejected["processed_at"] = "REJECTED", "AMOUNT_MISMATCH", schemaNow
	if err := insertTx(db, rejected); err != nil {
		t.Fatalf("rejected reversals do not count: %v", err)
	}
	firstRefund := processed(reversal(w, p, "REFUND", bet), 10000)
	if err := insertTx(db, firstRefund); err != nil {
		t.Fatalf("first refund: %v", err)
	}
	if err := insertTx(db, processed(reversal(w, p, "ROLLBACK", firstRefund), 7000)); err != nil {
		t.Fatalf("rollback of a processed refund is allowed: %v", err)
	}
	err := insertTx(db, processed(reversal(w, p, "ROLLBACK", bet), 13000))
	pgtest.RequirePgError(t, err, "23505", "wager_transactions_one_reversal_per_bet")
	// Same kind violates both unique indexes; only the code is asserted.
	err = insertTx(db, processed(reversal(w, p, "REFUND", bet), 13000))
	pgtest.RequirePgError(t, err, "23505", "")

	if err := insertTx(db, processed(reversal(w, p, "ROLLBACK", win), 5000)); err != nil {
		t.Fatalf("first rollback of win: %v", err)
	}
	err = insertTx(db, processed(reversal(w, p, "ROLLBACK", win), 0))
	pgtest.RequirePgError(t, err, "23505", "wager_transactions_one_reversal_per_kind")
}

func TestTransactionUpdateGuard(t *testing.T) {
	db := pgtest.AppDB(t)
	w, p := uuid.New(), uuid.New()
	update := func(id any, values map[string]any) error {
		return db.Table("wager_transactions").Where("id = ?", id).Updates(values).Error
	}

	pending := externalRow(w, p, "REFUND", 100)
	pending["reference_external_transaction_id"] = "later"
	if err := insertTx(db, pending); err != nil {
		t.Fatal(err)
	}
	if err := update(pending["id"], map[string]any{"status": "PENDING_REFERENCE", "next_attempt_at": schemaNow, "attempts": 1}); err != nil {
		t.Fatalf("PENDING -> PENDING_REFERENCE: %v", err)
	}
	err := update(pending["id"], map[string]any{"status": "PENDING"})
	pgtest.RequirePgError(t, err, "23514", "")

	err = update(pending["id"], map[string]any{"amount_minor": int64(999)})
	pgtest.RequirePgError(t, err, "23514", "")

	// Positive transitions out of PENDING_REFERENCE.
	target := processed(externalRow(w, p, "BET", 100), 0)
	if err := insertTx(db, target); err != nil {
		t.Fatal(err)
	}
	pendingRef := func() map[string]any {
		r := externalRow(w, p, "REFUND", 100)
		r["reference_external_transaction_id"] = target["external_transaction_id"]
		r["status"], r["next_attempt_at"], r["attempts"] = "PENDING_REFERENCE", schemaNow, 1
		if err := insertTx(db, r); err != nil {
			t.Fatal(err)
		}
		return r
	}
	retry := pendingRef()
	if err := update(retry["id"], map[string]any{"attempts": 2, "next_attempt_at": schemaNow.Add(time.Minute)}); err != nil {
		t.Fatalf("same-status retry: %v", err)
	}
	resolved := pendingRef()
	if err := update(resolved["id"], map[string]any{
		"status": "PROCESSED", "reference_transaction_id": target["id"], "reference_kind": "BET",
		"result_balance_minor": int64(100), "processed_at": schemaNow, "next_attempt_at": nil,
	}); err != nil {
		t.Fatalf("PENDING_REFERENCE -> PROCESSED: %v", err)
	}
	rejected := pendingRef()
	if err := update(rejected["id"], map[string]any{
		"status": "REJECTED", "failure_code": "REFERENCE_NOT_FOUND", "processed_at": schemaNow,
	}); err != nil {
		t.Fatalf("PENDING_REFERENCE -> REJECTED: %v", err)
	}
	failed := pendingRef()
	if err := update(failed["id"], map[string]any{
		"status": "FAILED", "failure_code": "INFRASTRUCTURE_FAILURE", "processed_at": schemaNow,
	}); err != nil {
		t.Fatalf("PENDING_REFERENCE -> FAILED: %v", err)
	}

	done := processed(externalRow(w, p, "BET", 100), 0)
	if err := insertTx(db, done); err != nil {
		t.Fatal(err)
	}
	err = update(done["id"], map[string]any{"attempts": 5})
	pgtest.RequirePgError(t, err, "23514", "")

	err = db.Exec("DELETE FROM wager_transactions WHERE id = ?", done["id"]).Error
	pgtest.RequirePgError(t, err, "42501", "")
}
