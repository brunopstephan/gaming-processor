package app

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

// Ledger page sizes.
const (
	DefaultLedgerLimit = 50
	MaxLedgerLimit     = 200
)

// LedgerPage is one page of ledger entries ordered by id. NextCursor is
// empty on the last page.
type LedgerPage struct {
	Entries    []wallet.LedgerEntry
	NextCursor string
}

// QueryService answers read-only questions with per-provider visibility.
type QueryService struct {
	d Deps
}

// NewQueryService builds a QueryService.
func NewQueryService(d Deps) *QueryService { return &QueryService{d: d} }

// Wallet reads a wallet.
func (s *QueryService) Wallet(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error) {
	return s.d.Wallets.Get(ctx, id)
}

// Transaction reads a transaction the viewer may see. A transaction of
// another provider (or the internal OPENING, for providers) is reported as
// ErrNotFound so its existence is not disclosed.
func (s *QueryService) Transaction(ctx context.Context, id uuid.UUID, v Viewer) (*wagering.WagerTransaction, error) {
	t, err := s.d.Transactions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !v.canSee(t) {
		return nil, fmt.Errorf("%w: transaction %s", ErrNotFound, id)
	}
	return t, nil
}

// TransactionByExternalID reads providerID's transaction by its external id.
// The caller authorizes providerID.
func (s *QueryService) TransactionByExternalID(ctx context.Context, providerID, externalTransactionID string) (*wagering.WagerTransaction, error) {
	return s.d.Transactions.FindByExternalID(ctx, providerID, externalTransactionID)
}

// Ledger returns a page of the wallet's ledger. cursor is opaque (empty for
// the first page); limit 0 means DefaultLedgerLimit.
func (s *QueryService) Ledger(ctx context.Context, walletID uuid.UUID, cursor string, limit int) (LedgerPage, error) {
	if limit == 0 {
		limit = DefaultLedgerLimit
	}
	if limit < 1 || limit > MaxLedgerLimit {
		return LedgerPage{}, fmt.Errorf("%w: %d (1..%d)", ErrInvalidLimit, limit, MaxLedgerLimit)
	}
	after, err := decodeCursor(cursor)
	if err != nil {
		return LedgerPage{}, err
	}
	if _, err := s.d.Wallets.Get(ctx, walletID); err != nil {
		return LedgerPage{}, err
	}
	entries, err := s.d.Ledger.List(ctx, walletID, after, limit+1)
	if err != nil {
		return LedgerPage{}, err
	}
	page := LedgerPage{Entries: entries}
	if len(entries) > limit {
		page.Entries = entries[:limit]
		page.NextCursor = encodeCursor(entries[limit-1].ID())
	}
	return page, nil
}

// encodeCursor hides the last entry id behind base64url.
func encodeCursor(id uuid.UUID) string { return base64.RawURLEncoding.EncodeToString(id[:]) }

// decodeCursor parses a cursor; "" is the first page.
func decodeCursor(cursor string) (uuid.UUID, error) {
	if cursor == "" {
		return uuid.Nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) != len(uuid.UUID{}) {
		return uuid.Nil, ErrInvalidCursor
	}
	id, err := uuid.FromBytes(raw)
	if err != nil {
		return uuid.Nil, ErrInvalidCursor
	}
	return id, nil
}
