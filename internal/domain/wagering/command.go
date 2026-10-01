package wagering

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

// MaxFieldLength bounds free-text identifiers and the idempotency key, in bytes.
const MaxFieldLength = 255

// RawCommand is an external operation exactly as received over HTTP or SQS.
// Absent optional fields are empty strings.
type RawCommand struct {
	ProviderID                     string
	ExternalTransactionID          string
	IdempotencyKey                 string
	PlayerID                       string
	WalletID                       string
	RoundID                        string
	GameID                         string
	Kind                           string
	Amount                         string
	Currency                       string
	ReferenceExternalTransactionID string
}

// Command is a validated external operation. HTTP and SQS build the same
// Command, so idempotency and hashing are identical across channels.
type Command struct {
	ProviderID                     string
	ExternalTransactionID          string
	IdempotencyKey                 string
	PlayerID                       uuid.UUID
	WalletID                       uuid.UUID
	RoundID                        string
	GameID                         string
	Kind                           Kind
	Money                          money.Money
	ReferenceExternalTransactionID string
}

// ParseCommand validates raw and returns a Command, or an *InputError listing
// every correctable violation. Values are never normalized: UUIDs must be
// lowercase canonical and money must be canonical, so the payload hash covers
// exactly what was received.
func ParseCommand(raw RawCommand) (Command, error) {
	var v violations
	v.requireText("providerId", raw.ProviderID)
	v.requireText("externalTransactionId", raw.ExternalTransactionID)
	v.requireText("roundId", raw.RoundID)
	v.requireText("gameId", raw.GameID)
	switch {
	case raw.IdempotencyKey == "":
		v.add(FailureMissingIdempotencyKey, "idempotencyKey", "is required")
	case len(raw.IdempotencyKey) > MaxFieldLength:
		v.add(FailureInvalidIdempotencyKey, "idempotencyKey", fmt.Sprintf("must be at most %d bytes", MaxFieldLength))
	}
	playerID := v.parseID("playerId", raw.PlayerID)
	walletID := v.parseID("walletId", raw.WalletID)
	kind, kindOK := v.parseKind(raw.Kind)
	amount, moneyOK := v.parseMoney(raw.Amount, raw.Currency)

	if kindOK && moneyOK {
		switch {
		case kind == KindLoss && !amount.IsZero():
			v.add(FailureInvalidAmountForKind, "money.amount", "LOSS requires 0.00")
		case kind != KindLoss && !amount.IsPositive():
			v.add(FailureInvalidAmountForKind, "money.amount", fmt.Sprintf("%s requires an amount greater than 0.00", kind))
		}
	}
	ref := raw.ReferenceExternalTransactionID
	if kindOK {
		switch {
		case kind.IsReversal() && ref == "":
			v.add(FailureMissingReference, "referenceExternalTransactionId", fmt.Sprintf("is required for %s", kind))
		case (kind == KindBet || kind == KindLoss) && ref != "":
			v.add(FailureReferenceNotAllowed, "referenceExternalTransactionId", fmt.Sprintf("is not allowed for %s", kind))
		}
	}
	if len(ref) > MaxFieldLength {
		v.add(FailureInvalidField, "referenceExternalTransactionId", fmt.Sprintf("must be at most %d bytes", MaxFieldLength))
	}
	if len(v) > 0 {
		return Command{}, &InputError{Violations: v}
	}
	return Command{
		ProviderID:                     raw.ProviderID,
		ExternalTransactionID:          raw.ExternalTransactionID,
		IdempotencyKey:                 raw.IdempotencyKey,
		PlayerID:                       playerID,
		WalletID:                       walletID,
		RoundID:                        raw.RoundID,
		GameID:                         raw.GameID,
		Kind:                           kind,
		Money:                          amount,
		ReferenceExternalTransactionID: ref,
	}, nil
}

// PayloadHash returns the hex SHA-256 of the canonical JSON of the business
// fields: keys sorted, no insignificant whitespace, no HTML escaping. The
// idempotency key and transport metadata are excluded, and
// referenceExternalTransactionId is omitted when absent.
func (c Command) PayloadHash() string {
	fields := map[string]any{
		"providerId":            c.ProviderID,
		"externalTransactionId": c.ExternalTransactionID,
		"playerId":              c.PlayerID.String(),
		"walletId":              c.WalletID.String(),
		"roundId":               c.RoundID,
		"gameId":                c.GameID,
		"kind":                  string(c.Kind),
		"money": map[string]any{
			"amount":   c.Money.Amount(),
			"currency": c.Money.Currency().Code(),
		},
	}
	if c.ReferenceExternalTransactionID != "" {
		fields["referenceExternalTransactionId"] = c.ReferenceExternalTransactionID
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	// encoding/json sorts map keys; strings and maps cannot fail to encode.
	_ = enc.Encode(fields)
	sum := sha256.Sum256(bytes.TrimSuffix(buf.Bytes(), []byte("\n")))
	return hex.EncodeToString(sum[:])
}

type violations []Violation

func (v *violations) add(code FailureCode, field, reason string) {
	*v = append(*v, Violation{Code: code, Field: field, Reason: reason})
}

func (v *violations) requireText(field, value string) {
	switch {
	case value == "":
		v.add(FailureMissingField, field, "is required")
	case len(value) > MaxFieldLength:
		v.add(FailureInvalidField, field, fmt.Sprintf("must be at most %d bytes", MaxFieldLength))
	}
}

func (v *violations) parseID(field, value string) uuid.UUID {
	if value == "" {
		v.add(FailureMissingField, field, "is required")
		return uuid.Nil
	}
	id, err := uuid.Parse(value)
	if err != nil || id.String() != value || id == uuid.Nil {
		v.add(FailureInvalidID, field, "must be a non-nil lowercase canonical UUID")
		return uuid.Nil
	}
	return id
}

func (v *violations) parseKind(value string) (Kind, bool) {
	k := Kind(value)
	switch {
	case value == "":
		v.add(FailureMissingField, "kind", "is required")
	case k == KindOpening:
		v.add(FailureKindNotAllowed, "kind", "OPENING is reserved for internal wallet opening")
	case !k.IsExternal():
		v.add(FailureUnknownKind, "kind", "must be one of BET, WIN, LOSS, REFUND, ROLLBACK")
	default:
		return k, true
	}
	return "", false
}

func (v *violations) parseMoney(amount, currency string) (money.Money, bool) {
	missing := false
	if amount == "" {
		v.add(FailureMissingField, "money.amount", "is required")
		missing = true
	}
	if currency == "" {
		v.add(FailureMissingField, "money.currency", "is required")
		missing = true
	}
	if missing {
		return money.Money{}, false
	}
	m, err := money.Parse(amount, currency)
	switch {
	case err == nil:
		return m, true
	case errors.Is(err, money.ErrUnsupportedCurrency):
		v.add(FailureUnsupportedCurrency, "money.currency", "must be an ISO 4217 currency with 2 decimals")
	default:
		v.add(FailureInvalidMoney, "money.amount", err.Error())
	}
	return money.Money{}, false
}
