package wagering

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

// Reference is the snapshot of a referenced transaction, resolved by the
// caller through (providerId, referenceExternalTransactionId).
type Reference struct {
	ID       uuid.UUID
	Kind     Kind
	Status   Status
	WalletID uuid.UUID
	PlayerID uuid.UUID
	RoundID  string
	Money    money.Money
}

// AsReference returns t as a Reference for operations that point to it.
func (t *WagerTransaction) AsReference() Reference {
	return Reference{
		ID:       t.id,
		Kind:     t.kind,
		Status:   t.status,
		WalletID: t.walletID,
		PlayerID: t.playerID,
		RoundID:  t.roundID,
		Money:    t.money,
	}
}

// ProcessInput is what Process needs from the caller, which must hold the
// wallet row lock inside the SQL transaction.
type ProcessInput struct {
	// Wallet is the locked wallet, or nil when it does not exist.
	Wallet *wallet.Wallet
	// Reference is the resolved reference, or nil when absent or not found.
	Reference *Reference
	// ReferenceAlreadyReversed is true when the reference already has a
	// PROCESSED reversal of the same kind or, for a BET, any PROCESSED
	// REFUND or ROLLBACK.
	ReferenceAlreadyReversed bool
	// LedgerEntryID identifies the ledger entry if the balance moves.
	LedgerEntryID uuid.UUID
	RetryPolicy   RetryPolicy
	Now           time.Time
}

// Process applies the business rules of BET, WIN, LOSS, REFUND and ROLLBACK.
// It moves the wallet balance when the operation succeeds and leaves t
// PROCESSED, REJECTED (with a failure code) or PENDING_REFERENCE. It returns
// the ledger entry when the balance moved. A returned error is never a
// business rejection: it signals misuse or an arithmetic failure.
func (t *WagerTransaction) Process(in ProcessInput) (*wallet.LedgerEntry, error) {
	if t.origin != OriginExternal {
		return nil, fmt.Errorf("%w: only external operations are processed", ErrInvalidTransition)
	}
	if t.status != StatusPending && t.status != StatusPendingReference {
		return nil, fmt.Errorf("%w: cannot process a %s transaction", ErrInvalidTransition, t.status)
	}
	now := in.Now.UTC()
	w := in.Wallet
	switch {
	case w == nil:
		return nil, t.reject(FailureWalletNotFound, now)
	case w.ID() != t.walletID:
		return nil, fmt.Errorf("%w: wallet %s given for transaction on %s", ErrInvalidTransaction, w.ID(), t.walletID)
	case w.PlayerID() != t.playerID:
		return nil, t.reject(FailureWalletPlayerMismatch, now)
	case w.Currency() != t.money.Currency():
		return nil, t.reject(FailureCurrencyMismatch, now)
	}

	if t.needsReference() {
		code, wait := t.resolveReference(in.Reference, in.ReferenceAlreadyReversed)
		if wait {
			return nil, t.waitForReference(in.RetryPolicy, in.Reference != nil, now)
		}
		if code != "" {
			return nil, t.reject(code, now)
		}
	}

	dir, moves := t.movement()
	if !moves {
		return nil, t.complete(w, nil, now)
	}
	var entry wallet.LedgerEntry
	var err error
	if dir == wallet.DirectionDebit {
		entry, err = w.Debit(t.id, in.LedgerEntryID, t.money, now)
	} else {
		entry, err = w.Credit(t.id, in.LedgerEntryID, t.money, now)
	}
	if errors.Is(err, wallet.ErrInsufficientFunds) {
		if t.kind == KindBet {
			return nil, t.reject(FailureInsufficientFunds, now)
		}
		return nil, t.reject(FailureInsufficientFundsForReversal, now)
	}
	if err != nil {
		return nil, err
	}
	return &entry, t.complete(w, &entry, now)
}

func (t *WagerTransaction) needsReference() bool {
	return t.kind.IsReversal() || (t.kind == KindWin && t.referenceExternalTransactionID != "")
}

// resolveReference checks ref and records it on t when it exists and is
// final. It returns wait=true while the reference is missing or pending, or a
// failure code when the operation must be rejected.
func (t *WagerTransaction) resolveReference(ref *Reference, alreadyReversed bool) (FailureCode, bool) {
	if ref == nil {
		return "", true
	}
	switch ref.Status {
	case StatusPending, StatusPendingReference:
		return "", true
	case StatusRejected, StatusFailed:
		return FailureReferenceNotProcessed, false
	}
	t.referenceTransactionID = ref.ID
	t.referenceKind = ref.Kind
	if !t.acceptsReferenceKind(ref.Kind) {
		return FailureReferenceKindInvalid, false
	}
	if ref.WalletID != t.walletID || ref.PlayerID != t.playerID || ref.RoundID != t.roundID ||
		ref.Money.Currency() != t.money.Currency() {
		return FailureReferenceMismatch, false
	}
	if t.kind.IsReversal() {
		if !ref.Money.Equal(t.money) {
			return FailureAmountMismatch, false
		}
		if alreadyReversed {
			return FailureReferenceAlreadyReversed, false
		}
	}
	return "", false
}

func (t *WagerTransaction) acceptsReferenceKind(k Kind) bool {
	switch t.kind {
	case KindWin, KindRefund:
		return k == KindBet
	case KindRollback:
		return k == KindBet || k == KindWin || k == KindRefund
	default:
		return false
	}
}

// waitForReference counts a failed resolution and schedules the next one, or
// rejects when attempts are exhausted. Only the first wait records
// WagerTransactionPendingReference.
func (t *WagerTransaction) waitForReference(policy RetryPolicy, referenceExists bool, now time.Time) error {
	if err := policy.Validate(); err != nil {
		return err
	}
	t.attempts++
	if t.attempts >= policy.MaxAttempts {
		if referenceExists {
			return t.reject(FailureReferenceNotProcessed, now)
		}
		return t.reject(FailureReferenceNotFound, now)
	}
	t.nextAttemptAt = now.Add(policy.Delay(t.attempts))
	if t.status == StatusPendingReference {
		t.updatedAt = now
		return nil
	}
	if err := t.transition(StatusPendingReference, now); err != nil {
		return err
	}
	t.record(events.WagerTransactionPendingReference{
		TransactionID:                  t.id,
		WalletID:                       t.walletID,
		ProviderID:                     t.providerID,
		ExternalTransactionID:          t.externalTransactionID,
		Kind:                           string(t.kind),
		ReferenceExternalTransactionID: t.referenceExternalTransactionID,
		Attempts:                       t.attempts,
		NextAttemptAt:                  events.FormatTime(t.nextAttemptAt),
	})
	return nil
}

// movement returns the ledger direction of t, or moves=false for LOSS.
func (t *WagerTransaction) movement() (wallet.Direction, bool) {
	switch t.kind {
	case KindBet:
		return wallet.DirectionDebit, true
	case KindWin, KindRefund:
		return wallet.DirectionCredit, true
	case KindRollback:
		if t.referenceKind == KindBet {
			return wallet.DirectionCredit, true
		}
		return wallet.DirectionDebit, true
	default:
		return "", false
	}
}
