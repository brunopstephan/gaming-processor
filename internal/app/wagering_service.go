package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// TransactionResult is the outcome of Process: the transaction (new or
// persisted) and whether it is an idempotent replay.
type TransactionResult struct {
	Transaction *wagering.WagerTransaction
	Replay      bool
}

// WageringService processes external operations (BET, WIN, LOSS, REFUND,
// ROLLBACK) for HTTP and SQS with the same idempotency guarantees.
type WageringService struct {
	d      Deps
	policy wagering.RetryPolicy
}

// NewWageringService validates the reference retry policy and builds the service.
func NewWageringService(d Deps, policy wagering.RetryPolicy) (*WageringService, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &WageringService{d: d, policy: policy}, nil
}

// alongsideError marks an error returned by the caller's alongside hook: it
// rolls the transaction back but never becomes a FAILED transaction.
type alongsideError struct{ err error }

func (e *alongsideError) Error() string { return e.err.Error() }
func (e *alongsideError) Unwrap() error { return e.err }

// Process applies cmd exactly once per (provider, idempotency key):
//
//   - a key already used with the same payload returns the persisted result
//     (Replay=true); with another payload, ErrIdempotencyKeyConflict;
//   - an external id already registered under another key is
//     ErrExternalTransactionConflict;
//   - otherwise the operation is inserted PENDING, the wallet is row-locked,
//     the domain decides and everything (transaction, balance, ledger, outbox
//     and the caller's alongside work) commits in one SQL transaction.
//
// Transient failures return an error matching ErrTransient and leave no
// trace. alongside (may be nil) runs inside the transaction that commits the
// outcome, including replays.
//
// A permanent infrastructure failure is a recorded outcome, not an error: it
// returns err == nil with a FAILED transaction (failureCode
// INFRASTRUCTURE_FAILURE). Callers must check the returned status, not only
// err, to answer HTTP 500 or send the message to the SQS DLQ. If recording
// the FAILED transaction itself fails, the returned error joins both causes
// and matches ErrTransient when either of them is transient.
//
// Process must not be called inside a transaction (the caller's atomic work
// goes in alongside): if ctx already carries one, it returns
// ErrInsideTransaction without touching anything.
func (s *WageringService) Process(ctx context.Context, cmd wagering.Command, meta Meta, alongside func(ctx context.Context) error) (TransactionResult, error) {
	if s.d.Tx.InTx(ctx) {
		return TransactionResult{}, ErrInsideTransaction
	}
	meta = meta.normalized()
	start := time.Now()
	defer func() { s.d.Metrics.ProcessingDuration(meta.Channel, time.Since(start)) }()

	if res, found, err := s.lookup(ctx, cmd, meta, alongside); found || err != nil {
		return res, err
	}
	t, err := wagering.NewExternal(newID(), cmd, s.d.Clock())
	if err != nil {
		return TransactionResult{}, err
	}
	raced := false
	err = s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		inserted, err := s.d.Transactions.Insert(ctx, t)
		if err != nil {
			return err
		}
		if !inserted {
			raced = true
			return nil
		}
		if err := s.apply(ctx, t, meta); err != nil {
			return err
		}
		if err := run(ctx, alongside); err != nil {
			return &alongsideError{err: err}
		}
		return nil
	})
	switch {
	case err != nil:
		return s.failure(ctx, cmd, meta, alongside, err)
	case raced:
		// A concurrent request with the same key or external id committed
		// first; answer from its persisted outcome.
		res, found, err := s.lookup(ctx, cmd, meta, alongside)
		if err == nil && !found {
			return TransactionResult{}, fmt.Errorf("%w: concurrent insert of %q not visible", ErrTransient, cmd.IdempotencyKey)
		}
		return res, err
	}
	s.completed(ctx, t, meta)
	return TransactionResult{Transaction: t}, nil
}

