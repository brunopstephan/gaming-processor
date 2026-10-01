package money

import "errors"

var (
	// ErrInvalidAmount reports an amount that is not in the canonical external form.
	ErrInvalidAmount = errors.New("money: invalid amount")
	// ErrUnsupportedCurrency reports a code that is not an ISO 4217 currency with exponent 2.
	ErrUnsupportedCurrency = errors.New("money: unsupported currency")
	// ErrCurrencyMismatch reports arithmetic or comparison between different currencies.
	ErrCurrencyMismatch = errors.New("money: currency mismatch")
	// ErrOverflow reports a result outside the int64 minor-unit range.
	ErrOverflow = errors.New("money: overflow")
	// ErrUninitialized reports use of the zero Money or zero Currency.
	ErrUninitialized = errors.New("money: uninitialized value")
)
