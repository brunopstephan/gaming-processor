// Package wagering models external wager operations (BET, WIN, LOSS, REFUND,
// ROLLBACK) and the internal OPENING, their state machine and business rules.
package wagering

import (
	"fmt"
	"strings"
)

// FailureCode is a stable, documented reason for a rejection or failure.
type FailureCode string

// Correctable input errors: HTTP 400, never persisted.
const (
	FailureMalformedPayload      FailureCode = "MALFORMED_PAYLOAD"
	FailureMissingField          FailureCode = "MISSING_FIELD"
	FailureInvalidField          FailureCode = "INVALID_FIELD"
	FailureInvalidID             FailureCode = "INVALID_ID"
	FailureInvalidMoney          FailureCode = "INVALID_MONEY"
	FailureUnsupportedCurrency   FailureCode = "UNSUPPORTED_CURRENCY"
	FailureUnknownKind           FailureCode = "UNKNOWN_KIND"
	FailureKindNotAllowed        FailureCode = "KIND_NOT_ALLOWED"
	FailureInvalidAmountForKind  FailureCode = "INVALID_AMOUNT_FOR_KIND"
	FailureMissingReference      FailureCode = "MISSING_REFERENCE"
	FailureReferenceNotAllowed   FailureCode = "REFERENCE_NOT_ALLOWED"
	FailureMissingIdempotencyKey FailureCode = "MISSING_IDEMPOTENCY_KEY"
	FailureInvalidIdempotencyKey FailureCode = "INVALID_IDEMPOTENCY_KEY"
)

// Definitive business rejections: persisted as REJECTED, HTTP 422.
const (
	FailureInsufficientFunds            FailureCode = "INSUFFICIENT_FUNDS"
	FailureInsufficientFundsForReversal FailureCode = "INSUFFICIENT_FUNDS_FOR_REVERSAL"
	FailureWalletNotFound               FailureCode = "WALLET_NOT_FOUND"
	FailureWalletPlayerMismatch         FailureCode = "WALLET_PLAYER_MISMATCH"
	FailureCurrencyMismatch             FailureCode = "CURRENCY_MISMATCH"
	FailureReferenceNotFound            FailureCode = "REFERENCE_NOT_FOUND"
	FailureReferenceNotProcessed        FailureCode = "REFERENCE_NOT_PROCESSED"
	FailureReferenceKindInvalid         FailureCode = "REFERENCE_KIND_INVALID"
	FailureReferenceMismatch            FailureCode = "REFERENCE_MISMATCH"
	FailureAmountMismatch               FailureCode = "AMOUNT_MISMATCH"
	FailureReferenceAlreadyReversed     FailureCode = "REFERENCE_ALREADY_REVERSED"
)

// Other definitive failures.
const (
	// FailureInfrastructure marks a permanent infrastructure failure (status FAILED).
	FailureInfrastructure FailureCode = "INFRASTRUCTURE_FAILURE"
	// FailureProviderIdentityMismatch: SQS sender is not the message providerId (DLQ).
	FailureProviderIdentityMismatch FailureCode = "PROVIDER_IDENTITY_MISMATCH"
	// FailureMessageIDReused: an SQS messageId was redelivered with another payload (DLQ).
	FailureMessageIDReused FailureCode = "MESSAGE_ID_REUSED"
)

// Category tells clients whether fixing the input can succeed.
type Category string

// Categories.
const (
	CategoryCorrectable Category = "CORRECTABLE"
	CategoryDefinitive  Category = "DEFINITIVE"
)

// Category returns CORRECTABLE for input errors and DEFINITIVE otherwise.
func (c FailureCode) Category() Category {
	switch c {
	case FailureMalformedPayload, FailureMissingField, FailureInvalidField, FailureInvalidID,
		FailureInvalidMoney, FailureUnsupportedCurrency, FailureUnknownKind, FailureKindNotAllowed,
		FailureInvalidAmountForKind, FailureMissingReference, FailureReferenceNotAllowed,
		FailureMissingIdempotencyKey, FailureInvalidIdempotencyKey:
		return CategoryCorrectable
	default:
		return CategoryDefinitive
	}
}

// IsBusinessRejection reports whether c is persisted as a REJECTED transaction.
func (c FailureCode) IsBusinessRejection() bool {
	switch c {
	case FailureInsufficientFunds, FailureInsufficientFundsForReversal, FailureWalletNotFound,
		FailureWalletPlayerMismatch, FailureCurrencyMismatch, FailureReferenceNotFound,
		FailureReferenceNotProcessed, FailureReferenceKindInvalid, FailureReferenceMismatch,
		FailureAmountMismatch, FailureReferenceAlreadyReversed:
		return true
	default:
		return false
	}
}

// Violation is one invalid input field.
type Violation struct {
	Code   FailureCode
	Field  string
	Reason string
}

// InputError reports correctable input errors. Nothing is persisted for it.
type InputError struct {
	Violations []Violation
}

// Error implements error.
func (e *InputError) Error() string {
	parts := make([]string, len(e.Violations))
	for i, v := range e.Violations {
		parts[i] = fmt.Sprintf("%s: %s (%s)", v.Field, v.Reason, v.Code)
	}
	return "invalid input: " + strings.Join(parts, "; ")
}

// Code returns the failure code of the first violation.
func (e *InputError) Code() FailureCode {
	if len(e.Violations) == 0 {
		return FailureMalformedPayload
	}
	return e.Violations[0].Code
}
