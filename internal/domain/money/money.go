package money

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

// Money is an immutable monetary value: an amount in minor units (cents) and
// its currency. The scale is fixed at two decimals, so the representable range
// is -92233720368547758.08 to 92233720368547758.07. The zero value is
// uninitialized: it is not valid and every operation on it fails.
type Money struct {
	minor    int64
	currency Currency
}

// externalAmount is the only accepted external form: no sign, no leading
// zeros, exactly two decimals.
var externalAmount = regexp.MustCompile(`^(0|[1-9][0-9]*)\.[0-9]{2}$`)

// Parse builds Money from an external decimal string such as "25.00". Only
// the canonical form is accepted, so no normalization happens before hashing.
// Negative values, scientific notation, NaN, Infinity and any other scale are
// rejected; nothing is rounded.
func Parse(amount, currency string) (Money, error) {
	cur, err := ParseCurrency(currency)
	if err != nil {
		return Money{}, err
	}
	if !externalAmount.MatchString(amount) {
		return Money{}, fmt.Errorf("%w: %q must have the form 123.45", ErrInvalidAmount, amount)
	}
	dot := len(amount) - 3
	units, err := strconv.ParseInt(amount[:dot], 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %q", ErrOverflow, amount)
	}
	cents, err := strconv.ParseInt(amount[dot+1:], 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: %q", ErrInvalidAmount, amount)
	}
	if units > (math.MaxInt64-cents)/100 {
		return Money{}, fmt.Errorf("%w: %q", ErrOverflow, amount)
	}
	return Money{minor: units*100 + cents, currency: cur}, nil
}

// FromMinor builds Money from minor units, for example when rehydrating from
// storage. Negative values are allowed for internal calculations.
func FromMinor(minor int64, c Currency) (Money, error) {
	if c.IsZero() {
		return Money{}, ErrUninitialized
	}
	return Money{minor: minor, currency: c}, nil
}

// Zero returns zero in currency c.
func Zero(c Currency) (Money, error) { return FromMinor(0, c) }

// MinorUnits returns the amount in minor units (cents).
func (m Money) MinorUnits() int64 { return m.minor }

// Currency returns the currency.
func (m Money) Currency() Currency { return m.currency }

// IsValid reports whether m was initialized with a currency.
func (m Money) IsValid() bool { return !m.currency.IsZero() }

// IsZero reports whether m is a valid zero amount.
func (m Money) IsZero() bool { return m.IsValid() && m.minor == 0 }

// IsPositive reports whether m is a valid amount greater than zero.
func (m Money) IsPositive() bool { return m.IsValid() && m.minor > 0 }

// IsNegative reports whether m is a valid amount lower than zero.
func (m Money) IsNegative() bool { return m.IsValid() && m.minor < 0 }

// Amount formats the amount with two decimals, e.g. "25.00" or "-0.50".
func (m Money) Amount() string {
	u := uint64(m.minor)
	sign := ""
	if m.minor < 0 {
		sign = "-"
		u = -u // two's complement magnitude; correct for math.MinInt64
	}
	return fmt.Sprintf("%s%d.%02d", sign, u/100, u%100)
}

// String implements fmt.Stringer, e.g. "25.00 BRL".
func (m Money) String() string {
	if !m.IsValid() {
		return "<invalid money>"
	}
	return m.Amount() + " " + m.currency.code
}

type jsonMoney struct {
	Amount   *string `json:"amount"`
	Currency *string `json:"currency"`
}

// MarshalJSON renders {"amount":"25.00","currency":"BRL"}.
func (m Money) MarshalJSON() ([]byte, error) {
	if !m.IsValid() {
		return nil, ErrUninitialized
	}
	amount, currency := m.Amount(), m.currency.code
	return json.Marshal(jsonMoney{Amount: &amount, Currency: &currency})
}

// UnmarshalJSON accepts {"amount":"25.00","currency":"BRL"} with the strict
// rules of Parse. JSON numbers are rejected so no float is ever involved.
func (m *Money) UnmarshalJSON(b []byte) error {
	var j jsonMoney
	if err := json.Unmarshal(b, &j); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidAmount, err)
	}
	if j.Amount == nil || j.Currency == nil {
		return fmt.Errorf("%w: amount and currency are required", ErrInvalidAmount)
	}
	v, err := Parse(*j.Amount, *j.Currency)
	if err != nil {
		return err
	}
	*m = v
	return nil
}
