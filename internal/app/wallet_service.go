package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

// OpenWalletCommand opens a wallet for a player in the currency of InitialBalance.
type OpenWalletCommand struct {
	PlayerID       uuid.UUID
	InitialBalance money.Money
}

// ParseOpenWallet validates raw input and returns *wagering.InputError with
// every violation. Zero is a valid initial balance.
func ParseOpenWallet(playerID, amount, currency string) (OpenWalletCommand, error) {
	var violations []wagering.Violation
	add := func(code wagering.FailureCode, field, reason string) {
		violations = append(violations, wagering.Violation{Code: code, Field: field, Reason: reason})
	}
	var cmd OpenWalletCommand
	id, err := uuid.Parse(playerID)
	switch {
	case playerID == "":
		add(wagering.FailureMissingField, "playerId", "is required")
	case err != nil || id.String() != playerID || id == uuid.Nil:
		add(wagering.FailureInvalidID, "playerId", "must be a non-nil lowercase canonical UUID")
	default:
		cmd.PlayerID = id
	}
	if amount == "" {
		add(wagering.FailureMissingField, "initialBalance.amount", "is required")
	}
	if currency == "" {
		add(wagering.FailureMissingField, "initialBalance.currency", "is required")
	}
	if amount != "" && currency != "" {
		m, err := money.Parse(amount, currency)
		switch {
		case err == nil:
			cmd.InitialBalance = m
		case errors.Is(err, money.ErrUnsupportedCurrency):
			add(wagering.FailureUnsupportedCurrency, "initialBalance.currency", "must be an ISO 4217 currency with 2 decimals")
		default:
			add(wagering.FailureInvalidMoney, "initialBalance.amount", err.Error())
		}
	}
	if len(violations) > 0 {
		return OpenWalletCommand{}, &wagering.InputError{Violations: violations}
	}
	return cmd, nil
}

// WalletService opens wallets.
type WalletService struct {
	d Deps
}

// NewWalletService builds a WalletService.
func NewWalletService(d Deps) *WalletService { return &WalletService{d: d} }

// Open creates the wallet at version 1. A positive initial balance also
// commits, in the same transaction, the PROCESSED OPENING transaction, its
// credit entry and the WagerTransactionProcessed and WalletBalanceChanged
// outbox events. A second wallet for (player, currency) is
// ErrWalletAlreadyExists.
func (s *WalletService) Open(ctx context.Context, cmd OpenWalletCommand, meta Meta) (*wallet.Wallet, error) {
	meta = meta.normalized()
	now := s.d.Clock()
	openingID := newID()
	w, entry, err := wallet.Open(wallet.OpenParams{
		ID: newID(), PlayerID: cmd.PlayerID, InitialBalance: cmd.InitialBalance,
		OpeningTransactionID: openingID, LedgerEntryID: newID(), Now: now,
	})
	if err != nil {
		return nil, err
	}
	err = s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.d.Wallets.Create(ctx, w); err != nil {
			if errors.Is(err, ErrConflict) {
				return ErrWalletAlreadyExists
			}
			return err
		}
		if entry == nil {
			return nil
		}
		opening, err := wagering.NewOpening(openingID, w.ID(), w.PlayerID(), cmd.InitialBalance, now)
		if err != nil {
			return err
		}
		if err := opening.CompleteOpening(w, *entry, now); err != nil {
			return err
		}
		inserted, err := s.d.Transactions.Insert(ctx, opening)
		if err != nil {
			return err
		}
		if !inserted {
			return fmt.Errorf("app: OPENING of wallet %s already exists", w.ID())
		}
		if err := s.d.Ledger.Append(ctx, *entry); err != nil {
			return err
		}
		return publish(ctx, s.d.Outbox, now, opening.PullEvents(), meta)
	})
	if err != nil {
		return nil, err
	}
	s.d.Log.InfoContext(ctx, "wallet opened",
		"walletId", w.ID().String(), "correlationId", meta.CorrelationID)
	return w, nil
}
