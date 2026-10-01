//go:build !integration && !e2e

package wagering

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

func TestProcessRejectsInvalidReferenceSnapshot(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *Reference)
	}{
		{"empty status", func(r *Reference) { r.Status = "" }},
		{"unknown status", func(r *Reference) { r.Status = "DONE" }},
		{"nil id", func(r *Reference) { r.ID = uuid.Nil }},
		{"invalid kind", func(r *Reference) { r.Kind = "JACKPOT" }},
		{"empty kind", func(r *Reference) { r.Kind = "" }},
		{"invalid money", func(r *Reference) { r.Money = money.Money{} }},
		{"nil wallet", func(r *Reference) { r.WalletID = uuid.Nil }},
		{"nil player", func(r *Reference) { r.PlayerID = uuid.Nil }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "100.00")
			bet := f.processed(KindBet, "10.00", nil)
			ref := bet
			tt.mutate(&ref)
			tx := f.op(KindRefund, "10.00", "ref")
			before := tx.State()
			balance, version := f.wallet.Balance(), f.wallet.Version()
			entry, err := tx.Process(f.input(&ref, false))
			if !errors.Is(err, ErrInvalidTransaction) || entry != nil {
				t.Fatalf("entry %v error = %v, want ErrInvalidTransaction", entry, err)
			}
			if !reflect.DeepEqual(tx.State(), before) || len(tx.PullEvents()) != 0 {
				t.Fatal("invalid snapshot changed the transaction")
			}
			if !f.wallet.Balance().Equal(balance) || f.wallet.Version() != version {
				t.Fatal("invalid snapshot moved the wallet")
			}
		})
	}
}

func TestProcessInvalidReferenceBeatsWalletRejection(t *testing.T) {
	f := newFixture(t, "100.00")
	ref := f.processed(KindBet, "10.00", nil)
	ref.Status = ""
	tx := f.op(KindRefund, "10.00", "ref")
	in := f.input(&ref, false)
	in.Wallet = nil
	if _, err := tx.Process(in); !errors.Is(err, ErrInvalidTransaction) || tx.Status() != StatusPending {
		t.Fatalf("status %s error = %v", tx.Status(), err)
	}
}

func TestRehydrateProcessedReversalNeedsResolvedReference(t *testing.T) {
	f := newFixture(t, "100.00")
	bet := f.processed(KindBet, "10.00", nil)
	tx := f.op(KindRefund, "10.00", "ref")
	if _, err := tx.Process(f.input(&bet, false)); err != nil {
		t.Fatal(err)
	}
	good := tx.State()
	if _, err := Rehydrate(good); err != nil {
		t.Fatalf("valid processed refund: %v", err)
	}
	for name, mutate := range map[string]func(s *State){
		"nil reference id": func(s *State) { s.ReferenceTransactionID = uuid.Nil },
		"invalid ref kind": func(s *State) { s.ReferenceKind = "" },
		"unknown ref kind": func(s *State) { s.ReferenceKind = "JACKPOT" },
		"rollback variant": func(s *State) { s.Kind = KindRollback; s.ReferenceTransactionID = uuid.Nil },
	} {
		t.Run(name, func(t *testing.T) {
			s := good
			mutate(&s)
			if _, err := Rehydrate(s); !errors.Is(err, ErrInvalidTransaction) {
				t.Fatalf("error = %v, want ErrInvalidTransaction", err)
			}
		})
	}

	win := f.op(KindWin, "5.00", "ref")
	if _, err := win.Process(f.input(&bet, false)); err != nil {
		t.Fatal(err)
	}
	s := win.State()
	s.ReferenceTransactionID = uuid.Nil
	if _, err := Rehydrate(s); !errors.Is(err, ErrInvalidTransaction) {
		t.Fatalf("processed WIN with reference text and nil id: error = %v", err)
	}
	// A WIN without a reference needs no resolved id.
	plain := f.op(KindWin, "5.00", "")
	if _, err := plain.Process(f.input(nil, false)); err != nil {
		t.Fatal(err)
	}
	if _, err := Rehydrate(plain.State()); err != nil {
		t.Fatalf("plain WIN: %v", err)
	}
}

