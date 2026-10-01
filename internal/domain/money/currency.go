// Package money implements Money, an immutable value object holding an exact
// amount in minor units (int64, fixed scale of two decimals) and an ISO 4217
// currency. Money never passes through floating point.
package money

import "fmt"

// Currency is an ISO 4217 alphabetic code whose minor unit exponent is 2.
// The zero value is not a valid currency.
type Currency struct {
	code string
}

// Currencies used by the main scenarios and the currency mismatch tests.
var (
	BRL = Currency{code: "BRL"}
	USD = Currency{code: "USD"}
)

// ParseCurrency validates code against the ISO 4217 currencies with exponent 2.
// Codes are case sensitive and must be upper case.
func ParseCurrency(code string) (Currency, error) {
	if _, ok := iso4217Exponent2[code]; !ok {
		return Currency{}, fmt.Errorf("%w: %q", ErrUnsupportedCurrency, code)
	}
	return Currency{code: code}, nil
}

// Code returns the ISO 4217 alphabetic code.
func (c Currency) Code() string { return c.code }

// String implements fmt.Stringer.
func (c Currency) String() string { return c.code }

// IsZero reports whether c is the uninitialized Currency.
func (c Currency) IsZero() bool { return c.code == "" }
