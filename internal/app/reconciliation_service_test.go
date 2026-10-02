//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func TestReconcileConsistentWallet(t *testing.T) {
	h := apptest.New(t)
	w := h.OpenWallet(t, "1000.00")
	process(t, newWagering(t, h), apptest.Command(t, w, "provider-a", "BET", "25.00", ""))

	r, err := app.NewReconciliationService(h.Deps).Reconcile(context.Background(), w.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Consistent || r.Stored.Amount() != "975.00" || r.Calculated.Amount() != "975.00" ||
		r.Difference.Amount() != "0.00" || r.CheckedEntries != 2 || r.WalletID != w.ID() {
		t.Fatalf("reconciliation = %+v", r)
	}
	if h.Metrics.Count("divergence") != 0 {
		t.Fatal("no divergence expected")
	}
}

func TestReconcileReportsDivergenceWithoutFixingIt(t *testing.T) {
	h := apptest.New(t)
	w := h.OpenWallet(t, "100.00")
	// Simulate corruption the application could never produce.
	if err := pgtest.OwnerDB(t).Exec("UPDATE wallets SET balance_minor = balance_minor + 100 WHERE id = ?", w.ID()).Error; err != nil {
		t.Fatal(err)
	}
	r, err := app.NewReconciliationService(h.Deps).Reconcile(context.Background(), w.ID())
	if err != nil {
		t.Fatal(err)
	}
	if r.Consistent || r.Stored.Amount() != "101.00" || r.Calculated.Amount() != "100.00" || r.Difference.Amount() != "1.00" {
		t.Fatalf("reconciliation = %+v", r)
	}
	if h.Metrics.Count("divergence") != 1 {
		t.Fatal("divergence metric not recorded")
	}
	if balance(t, h, w.ID()) != "101.00" {
		t.Fatal("reconciliation must not change the stored balance")
	}
}

func TestReconcileEmptyAndMissingWallets(t *testing.T) {
	h := apptest.New(t)
	empty := h.OpenWallet(t, "0.00")
	svc := app.NewReconciliationService(h.Deps)
	r, err := svc.Reconcile(context.Background(), empty.ID())
	if err != nil || !r.Consistent || r.CheckedEntries != 0 || !r.Calculated.IsZero() {
		t.Fatalf("empty wallet = %+v, %v", r, err)
	}
	if _, err := svc.Reconcile(context.Background(), uuid.New()); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing wallet error = %v", err)
	}
}
