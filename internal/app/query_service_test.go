//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

func TestTransactionVisibility(t *testing.T) {
	h := apptest.New(t)
	q := app.NewQueryService(h.Deps)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
	bet := process(t, newWagering(t, h), cmd).Transaction
	ctx := context.Background()

	if got, err := q.Transaction(ctx, bet.ID(), app.Viewer{ProviderID: "provider-a"}); err != nil || got.ID() != bet.ID() {
		t.Fatalf("owner: %v", err)
	}
	if _, err := q.Transaction(ctx, bet.ID(), app.Viewer{ProviderID: "provider-b"}); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("another provider must get ErrNotFound: %v", err)
	}
	if _, err := q.Transaction(ctx, bet.ID(), app.Viewer{Internal: true}); err != nil {
		t.Fatalf("internal: %v", err)
	}
	entries, _ := h.Ledger.List(ctx, w.ID(), uuid.Nil, 1)
	openingID := entries[0].TransactionID()
	if _, err := q.Transaction(ctx, openingID, app.Viewer{ProviderID: "provider-a"}); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("providers never see the internal OPENING: %v", err)
	}
	if _, err := q.Transaction(ctx, openingID, app.Viewer{Internal: true}); err != nil {
		t.Fatalf("internal sees OPENING: %v", err)
	}
	if _, err := q.Transaction(ctx, uuid.New(), app.Viewer{Internal: true}); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}

	if got, err := q.TransactionByExternalID(ctx, "provider-a", cmd.ExternalTransactionID); err != nil || got.ID() != bet.ID() {
		t.Fatalf("by external id: %v", err)
	}
	if _, err := q.TransactionByExternalID(ctx, "provider-b", cmd.ExternalTransactionID); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("external ids are scoped by provider: %v", err)
	}
	if _, err := q.Wallet(ctx, w.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Wallet(ctx, uuid.New()); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing wallet: %v", err)
	}
}

func TestLedgerPagination(t *testing.T) {
	h := apptest.New(t)
	q := app.NewQueryService(h.Deps)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	for range 4 {
		process(t, svc, apptest.Command(t, w, "provider-a", "BET", "1.00", ""))
	}
	ctx := context.Background()

	var sizes []int
	var seen []uuid.UUID
	cursor := ""
	for {
		page, err := q.Ledger(ctx, w.ID(), cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(page.Entries))
		for _, e := range page.Entries {
			seen = append(seen, e.ID())
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(sizes) != 3 || sizes[0] != 2 || sizes[1] != 2 || sizes[2] != 1 || len(seen) != 5 {
		t.Fatalf("page sizes %v, seen %d", sizes, len(seen))
	}
	all, err := q.Ledger(ctx, w.ID(), "", 0)
	if err != nil || len(all.Entries) != 5 || all.NextCursor != "" {
		t.Fatalf("default limit page: %d entries next %q err %v", len(all.Entries), all.NextCursor, err)
	}
	for i, e := range all.Entries {
		if e.ID() != seen[i] {
			t.Fatal("pages must follow the same stable order")
		}
	}

	if _, err := q.Ledger(ctx, w.ID(), "", app.MaxLedgerLimit+1); !errors.Is(err, app.ErrInvalidLimit) {
		t.Fatalf("limit too large: %v", err)
	}
	if _, err := q.Ledger(ctx, w.ID(), "", -1); !errors.Is(err, app.ErrInvalidLimit) {
		t.Fatalf("negative limit: %v", err)
	}
	if _, err := q.Ledger(ctx, w.ID(), "not-a-cursor", 2); !errors.Is(err, app.ErrInvalidCursor) {
		t.Fatalf("bad cursor: %v", err)
	}
	if _, err := q.Ledger(ctx, uuid.New(), "", 2); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing wallet: %v", err)
	}
}
