//go:build !unit && !e2e

package app_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

func assertStatus(t *testing.T, res wagering.Status, code wagering.FailureCode, tx *wagering.WagerTransaction) {
	t.Helper()
	if tx.Status() != res || tx.FailureCode() != code {
		t.Fatalf("status %s code %q, want %s %q", tx.Status(), tx.FailureCode(), res, code)
	}
}

func TestRefundAndRollbackResolveTheirReference(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")

	betCmd := apptest.Command(t, w, "provider-a", "BET", "30.00", "")
	bet := process(t, svc, betCmd).Transaction
	refund := process(t, svc, apptest.Command(t, w, "provider-a", "REFUND", "30.00", betCmd.ExternalTransactionID)).Transaction
	assertStatus(t, wagering.StatusProcessed, "", refund)
	if refund.ReferenceTransactionID() != bet.ID() || balance(t, h, w.ID()) != "100.00" {
		t.Fatalf("refund reference %s balance %s", refund.ReferenceTransactionID(), balance(t, h, w.ID()))
	}

	rollbackOfBet := process(t, svc, apptest.Command(t, w, "provider-a", "ROLLBACK", "30.00", betCmd.ExternalTransactionID)).Transaction
	assertStatus(t, wagering.StatusRejected, wagering.FailureReferenceAlreadyReversed, rollbackOfBet)
	secondRefund := process(t, svc, apptest.Command(t, w, "provider-a", "REFUND", "30.00", betCmd.ExternalTransactionID)).Transaction
	assertStatus(t, wagering.StatusRejected, wagering.FailureReferenceAlreadyReversed, secondRefund)

	refundExt := refund.ExternalTransactionID()
	rollbackOfRefund := process(t, svc, apptest.Command(t, w, "provider-a", "ROLLBACK", "30.00", refundExt)).Transaction
	assertStatus(t, wagering.StatusProcessed, "", rollbackOfRefund)
	if balance(t, h, w.ID()) != "70.00" {
		t.Fatalf("after rolling back the refund balance %s, want 70.00", balance(t, h, w.ID()))
	}
}

func TestWinWithReferenceAndReversalWithoutFunds(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "10.00")

	betCmd := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
	process(t, svc, betCmd)
	winCmd := apptest.Command(t, w, "provider-a", "WIN", "50.00", betCmd.ExternalTransactionID)
	win := process(t, svc, winCmd).Transaction
	assertStatus(t, wagering.StatusProcessed, "", win)
	if win.ReferenceTransactionID() == uuid.Nil {
		t.Fatal("WIN must persist the resolved BET")
	}
	process(t, svc, apptest.Command(t, w, "provider-a", "BET", "45.00", ""))
	rollback := process(t, svc, apptest.Command(t, w, "provider-a", "ROLLBACK", "50.00", winCmd.ExternalTransactionID)).Transaction
	assertStatus(t, wagering.StatusRejected, wagering.FailureInsufficientFundsForReversal, rollback)
	if balance(t, h, w.ID()) != "5.00" {
		t.Fatalf("balance %s, want 5.00", balance(t, h, w.ID()))
	}
}

func TestReversalBeforeItsReferenceWaits(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	before := time.Now()

	refund := process(t, svc, apptest.Command(t, w, "provider-a", "REFUND", "30.00", "bet-not-yet")).Transaction
	assertStatus(t, wagering.StatusPendingReference, "", refund)
	stored, err := h.Transactions.Get(context.Background(), refund.ID())
	if err != nil || stored.Status() != wagering.StatusPendingReference || stored.Attempts() != 1 ||
		stored.NextAttemptAt().Before(before.Add(time.Second)) {
		t.Fatalf("persisted pending %+v err %v", stored, err)
	}
	if got := eventTypes(h.OutboxRows(t, w.ID())[2:]); !slices.Equal(got, []string{"WagerTransactionPendingReference"}) {
		t.Fatalf("pending events = %v", got)
	}
	if balance(t, h, w.ID()) != "100.00" {
		t.Fatal("a pending reversal must not move the wallet")
	}
}

func TestReferencesAreScopedAndMustMatch(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	other := h.OpenWallet(t, "100.00")

	betCmd := apptest.Command(t, w, "provider-a", "BET", "20.00", "")
	process(t, svc, betCmd)

	// provider-b cannot see provider-a's bet: the reference is "not found yet".
	foreign := process(t, svc, apptest.Command(t, w, "provider-b", "REFUND", "20.00", betCmd.ExternalTransactionID)).Transaction
	assertStatus(t, wagering.StatusPendingReference, "", foreign)

	// Same provider, other wallet: the reference does not match.
	mismatch := process(t, svc, apptest.Command(t, other, "provider-a", "REFUND", "20.00", betCmd.ExternalTransactionID)).Transaction
	assertStatus(t, wagering.StatusRejected, wagering.FailureReferenceMismatch, mismatch)
	if balance(t, h, other.ID()) != "100.00" || balance(t, h, w.ID()) != "80.00" {
		t.Fatal("rejected reversals must not move wallets")
	}
}
