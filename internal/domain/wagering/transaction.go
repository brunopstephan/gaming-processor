package wagering

import (
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

// WagerTransaction is one financial operation. External operations carry
// provider metadata and idempotency data; the internal OPENING does not.
// State changes only through validated transitions, which record the
// integration events to be written to the outbox.
type WagerTransaction struct {
	id                             uuid.UUID
	origin                         Origin
	kind                           Kind
	status                         Status
	walletID                       uuid.UUID
	playerID                       uuid.UUID
	money                          money.Money
	providerID                     string
	externalTransactionID          string
	idempotencyKey                 string
	payloadHash                    string
	roundID                        string
	gameID                         string
	referenceExternalTransactionID string
	referenceTransactionID         uuid.UUID
	referenceKind                  Kind
	failureCode                    FailureCode
	resultBalance                  money.Money
	attempts                       int
	nextAttemptAt                  time.Time
	createdAt                      time.Time
	updatedAt                      time.Time
	processedAt                    time.Time
	events                         []events.Data
}

// State is the full persisted form of a WagerTransaction. Optional values use
// zero values: uuid.Nil, "", the zero Money, the zero time.
type State struct {
	ID                             uuid.UUID
	Origin                         Origin
	Kind                           Kind
	Status                         Status
	WalletID                       uuid.UUID
	PlayerID                       uuid.UUID
	Money                          money.Money
	ProviderID                     string
	ExternalTransactionID          string
	IdempotencyKey                 string
	PayloadHash                    string
	RoundID                        string
	GameID                         string
	ReferenceExternalTransactionID string
	ReferenceTransactionID         uuid.UUID
	ReferenceKind                  Kind
	FailureCode                    FailureCode
	ResultBalance                  money.Money
	Attempts                       int
	NextAttemptAt                  time.Time
	CreatedAt                      time.Time
	UpdatedAt                      time.Time
	ProcessedAt                    time.Time
}

// NewExternal creates a PENDING external operation from a validated command.
func NewExternal(id uuid.UUID, cmd Command, now time.Time) (*WagerTransaction, error) {
	switch {
	case id == uuid.Nil || now.IsZero():
		return nil, fmt.Errorf("%w: id and now are required", ErrInvalidTransaction)
	case !cmd.Kind.IsExternal():
		return nil, fmt.Errorf("%w: kind %q is not external", ErrInvalidTransaction, cmd.Kind)
	case cmd.WalletID == uuid.Nil || cmd.PlayerID == uuid.Nil || !cmd.Money.IsValid():
		return nil, fmt.Errorf("%w: wallet, player and money are required", ErrInvalidTransaction)
	case cmd.ProviderID == "" || cmd.ExternalTransactionID == "" || cmd.IdempotencyKey == "" ||
		cmd.RoundID == "" || cmd.GameID == "":
		return nil, fmt.Errorf("%w: external metadata is required", ErrInvalidTransaction)
	}
	now = now.UTC()
	return &WagerTransaction{
		id:                             id,
		origin:                         OriginExternal,
		kind:                           cmd.Kind,
		status:                         StatusPending,
		walletID:                       cmd.WalletID,
		playerID:                       cmd.PlayerID,
		money:                          cmd.Money,
		providerID:                     cmd.ProviderID,
		externalTransactionID:          cmd.ExternalTransactionID,
		idempotencyKey:                 cmd.IdempotencyKey,
		payloadHash:                    cmd.PayloadHash(),
		roundID:                        cmd.RoundID,
		gameID:                         cmd.GameID,
		referenceExternalTransactionID: cmd.ReferenceExternalTransactionID,
		createdAt:                      now,
		updatedAt:                      now,
	}, nil
}

// NewOpening creates the PENDING internal OPENING credit of a wallet opened
// with a positive balance. A zero initial balance creates no OPENING.
func NewOpening(id, walletID, playerID uuid.UUID, amount money.Money, now time.Time) (*WagerTransaction, error) {
	if id == uuid.Nil || walletID == uuid.Nil || playerID == uuid.Nil || now.IsZero() {
		return nil, fmt.Errorf("%w: id, walletId, playerId and now are required", ErrInvalidTransaction)
	}
	if !amount.IsPositive() {
		return nil, fmt.Errorf("%w: OPENING requires a positive amount", ErrInvalidTransaction)
	}
	now = now.UTC()
	return &WagerTransaction{
		id:        id,
		origin:    OriginInternal,
		kind:      KindOpening,
		status:    StatusPending,
		walletID:  walletID,
		playerID:  playerID,
		money:     amount,
		createdAt: now,
		updatedAt: now,
	}, nil
}

// CompleteOpening marks the OPENING as PROCESSED once the wallet was opened
// with entry, recording WagerTransactionProcessed and WalletBalanceChanged.
func (t *WagerTransaction) CompleteOpening(w *wallet.Wallet, entry wallet.LedgerEntry, now time.Time) error {
	if t.kind != KindOpening {
		return fmt.Errorf("%w: CompleteOpening on %s", ErrInvalidTransition, t.kind)
	}
	if w.ID() != t.walletID || entry.TransactionID() != t.id || entry.WalletID() != t.walletID {
		return fmt.Errorf("%w: wallet or entry does not belong to this OPENING", ErrInvalidTransaction)
	}
	return t.complete(w, &entry, now)
}

// MarkFailed records a permanent infrastructure failure for audit.
func (t *WagerTransaction) MarkFailed(now time.Time) error {
	if err := t.transition(StatusFailed, now); err != nil {
		return err
	}
	t.failureCode = FailureInfrastructure
	t.processedAt = t.updatedAt
	t.nextAttemptAt = time.Time{}
	return nil
}

// PullEvents returns the events recorded since the last call and clears them.
func (t *WagerTransaction) PullEvents() []events.Data {
	evs := t.events
	t.events = nil
	return evs
}

// Rehydrate rebuilds a stored transaction without transitions or events.
func Rehydrate(s State) (*WagerTransaction, error) {
	if err := s.validate(); err != nil {
		return nil, err
	}
	return &WagerTransaction{
		id:                             s.ID,
		origin:                         s.Origin,
		kind:                           s.Kind,
		status:                         s.Status,
		walletID:                       s.WalletID,
		playerID:                       s.PlayerID,
		money:                          s.Money,
		providerID:                     s.ProviderID,
		externalTransactionID:          s.ExternalTransactionID,
		idempotencyKey:                 s.IdempotencyKey,
		payloadHash:                    s.PayloadHash,
		roundID:                        s.RoundID,
		gameID:                         s.GameID,
		referenceExternalTransactionID: s.ReferenceExternalTransactionID,
		referenceTransactionID:         s.ReferenceTransactionID,
		referenceKind:                  s.ReferenceKind,
		failureCode:                    s.FailureCode,
		resultBalance:                  s.ResultBalance,
		attempts:                       s.Attempts,
		nextAttemptAt:                  s.NextAttemptAt,
		createdAt:                      s.CreatedAt,
		updatedAt:                      s.UpdatedAt,
		processedAt:                    s.ProcessedAt,
	}, nil
}

func (s State) validate() error {
	fail := func(reason string) error { return fmt.Errorf("%w: %s", ErrInvalidTransaction, reason) }
	switch {
	case s.ID == uuid.Nil || s.WalletID == uuid.Nil || s.PlayerID == uuid.Nil:
		return fail("id, walletId and playerId are required")
	case !s.Kind.IsValid() || !s.Status.IsValid():
		return fail("unknown kind or status")
	case !s.Money.IsValid() || s.Money.IsNegative():
		return fail("money must be valid and non-negative")
	case s.CreatedAt.IsZero() || s.UpdatedAt.IsZero():
		return fail("timestamps are required")
	case s.Attempts < 0:
		return fail("attempts must be non-negative")
	}
	switch s.Origin {
	case OriginInternal:
		if s.Kind != KindOpening || s.ProviderID != "" || s.ExternalTransactionID != "" || s.IdempotencyKey != "" ||
			s.PayloadHash != "" || s.RoundID != "" || s.GameID != "" || s.ReferenceExternalTransactionID != "" {
			return fail("internal origin must be an OPENING without external metadata")
		}
		if !s.Money.IsPositive() {
			return fail("OPENING requires a strictly positive amount")
		}
	case OriginExternal:
		if !s.Kind.IsExternal() || s.ProviderID == "" || s.ExternalTransactionID == "" || s.IdempotencyKey == "" ||
			s.PayloadHash == "" || s.RoundID == "" || s.GameID == "" {
			return fail("external origin requires an external kind and metadata")
		}
	default:
		return fail("unknown origin")
	}
	switch s.Status {
	case StatusProcessed:
		if !s.ResultBalance.IsValid() || s.FailureCode != "" {
			return fail("PROCESSED requires a result balance and no failure code")
		}
	case StatusRejected:
		if !s.FailureCode.IsBusinessRejection() {
			return fail("REJECTED requires a business rejection code")
		}
	case StatusFailed:
		if s.FailureCode != FailureInfrastructure {
			return fail("FAILED requires FailureInfrastructure code")
		}
	case StatusPending, StatusPendingReference:
		if s.FailureCode != "" {
			return fail("PENDING/PENDING_REFERENCE must not have a failure code")
		}
		if s.Status == StatusPendingReference && s.NextAttemptAt.IsZero() {
			return fail("PENDING_REFERENCE requires nextAttemptAt")
		}
	}
	if s.Status.IsTerminal() && s.ProcessedAt.IsZero() {
		return fail("terminal status requires processedAt")
	}
	return nil
}

// State returns the persisted form of t.
func (t *WagerTransaction) State() State {
	return State{
		ID:                             t.id,
		Origin:                         t.origin,
		Kind:                           t.kind,
		Status:                         t.status,
		WalletID:                       t.walletID,
		PlayerID:                       t.playerID,
		Money:                          t.money,
		ProviderID:                     t.providerID,
		ExternalTransactionID:          t.externalTransactionID,
		IdempotencyKey:                 t.idempotencyKey,
		PayloadHash:                    t.payloadHash,
		RoundID:                        t.roundID,
		GameID:                         t.gameID,
		ReferenceExternalTransactionID: t.referenceExternalTransactionID,
		ReferenceTransactionID:         t.referenceTransactionID,
		ReferenceKind:                  t.referenceKind,
		FailureCode:                    t.failureCode,
		ResultBalance:                  t.resultBalance,
		Attempts:                       t.attempts,
		NextAttemptAt:                  t.nextAttemptAt,
		CreatedAt:                      t.createdAt,
		UpdatedAt:                      t.updatedAt,
		ProcessedAt:                    t.processedAt,
	}
}

// ID returns the internal transaction id.
func (t *WagerTransaction) ID() uuid.UUID { return t.id }

// WalletID returns the wallet the operation targets.
func (t *WagerTransaction) WalletID() uuid.UUID { return t.walletID }

// Kind returns the operation kind.
func (t *WagerTransaction) Kind() Kind { return t.kind }

// Status returns the current status.
func (t *WagerTransaction) Status() Status { return t.status }

// FailureCode returns the rejection or failure code, if any.
func (t *WagerTransaction) FailureCode() FailureCode { return t.failureCode }

// PayloadHash returns the canonical business payload hash (external only).
func (t *WagerTransaction) PayloadHash() string { return t.payloadHash }

// ProviderID returns the provider (external only).
func (t *WagerTransaction) ProviderID() string { return t.providerID }

// ExternalTransactionID returns the provider's transaction id (external only).
func (t *WagerTransaction) ExternalTransactionID() string { return t.externalTransactionID }

// IdempotencyKey returns the key the operation was received with (external only).
func (t *WagerTransaction) IdempotencyKey() string { return t.idempotencyKey }

// ReferenceExternalTransactionID returns the requested reference, if any.
func (t *WagerTransaction) ReferenceExternalTransactionID() string {
	return t.referenceExternalTransactionID
}

// ReferenceTransactionID returns the resolved internal reference, or uuid.Nil.
func (t *WagerTransaction) ReferenceTransactionID() uuid.UUID { return t.referenceTransactionID }

// ResultBalance returns the balance observed when the operation was
// PROCESSED; it is the zero Money otherwise.
func (t *WagerTransaction) ResultBalance() money.Money { return t.resultBalance }

// Attempts returns how many reference resolutions failed so far.
func (t *WagerTransaction) Attempts() int { return t.attempts }

// NextAttemptAt returns when the reference worker retries; zero if none.
func (t *WagerTransaction) NextAttemptAt() time.Time { return t.nextAttemptAt }

func (t *WagerTransaction) transition(next Status, now time.Time) error {
	if !t.status.CanTransitionTo(next) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, t.status, next)
	}
	t.status = next
	t.updatedAt = now.UTC()
	return nil
}