func TestZeroTimeRejected(t *testing.T) {
	t.Run("Process", func(t *testing.T) {
		f := newFixture(t, "100.00")
		tx := f.op(KindBet, "10.00", "")
		before := tx.State()
		in := f.input(nil, false)
		in.Now = time.Time{}
		if _, err := tx.Process(in); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("error = %v", err)
		}
		if !reflect.DeepEqual(tx.State(), before) || f.wallet.Version() != wallet.InitialVersion {
			t.Fatal("zero time mutated state")
		}
	})
	t.Run("MarkFailed", func(t *testing.T) {
		f := newFixture(t, "100.00")
		tx := f.op(KindBet, "10.00", "")
		before := tx.State()
		if err := tx.MarkFailed(time.Time{}); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("error = %v", err)
		}
		if !reflect.DeepEqual(tx.State(), before) {
			t.Fatal("zero time mutated state")
		}
	})
	t.Run("CompleteOpening", func(t *testing.T) {
		tx, w, entry := openingFixture(t, "10.00")
		before := tx.State()
		if err := tx.CompleteOpening(w, entry, time.Time{}); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("error = %v", err)
		}
		if !reflect.DeepEqual(tx.State(), before) {
			t.Fatal("zero time mutated state")
		}
	})
}

func openingFixture(t *testing.T, amount string) (*WagerTransaction, *wallet.Wallet, wallet.LedgerEntry) {
	t.Helper()
	walletID, playerID, txID := newID(t), newID(t), newID(t)
	initial := brl(t, amount)
	w, entry, err := wallet.Open(wallet.OpenParams{
		ID: walletID, PlayerID: playerID, InitialBalance: initial,
		OpeningTransactionID: txID, LedgerEntryID: newID(t), Now: testNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := NewOpening(txID, walletID, playerID, initial, testNow)
	if err != nil {
		t.Fatal(err)
	}
	return tx, w, *entry
}

func TestCompleteOpeningGuards(t *testing.T) {
	assertNoMutation := func(t *testing.T, tx *WagerTransaction, before State) {
		t.Helper()
		if !reflect.DeepEqual(tx.State(), before) || len(tx.PullEvents()) != 0 {
			t.Fatal("failed CompleteOpening mutated the transaction")
		}
	}
	t.Run("nil wallet", func(t *testing.T) {
		tx, _, entry := openingFixture(t, "10.00")
		before := tx.State()
		if err := tx.CompleteOpening(nil, entry, testNow); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("error = %v", err)
		}
		assertNoMutation(t, tx, before)
	})
	t.Run("wrong amount", func(t *testing.T) {
		tx, w, entry := openingFixture(t, "10.00")
		other, err := wallet.NewLedgerEntry(wallet.LedgerEntryParams{
			ID: newID(t), WalletID: entry.WalletID(), TransactionID: entry.TransactionID(),
			Direction: wallet.DirectionCredit, Amount: brl(t, "9.00"),
			BalanceBefore: brl(t, "0.00"), BalanceAfter: brl(t, "9.00"), CreatedAt: testNow,
		})
		if err != nil {
			t.Fatal(err)
		}
		before := tx.State()
		if err := tx.CompleteOpening(w, other, testNow); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("error = %v", err)
		}
		assertNoMutation(t, tx, before)
	})
	t.Run("debit entry", func(t *testing.T) {
		tx, w, entry := openingFixture(t, "10.00")
		debit, err := wallet.NewLedgerEntry(wallet.LedgerEntryParams{
			ID: newID(t), WalletID: entry.WalletID(), TransactionID: entry.TransactionID(),
			Direction: wallet.DirectionDebit, Amount: brl(t, "10.00"),
			BalanceBefore: brl(t, "10.00"), BalanceAfter: brl(t, "0.00"), CreatedAt: testNow,
		})
		if err != nil {
			t.Fatal(err)
		}
		before := tx.State()
		if err := tx.CompleteOpening(w, debit, testNow); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("error = %v", err)
		}
		assertNoMutation(t, tx, before)
	})
	t.Run("non-zero balance before", func(t *testing.T) {
		tx, w, entry := openingFixture(t, "10.00")
		e, err := wallet.NewLedgerEntry(wallet.LedgerEntryParams{
			ID: newID(t), WalletID: entry.WalletID(), TransactionID: entry.TransactionID(),
			Direction: wallet.DirectionCredit, Amount: brl(t, "10.00"),
			BalanceBefore: brl(t, "5.00"), BalanceAfter: brl(t, "15.00"), CreatedAt: testNow,
		})
		if err != nil {
			t.Fatal(err)
		}
		before := tx.State()
		if err := tx.CompleteOpening(w, e, testNow); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("error = %v", err)
		}
		assertNoMutation(t, tx, before)
	})
	t.Run("wallet already moved", func(t *testing.T) {
		tx, w, entry := openingFixture(t, "10.00")
		if _, err := w.Credit(newID(t), newID(t), brl(t, "1.00"), testNow); err != nil {
			t.Fatal(err)
		}
		before := tx.State()
		if err := tx.CompleteOpening(w, entry, testNow); !errors.Is(err, ErrInvalidTransaction) {
			t.Fatalf("error = %v", err)
		}
		assertNoMutation(t, tx, before)
	})
	t.Run("non-opening transaction", func(t *testing.T) {
		f := newFixture(t, "10.00")
		tx := f.op(KindBet, "1.00", "")
		_, w, entry := openingFixture(t, "10.00")
		if err := tx.CompleteOpening(w, entry, testNow); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("error = %v, want ErrInvalidTransition", err)
		}
	})
	t.Run("second completion", func(t *testing.T) {
		tx, w, entry := openingFixture(t, "10.00")
		if err := tx.CompleteOpening(w, entry, testNow); err != nil {
			t.Fatal(err)
		}
		tx.PullEvents()
		before := tx.State()
		if err := tx.CompleteOpening(w, entry, testNow); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("error = %v, want ErrInvalidTransition", err)
		}
		assertNoMutation(t, tx, before)
	})
}

