//go:build !integration && !e2e

package wagering

import (
	"errors"
	"strings"
	"testing"
)

func TestFailureCodeCategories(t *testing.T) {
	correctable := []FailureCode{
		FailureMalformedPayload, FailureMissingField, FailureInvalidField, FailureInvalidID,
		FailureInvalidMoney, FailureUnsupportedCurrency, FailureUnknownKind, FailureKindNotAllowed,
		FailureInvalidAmountForKind, FailureMissingReference, FailureReferenceNotAllowed,
		FailureMissingIdempotencyKey, FailureInvalidIdempotencyKey,
	}
	business := []FailureCode{
		FailureInsufficientFunds, FailureInsufficientFundsForReversal, FailureWalletNotFound,
		FailureWalletPlayerMismatch, FailureCurrencyMismatch, FailureReferenceNotFound,
		FailureReferenceNotProcessed, FailureReferenceKindInvalid, FailureReferenceMismatch,
		FailureAmountMismatch, FailureReferenceAlreadyReversed,
	}
	other := []FailureCode{FailureInfrastructure, FailureProviderIdentityMismatch, FailureMessageIDReused}

	for _, c := range correctable {
		if c.Category() != CategoryCorrectable || c.IsBusinessRejection() {
			t.Errorf("%s: category %s business %v", c, c.Category(), c.IsBusinessRejection())
		}
	}
	for _, c := range business {
		if c.Category() != CategoryDefinitive || !c.IsBusinessRejection() {
			t.Errorf("%s: category %s business %v", c, c.Category(), c.IsBusinessRejection())
		}
	}
	for _, c := range other {
		if c.Category() != CategoryDefinitive || c.IsBusinessRejection() {
			t.Errorf("%s: category %s business %v", c, c.Category(), c.IsBusinessRejection())
		}
	}
	if FailureInsufficientFunds == FailureInsufficientFundsForReversal {
		t.Fatal("reversal without funds must have its own code")
	}
}

func TestInputError(t *testing.T) {
	var err error = &InputError{Violations: []Violation{
		{Code: FailureInvalidMoney, Field: "money.amount", Reason: "bad scale"},
		{Code: FailureMissingField, Field: "roundId", Reason: "is required"},
	}}
	var ie *InputError
	if !errors.As(err, &ie) {
		t.Fatal("errors.As must find *InputError")
	}
	if ie.Code() != FailureInvalidMoney {
		t.Fatalf("Code() = %s, want first violation code", ie.Code())
	}
	if !strings.Contains(err.Error(), "money.amount") || !strings.Contains(err.Error(), "roundId") {
		t.Fatalf("Error() = %q", err.Error())
	}
}