func (t *WagerTransaction) record(e events.Data) { t.events = append(t.events, e) }

// complete marks t PROCESSED, snapshots the wallet balance and records the
// events. entry is nil when the balance did not move (LOSS).
func (t *WagerTransaction) complete(w *wallet.Wallet, entry *wallet.LedgerEntry, now time.Time) error {
	if err := t.transition(StatusProcessed, now); err != nil {
		return err
	}
	t.resultBalance = w.Balance()
	t.processedAt = t.updatedAt
	t.nextAttemptAt = time.Time{}
	processed := events.WagerTransactionProcessed{
		TransactionID:         t.id,
		WalletID:              t.walletID,
		PlayerID:              t.playerID,
		Origin:                string(t.origin),
		Kind:                  string(t.kind),
		Money:                 t.money,
		ProviderID:            t.providerID,
		ExternalTransactionID: t.externalTransactionID,
		RoundID:               t.roundID,
		GameID:                t.gameID,
		BalanceAfter:          t.resultBalance,
	}
	if t.referenceTransactionID != uuid.Nil {
		ref := t.referenceTransactionID
		processed.ReferenceTransactionID = &ref
	}
	t.record(processed)
	if entry != nil {
		t.record(events.WalletBalanceChanged{
			WalletID:      entry.WalletID(),
			TransactionID: entry.TransactionID(),
			Direction:     string(entry.Direction()),
			Money:         entry.Amount(),
			BalanceBefore: entry.BalanceBefore(),
			BalanceAfter:  entry.BalanceAfter(),
			WalletVersion: w.Version(),
		})
	}
	return nil
}

// reject marks t REJECTED with code and records WagerTransactionRejected.
func (t *WagerTransaction) reject(code FailureCode, now time.Time) error {
	if err := t.transition(StatusRejected, now); err != nil {
		return err
	}
	t.failureCode = code
	t.processedAt = t.updatedAt
	t.nextAttemptAt = time.Time{}
	t.record(events.WagerTransactionRejected{
		TransactionID:         t.id,
		WalletID:              t.walletID,
		ProviderID:            t.providerID,
		ExternalTransactionID: t.externalTransactionID,
		Kind:                  string(t.kind),
		Money:                 t.money,
		FailureCode:           string(code),
	})
	return nil
}
