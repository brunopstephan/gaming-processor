package postgres

import (
	"fmt"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

func walletToModel(w *wallet.Wallet) walletModel {
	return walletModel{
		ID:           w.ID(),
		PlayerID:     w.PlayerID(),
		Currency:     w.Currency().Code(),
		BalanceMinor: w.Balance().MinorUnits(),
		Version:      w.Version(),
		CreatedAt:    w.CreatedAt(),
		UpdatedAt:    w.UpdatedAt(),
	}
}

func walletFromModel(m walletModel) (*wallet.Wallet, error) {
	balance, err := moneyFrom(m.BalanceMinor, m.Currency)
	if err != nil {
		return nil, fmt.Errorf("postgres: wallet %s: %w", m.ID, err)
	}
	return wallet.Rehydrate(wallet.RehydrateParams{
		ID:        m.ID,
		PlayerID:  m.PlayerID,
		Balance:   balance,
		Version:   m.Version,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	})
}

func moneyFrom(minor int64, currency string) (money.Money, error) {
	cur, err := money.ParseCurrency(currency)
	if err != nil {
		return money.Money{}, err
	}
	return money.FromMinor(minor, cur)
}
