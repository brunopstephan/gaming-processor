package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// ConsumerWagerTransactions is the inbox consumer name of the input queue.
const ConsumerWagerTransactions = "wager-transactions"

// IntakeOutcome says how a message was handled.
type IntakeOutcome string

// Intake outcomes.
const (
	// IntakeHandled: the operation was processed (or replayed) and the inbox
	// entry committed with it.
	IntakeHandled IntakeOutcome = "HANDLED"
	// IntakeDuplicate: the message was already handled with the same payload.
	IntakeDuplicate IntakeOutcome = "DUPLICATE"
	// IntakeMessageReused: the messageId was already handled with another payload.
	IntakeMessageReused IntakeOutcome = "MESSAGE_ID_REUSED"
)

// IntakeResult is the outcome of Handle. Transaction and Replay are set when
// Outcome is IntakeHandled.
type IntakeResult struct {
	Outcome     IntakeOutcome
	Transaction *wagering.WagerTransaction
	Replay      bool
}

// IntakeService handles operations that arrive as messages: the inbox drops
// redeliveries by messageId, and the operation itself goes through
// WageringService.Process, so HTTP and SQS share idempotency.
type IntakeService struct {
	d        Deps
	inbox    InboxRepository
	wagering *WageringService
}

// NewIntakeService builds an IntakeService.
func NewIntakeService(d Deps, inbox InboxRepository, wagering *WageringService) *IntakeService {
	return &IntakeService{d: d, inbox: inbox, wagering: wagering}
}

// InboxHash identifies a message's content: the business payload hash plus
// the idempotency key (both carried by the message's data).
func InboxHash(cmd wagering.Command) string {
	sum := sha256.Sum256([]byte(cmd.PayloadHash() + "\n" + cmd.IdempotencyKey))
	return hex.EncodeToString(sum[:])
}

// errInboxRace aborts the processing transaction when a concurrent delivery
// of the same message recorded the inbox entry first.
var errInboxRace = errors.New("app: message recorded concurrently")

// Handle processes cmd, carried by messageID, at most once per message:
//
//   - a messageId already in the inbox with the same content is
//     IntakeDuplicate; with other content, IntakeMessageReused (nothing runs);
//   - otherwise Process runs with the inbox insert in its transaction, so the
//     entry commits with the outcome (PROCESSED, REJECTED, PENDING_REFERENCE,
//     a replay of an operation received by HTTP, or FAILED).
//
// Errors are those of Process (ErrTransient for retries; the conflict errors
// for a reused key or external id).
func (s *IntakeService) Handle(ctx context.Context, messageID string, cmd wagering.Command, meta Meta) (IntakeResult, error) {
	if messageID == "" {
		return IntakeResult{}, errors.New("app: messageId is required")
	}
	hash := InboxHash(cmd)
	if res, seen, err := s.seen(ctx, messageID, hash); seen || err != nil {
		return res, err
	}
	meta.Channel = ChannelSQS
	meta.CausationID = messageID
	receivedAt := s.d.Clock()
	res, err := s.wagering.Process(ctx, cmd, meta, func(ctx context.Context) error {
		inserted, err := s.inbox.Insert(ctx, InboxMessage{
			Consumer: ConsumerWagerTransactions, MessageID: messageID, PayloadHash: hash,
			ReceivedAt: receivedAt, ProcessedAt: s.d.Clock(),
		})
		if err != nil {
			return err
		}
		if !inserted {
			return errInboxRace
		}
		return nil
	})
	if errors.Is(err, errInboxRace) {
		res, seen, err := s.seen(ctx, messageID, hash)
		if err == nil && !seen {
			return IntakeResult{}, fmt.Errorf("%w: inbox entry %q not visible", ErrTransient, messageID)
		}
		return res, err
	}
	if err != nil {
		return IntakeResult{}, err
	}
	return IntakeResult{Outcome: IntakeHandled, Transaction: res.Transaction, Replay: res.Replay}, nil
}

// seen answers from the inbox; seen=false means the message is new.
func (s *IntakeService) seen(ctx context.Context, messageID, hash string) (IntakeResult, bool, error) {
	m, err := s.inbox.Get(ctx, ConsumerWagerTransactions, messageID)
	switch {
	case errors.Is(err, ErrNotFound):
		return IntakeResult{}, false, nil
	case err != nil:
		return IntakeResult{}, false, err
	case m.PayloadHash != hash:
		return IntakeResult{Outcome: IntakeMessageReused}, true, nil
	}
	s.d.Metrics.InboxDuplicate()
	return IntakeResult{Outcome: IntakeDuplicate}, true, nil
}