// lookup answers from persisted state: replay, key conflict or external-id
// conflict. found=false means the operation is new.
func (s *WageringService) lookup(ctx context.Context, cmd wagering.Command, meta Meta, alongside func(ctx context.Context) error) (TransactionResult, bool, error) {
	existing, err := s.d.Transactions.FindByIdempotencyKey(ctx, cmd.ProviderID, cmd.IdempotencyKey)
	switch {
	case err == nil:
		return s.replay(ctx, existing, cmd, meta, alongside)
	case !errors.Is(err, ErrNotFound):
		return TransactionResult{}, false, err
	}
	byExternal, err := s.d.Transactions.FindByExternalID(ctx, cmd.ProviderID, cmd.ExternalTransactionID)
	switch {
	case err == nil:
		// The key lookup above and this one are separate reads: a concurrent
		// request with the same key may have committed in between. Same key
		// means the same operation, so answer as a replay.
		if byExternal.IdempotencyKey() == cmd.IdempotencyKey {
			return s.replay(ctx, byExternal, cmd, meta, alongside)
		}
		return TransactionResult{}, true, ErrExternalTransactionConflict
	case errors.Is(err, ErrNotFound):
		return TransactionResult{}, false, nil
	default:
		return TransactionResult{}, false, err
	}
}

// replay answers cmd from the already persisted operation existing.
func (s *WageringService) replay(ctx context.Context, existing *wagering.WagerTransaction, cmd wagering.Command, meta Meta, alongside func(ctx context.Context) error) (TransactionResult, bool, error) {
	if existing.PayloadHash() != cmd.PayloadHash() {
		return TransactionResult{}, true, ErrIdempotencyKeyConflict
	}
	if alongside != nil {
		if err := s.d.Tx.WithinTx(ctx, alongside); err != nil {
			return TransactionResult{}, true, err
		}
	}
	s.d.Metrics.IdempotentReplay(meta.Channel)
	return TransactionResult{Transaction: existing, Replay: true}, true, nil
}

// apply locks the wallet, lets the domain decide and persists the outcome in
// the caller's transaction. Lock order: the transaction row is inserted first
// (waiting on the unique index if a concurrent request holds the same key),
// then the wallet is row-locked, then references are read.
func (s *WageringService) apply(ctx context.Context, t *wagering.WagerTransaction, meta Meta) error {
	w, err := s.d.Wallets.GetForUpdate(ctx, t.WalletID())
	switch {
	case errors.Is(err, ErrNotFound):
		w = nil
	case err != nil:
		return err
	}
	var before int64
	if w != nil {
		before = w.Version()
	}
	in := wagering.ProcessInput{Wallet: w, LedgerEntryID: newID(), RetryPolicy: s.policy, Now: s.d.Clock()}
	if err := s.resolveReference(ctx, t, &in); err != nil {
		return err
	}
	entry, err := t.Process(in)
	if err != nil {
		return err
	}
	if entry != nil {
		if err := s.d.Wallets.Update(ctx, w, before); err != nil {
			return err
		}
		if err := s.d.Ledger.Append(ctx, *entry); err != nil {
			return err
		}
	}
	if err := s.d.Transactions.Update(ctx, t); err != nil {
		return err
	}
	return publish(ctx, s.d.Outbox, s.d.Clock(), t.PullEvents(), meta)
}

// failure classifies an error of the processing transaction, which has
// already rolled back. Transient errors are returned for retry; nothing is
// recorded. Anything else is a permanent failure and is recorded as FAILED.
func (s *WageringService) failure(ctx context.Context, cmd wagering.Command, meta Meta, alongside func(ctx context.Context) error, err error) (TransactionResult, error) {
	var hookErr *alongsideError
	switch {
	case errors.As(err, &hookErr):
		return TransactionResult{}, err
	case ctx.Err() != nil:
		// The request was canceled or timed out: the rollback is not a
		// verdict about the operation, so never record FAILED.
		s.d.Metrics.ConcurrencyConflict(ConflictTransient)
		return TransactionResult{}, fmt.Errorf("%w: %w", ErrTransient, ctx.Err())
	case errors.Is(err, ErrLockTimeout):
		s.d.Metrics.ConcurrencyConflict(ConflictLockTimeout)
		return TransactionResult{}, err
	case errors.Is(err, ErrVersionConflict):
		s.d.Metrics.ConcurrencyConflict(ConflictVersion)
		return TransactionResult{}, err
	case errors.Is(err, ErrTransient):
		s.d.Metrics.ConcurrencyConflict(ConflictTransient)
		return TransactionResult{}, err
	case errors.Is(err, ErrConflict):
		// A unique index refused a write inside processing (a race the wallet
		// lock should prevent); retrying re-reads the winner's state.
		s.d.Metrics.ConcurrencyConflict(ConflictUnique)
		return TransactionResult{}, fmt.Errorf("%w: %v", ErrTransient, err)
	}
	return s.recordFailure(ctx, cmd, meta, alongside, err)
}

