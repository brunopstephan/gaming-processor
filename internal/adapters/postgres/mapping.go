package postgres

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
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

func transactionToModel(t *wagering.WagerTransaction) transactionModel {
	s := t.State()
	m := transactionModel{
		ID:                             s.ID,
		Origin:                         string(s.Origin),
		Kind:                           string(s.Kind),
		Status:                         string(s.Status),
		WalletID:                       s.WalletID,
		PlayerID:                       s.PlayerID,
		AmountMinor:                    s.Money.MinorUnits(),
		Currency:                       s.Money.Currency().Code(),
		ProviderID:                     optString(s.ProviderID),
		ExternalTransactionID:          optString(s.ExternalTransactionID),
		IdempotencyKey:                 optString(s.IdempotencyKey),
		PayloadHash:                    optString(s.PayloadHash),
		RoundID:                        optString(s.RoundID),
		GameID:                         optString(s.GameID),
		ReferenceExternalTransactionID: optString(s.ReferenceExternalTransactionID),
		ReferenceKind:                  optString(string(s.ReferenceKind)),
		FailureCode:                    optString(string(s.FailureCode)),
		Attempts:                       s.Attempts,
		NextAttemptAt:                  optTime(s.NextAttemptAt),
		CreatedAt:                      s.CreatedAt,
		UpdatedAt:                      s.UpdatedAt,
		ProcessedAt:                    optTime(s.ProcessedAt),
	}
	if s.ReferenceTransactionID != uuid.Nil {
		ref := s.ReferenceTransactionID
		m.ReferenceTransactionID = &ref
	}
	if s.ResultBalance.IsValid() {
		minor := s.ResultBalance.MinorUnits()
		m.ResultBalanceMinor = &minor
	}
	return m
}

func transactionFromModel(m transactionModel) (*wagering.WagerTransaction, error) {
	amount, err := moneyFrom(m.AmountMinor, m.Currency)
	if err != nil {
		return nil, fmt.Errorf("postgres: transaction %s: %w", m.ID, err)
	}
	s := wagering.State{
		ID:                             m.ID,
		Origin:                         wagering.Origin(m.Origin),
		Kind:                           wagering.Kind(m.Kind),
		Status:                         wagering.Status(m.Status),
		WalletID:                       m.WalletID,
		PlayerID:                       m.PlayerID,
		Money:                          amount,
		ProviderID:                     deref(m.ProviderID),
		ExternalTransactionID:          deref(m.ExternalTransactionID),
		IdempotencyKey:                 deref(m.IdempotencyKey),
		PayloadHash:                    deref(m.PayloadHash),
		RoundID:                        deref(m.RoundID),
		GameID:                         deref(m.GameID),
		ReferenceExternalTransactionID: deref(m.ReferenceExternalTransactionID),
		ReferenceKind:                  wagering.Kind(deref(m.ReferenceKind)),
		FailureCode:                    wagering.FailureCode(deref(m.FailureCode)),
		Attempts:                       m.Attempts,
		CreatedAt:                      m.CreatedAt,
		UpdatedAt:                      m.UpdatedAt,
	}
	if m.ReferenceTransactionID != nil {
		s.ReferenceTransactionID = *m.ReferenceTransactionID
	}
	if m.ResultBalanceMinor != nil {
		if s.ResultBalance, err = moneyFrom(*m.ResultBalanceMinor, m.Currency); err != nil {
			return nil, fmt.Errorf("postgres: transaction %s result balance: %w", m.ID, err)
		}
	}
	if m.NextAttemptAt != nil {
		s.NextAttemptAt = *m.NextAttemptAt
	}
	if m.ProcessedAt != nil {
		s.ProcessedAt = *m.ProcessedAt
	}
	t, err := wagering.Rehydrate(s)
	if err != nil {
		return nil, fmt.Errorf("postgres: transaction %s: %w", m.ID, err)
	}
	return t, nil
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func optTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
