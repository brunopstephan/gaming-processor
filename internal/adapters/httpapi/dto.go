package httpapi

import (
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

type moneyRequest struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type walletResponse struct {
	ID       string      `json:"id"`
	PlayerID string      `json:"playerId"`
	Balance  money.Money `json:"balance"`
	Version  int64       `json:"version"`
}

func newWalletResponse(w *wallet.Wallet) walletResponse {
	return walletResponse{ID: w.ID().String(), PlayerID: w.PlayerID().String(), Balance: w.Balance(), Version: w.Version()}
}

type ledgerEntryResponse struct {
	ID            string      `json:"id"`
	TransactionID string      `json:"transactionId"`
	Direction     string      `json:"direction"`
	Money         money.Money `json:"money"`
	BalanceBefore money.Money `json:"balanceBefore"`
	BalanceAfter  money.Money `json:"balanceAfter"`
	CreatedAt     string      `json:"createdAt"`
}

type ledgerPageResponse struct {
	Items      []ledgerEntryResponse `json:"items"`
	NextCursor string                `json:"nextCursor,omitempty"`
}

func newLedgerEntryResponse(e wallet.LedgerEntry) ledgerEntryResponse {
	return ledgerEntryResponse{
		ID: e.ID().String(), TransactionID: e.TransactionID().String(), Direction: string(e.Direction()),
		Money: e.Amount(), BalanceBefore: e.BalanceBefore(), BalanceAfter: e.BalanceAfter(),
		CreatedAt: formatTime(e.CreatedAt()),
	}
}

type reconciliationResponse struct {
	WalletID          string      `json:"walletId"`
	StoredBalance     money.Money `json:"storedBalance"`
	CalculatedBalance money.Money `json:"calculatedBalance"`
	Difference        money.Money `json:"difference"`
	Consistent        bool        `json:"consistent"`
	CheckedEntries    int64       `json:"checkedEntries"`
}

// formatTime renders RFC 3339 UTC with milliseconds, like the events.
func formatTime(t time.Time) string { return events.FormatTime(t) }

type transactionRequest struct {
	ProviderID                     string       `json:"providerId"`
	ExternalTransactionID          string       `json:"externalTransactionId"`
	PlayerID                       string       `json:"playerId"`
	WalletID                       string       `json:"walletId"`
	RoundID                        string       `json:"roundId"`
	GameID                         string       `json:"gameId"`
	Kind                           string       `json:"kind"`
	Money                          moneyRequest `json:"money"`
	ReferenceExternalTransactionID string       `json:"referenceExternalTransactionId"`
}

type processResponse struct {
	TransactionID    string       `json:"transactionId"`
	Status           string       `json:"status"`
	Balance          *money.Money `json:"balance,omitempty"`
	FailureCode      string       `json:"failureCode,omitempty"`
	Category         string       `json:"category,omitempty"`
	IdempotentReplay bool         `json:"idempotentReplay"`
}

func newProcessResponse(t *wagering.WagerTransaction, replay bool) processResponse {
	r := processResponse{TransactionID: t.ID().String(), Status: string(t.Status()), IdempotentReplay: replay}
	if t.Status() == wagering.StatusProcessed {
		b := t.ResultBalance()
		r.Balance = &b
	}
	if code := t.FailureCode(); code != "" {
		r.FailureCode, r.Category = string(code), string(code.Category())
	}
	return r
}

// statusCode maps an outcome to the HTTP status of the contract.
func statusCode(s wagering.Status) int {
	switch s {
	case wagering.StatusProcessed:
		return http.StatusOK
	case wagering.StatusRejected:
		return http.StatusUnprocessableEntity
	case wagering.StatusFailed:
		return http.StatusInternalServerError
	default: // PENDING_REFERENCE (and PENDING, never persisted)
		return http.StatusAccepted
	}
}

type transactionView struct {
	TransactionID                  string       `json:"transactionId"`
	Origin                         string       `json:"origin"`
	Kind                           string       `json:"kind"`
	Status                         string       `json:"status"`
	WalletID                       string       `json:"walletId"`
	PlayerID                       string       `json:"playerId"`
	Money                          money.Money  `json:"money"`
	ProviderID                     string       `json:"providerId,omitempty"`
	ExternalTransactionID          string       `json:"externalTransactionId,omitempty"`
	RoundID                        string       `json:"roundId,omitempty"`
	GameID                         string       `json:"gameId,omitempty"`
	ReferenceExternalTransactionID string       `json:"referenceExternalTransactionId,omitempty"`
	ReferenceTransactionID         string       `json:"referenceTransactionId,omitempty"`
	FailureCode                    string       `json:"failureCode,omitempty"`
	Category                       string       `json:"category,omitempty"`
	Balance                        *money.Money `json:"balance,omitempty"`
	Attempts                       int          `json:"attempts"`
	NextAttemptAt                  string       `json:"nextAttemptAt,omitempty"`
	CreatedAt                      string       `json:"createdAt"`
	UpdatedAt                      string       `json:"updatedAt"`
	ProcessedAt                    string       `json:"processedAt,omitempty"`
}

func newTransactionView(t *wagering.WagerTransaction) transactionView {
	s := t.State()
	v := transactionView{
		TransactionID: s.ID.String(), Origin: string(s.Origin), Kind: string(s.Kind), Status: string(s.Status),
		WalletID: s.WalletID.String(), PlayerID: s.PlayerID.String(), Money: s.Money,
		ProviderID: s.ProviderID, ExternalTransactionID: s.ExternalTransactionID, RoundID: s.RoundID, GameID: s.GameID,
		ReferenceExternalTransactionID: s.ReferenceExternalTransactionID, Attempts: s.Attempts,
		CreatedAt: formatTime(s.CreatedAt), UpdatedAt: formatTime(s.UpdatedAt),
	}
	if s.ReferenceTransactionID != uuid.Nil {
		v.ReferenceTransactionID = s.ReferenceTransactionID.String()
	}
	if s.FailureCode != "" {
		v.FailureCode, v.Category = string(s.FailureCode), string(s.FailureCode.Category())
	}
	if s.Status == wagering.StatusProcessed {
		b := s.ResultBalance
		v.Balance = &b
	}
	if !s.NextAttemptAt.IsZero() {
		v.NextAttemptAt = formatTime(s.NextAttemptAt)
	}
	if !s.ProcessedAt.IsZero() {
		v.ProcessedAt = formatTime(s.ProcessedAt)
	}
	return v
}
