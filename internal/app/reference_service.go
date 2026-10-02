package app

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// ReferenceService resumes PENDING_REFERENCE operations: each call claims
// the oldest due operation in its own transaction (FOR NO KEY UPDATE SKIP
// LOCKED, so workers on any instance never take the same row), locks the
// wallet and re-runs the same processing as Process. Restarts lose nothing:
// the pending state lives only in the database.
type ReferenceService struct {
	d        Deps
	wagering *WageringService
}

// NewReferenceService builds a ReferenceService.
func NewReferenceService(d Deps, wagering *WageringService) *ReferenceService {
	return &ReferenceService{d: d, wagering: wagering}
}

// ResumeNext resumes one due operation; found=false when none is due. The
// outcome is PROCESSED, REJECTED (including REFERENCE_NOT_FOUND once the
// attempts are exhausted) or still PENDING_REFERENCE with the next attempt
// scheduled. A transient error leaves the operation due for the next run; a
// permanent one marks it FAILED.
func (s *ReferenceService) ResumeNext(ctx context.Context) (bool, error) {
	var (
		t    *wagering.WagerTransaction
		meta Meta
	)
	err := s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		claimed, err := s.d.Transactions.ClaimDuePending(ctx, s.d.Clock())
		if err != nil {
			return err
		}
		t = claimed
		id := t.ID().String()
		meta = Meta{CorrelationID: id, CausationID: id, Channel: ChannelWorker}
		return s.wagering.apply(ctx, t, meta)
	})
	switch {
	case t == nil && errors.Is(err, ErrNotFound):
		return false, nil
	case t == nil:
		return false, err
	case err != nil:
		if terr := s.wagering.transientFailure(ctx, err); terr != nil {
			return true, terr
		}
		return true, s.fail(ctx, t.ID(), meta, err)
	}
	if t.Status() == wagering.StatusPendingReference {
		s.d.Metrics.ReferenceRetry()
		return true, nil
	}
	s.wagering.completed(ctx, t, meta)
	return true, nil
}

// fail records a permanent infrastructure failure on the pending row itself,
// in a new transaction, unless it was resolved meanwhile.
func (s *ReferenceService) fail(ctx context.Context, id uuid.UUID, meta Meta, cause error) error {
	s.d.Log.ErrorContext(ctx, "permanent failure resuming pending wager transaction",
		"transactionId", id.String(), "error", cause.Error())
	var failed *wagering.WagerTransaction
	err := s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		t, err := s.d.Transactions.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if t.Status() != wagering.StatusPendingReference {
			return nil
		}
		if err := t.MarkFailed(s.d.Clock()); err != nil {
			return err
		}
		failed = t
		return s.d.Transactions.Update(ctx, t)
	})
	if err != nil {
		return errors.Join(cause, err)
	}
	if failed != nil {
		s.wagering.completed(ctx, failed, meta)
	}
	return nil
}