// recordFailure stores the operation as FAILED (INFRASTRUCTURE_FAILURE) in a
// new transaction, for audit, after the processing transaction rolled back.
// The caller's alongside work commits with the FAILED record. If even this
// write fails, both errors are returned (transient if either is).
func (s *WageringService) recordFailure(ctx context.Context, cmd wagering.Command, meta Meta, alongside func(ctx context.Context) error, cause error) (TransactionResult, error) {
	t, err := wagering.NewExternal(newID(), cmd, s.d.Clock())
	if err != nil {
		return TransactionResult{}, errors.Join(cause, err)
	}
	s.d.Log.ErrorContext(ctx, "permanent failure processing wager transaction",
		"transactionId", t.ID().String(), "providerId", cmd.ProviderID, "walletId", cmd.WalletID.String(),
		"correlationId", meta.CorrelationID, "messageId", meta.CausationID, "error", cause.Error())
	if err := t.MarkFailed(s.d.Clock()); err != nil {
		return TransactionResult{}, errors.Join(cause, err)
	}
	raced := false
	err = s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		inserted, err := s.d.Transactions.Insert(ctx, t)
		if err != nil {
			return err
		}
		if !inserted {
			raced = true
			return nil
		}
		if err := run(ctx, alongside); err != nil {
			return &alongsideError{err: err}
		}
		return nil
	})
	if err != nil {
		return TransactionResult{}, errors.Join(cause, err)
	}
	if raced {
		res, found, err := s.lookup(ctx, cmd, meta, alongside)
		if err == nil && !found {
			return TransactionResult{}, fmt.Errorf("%w: concurrent insert of %q not visible", ErrTransient, cmd.IdempotencyKey)
		}
		return res, err
	}
	s.completed(ctx, t, meta)
	return TransactionResult{Transaction: t}, nil
}

func (s *WageringService) completed(ctx context.Context, t *wagering.WagerTransaction, meta Meta) {
	s.d.Metrics.TransactionCompleted(t.Kind(), t.Status(), meta.Channel)
	s.d.Log.InfoContext(ctx, "wager transaction completed",
		"transactionId", t.ID().String(), "walletId", t.WalletID().String(), "providerId", t.ProviderID(),
		"kind", string(t.Kind()), "status", string(t.Status()), "failureCode", string(t.FailureCode()),
		"correlationId", meta.CorrelationID, "messageId", meta.CausationID)
}

// resolveReference loads the referenced operation of the same provider and,
// for reversals, whether it was already reversed. The wallet row is already
// locked, so a reference on the same wallet cannot change underneath; a
// reference on another wallet is rejected by the domain (REFERENCE_MISMATCH).
func (s *WageringService) resolveReference(ctx context.Context, t *wagering.WagerTransaction, in *wagering.ProcessInput) error {
	refExt := t.ReferenceExternalTransactionID()
	if refExt == "" {
		return nil
	}
	ref, err := s.d.Transactions.FindByExternalID(ctx, t.ProviderID(), refExt)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil
	case err != nil:
		return err
	}
	r := ref.AsReference()
	in.Reference = &r
	if t.Kind().IsReversal() {
		reversed, err := s.d.Transactions.HasProcessedReversal(ctx, r.ID, t.Kind(), r.Kind)
		if err != nil {
			return err
		}
		in.ReferenceAlreadyReversed = reversed
	}
	return nil
}