func TestKindPolicyInConstructorAndRehydrate(t *testing.T) {
	base, err := ParseCommand(validRaw())
	if err != nil {
		t.Fatal(err)
	}
	cmds := map[string]func(c *Command){
		"LOSS with amount":    func(c *Command) { c.Kind = KindLoss },
		"REFUND no reference": func(c *Command) { c.Kind = KindRefund },
		"ROLLBACK no ref":     func(c *Command) { c.Kind = KindRollback },
		"BET with reference":  func(c *Command) { c.ReferenceExternalTransactionID = "x" },
		"LOSS with reference": func(c *Command) {
			c.Kind, c.Money, c.ReferenceExternalTransactionID = KindLoss, brl(t, "0.00"), "x"
		},
		"WIN zero amount": func(c *Command) { c.Kind, c.Money = KindWin, brl(t, "0.00") },
	}
	for name, mutate := range cmds {
		t.Run("NewExternal "+name, func(t *testing.T) {
			c := base
			mutate(&c)
			if _, err := NewExternal(newID(t), c, testNow); !errors.Is(err, ErrInvalidTransaction) {
				t.Fatalf("error = %v, want ErrInvalidTransaction", err)
			}
		})
		t.Run("Rehydrate "+name, func(t *testing.T) {
			tx, err := NewExternal(newID(t), base, testNow)
			if err != nil {
				t.Fatal(err)
			}
			s := tx.State()
			c := Command{Kind: s.Kind, Money: s.Money, ReferenceExternalTransactionID: s.ReferenceExternalTransactionID}
			mutate(&c)
			s.Kind, s.Money, s.ReferenceExternalTransactionID = c.Kind, c.Money, c.ReferenceExternalTransactionID
			if _, err := Rehydrate(s); !errors.Is(err, ErrInvalidTransaction) {
				t.Fatalf("error = %v, want ErrInvalidTransaction", err)
			}
		})
	}
}

func TestRehydrateNormalizesTimesToUTC(t *testing.T) {
	f := newFixture(t, "100.00")
	tx := f.op(KindBet, "10.00", "")
	if _, err := tx.Process(f.input(nil, false)); err != nil {
		t.Fatal(err)
	}
	zone := time.FixedZone("BRT", -3*3600)
	s := tx.State()
	s.CreatedAt, s.UpdatedAt, s.ProcessedAt = s.CreatedAt.In(zone), s.UpdatedAt.In(zone), s.ProcessedAt.In(zone)
	got, err := Rehydrate(s)
	if err != nil {
		t.Fatal(err)
	}
	gs := got.State()
	for name, ts := range map[string]time.Time{"created": gs.CreatedAt, "updated": gs.UpdatedAt, "processed": gs.ProcessedAt} {
		if ts.Location() != time.UTC || ts.IsZero() {
			t.Errorf("%s location = %v", name, ts.Location())
		}
	}

	pending := f.op(KindRefund, "10.00", "ref")
	if _, err := pending.Process(f.input(nil, false)); err != nil {
		t.Fatal(err)
	}
	ps := pending.State()
	ps.NextAttemptAt = ps.NextAttemptAt.In(zone)
	got, err = Rehydrate(ps)
	if err != nil {
		t.Fatal(err)
	}
	if next := got.State().NextAttemptAt; next.Location() != time.UTC || next.IsZero() {
		t.Fatalf("nextAttemptAt location = %v", next.Location())
	}
}
