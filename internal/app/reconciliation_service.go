package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

// Reconciliation compares the stored balance with the balance rebuilt from
// the ledger. Difference = Stored - Calculated.
type Reconciliation struct {
	WalletID       uuid.UUID
	Stored         money.Money
	Calculated     money.Money
	Difference     money.Money
	Consistent     bool
	CheckedEntries int64
}

// ReconciliationService checks wallets against their ledger. It never
// changes data: divergences are reported in the result, logs and metrics.
type ReconciliationService struct {
	d Deps
}

// NewReconciliationService builds a ReconciliationService.
func NewReconciliationService(d Deps) *ReconciliationService { return &ReconciliationService{d: d} }

// Reconcile reads the wallet and its ledger totals from one consistent,
// read-only snapshot.
func (s *ReconciliationService) Reconcile(ctx context.Context, walletID uuid.UUID) (Reconciliation, error) {
	var r Reconciliation
	err := s.d.Tx.WithinSnapshot(ctx, func(ctx context.Context) error {
		w, err := s.d.Wallets.Get(ctx, walletID)
		if err != nil {
			return err
		}
		net, count, err := s.d.Ledger.Totals(ctx, walletID)
		if err != nil {
			return err
		}
		calculated, err := money.FromMinor(net, w.Currency())
		if err != nil {
			return err
		}
		difference, err := w.Balance().Sub(calculated)
		if err != nil {
			return err
		}
		r = Reconciliation{
			WalletID: walletID, Stored: w.Balance(), Calculated: calculated, Difference: difference,
			Consistent: difference.IsZero(), CheckedEntries: count,
		}
		return nil
	})
	if err != nil {
		return Reconciliation{}, err
	}
	if !r.Consistent {
		s.d.Metrics.ReconciliationDivergence()
		s.d.Log.WarnContext(ctx, "wallet reconciliation divergence",
			"walletId", walletID.String(), "difference", r.Difference.String(), "checkedEntries", r.CheckedEntries)
	}
	return r, nil
}
