// Package jsonstrict decodes request payloads strictly, for HTTP bodies and
// SQS messages alike: unknown fields, trailing data and wrong JSON types are
// correctable input errors, and a numeric money amount is INVALID_MONEY
// (amounts are decimal strings).
package jsonstrict

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// Decode decodes exactly one JSON object from r into v.
func Decode(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			if strings.HasSuffix(typeErr.Field, "amount") {
				return violation(wagering.FailureInvalidMoney, typeErr.Field, "amount must be a decimal string such as \"25.00\"")
			}
			return violation(wagering.FailureInvalidField, typeErr.Field, "must be a "+typeErr.Type.String())
		}
		return violation(wagering.FailureMalformedPayload, "body", fmt.Sprintf("invalid JSON: %v", err))
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return violation(wagering.FailureMalformedPayload, "body", "unexpected data after the JSON object")
	}
	return nil
}

func violation(code wagering.FailureCode, field, reason string) *wagering.InputError {
	return &wagering.InputError{Violations: []wagering.Violation{{Code: code, Field: field, Reason: reason}}}
}
