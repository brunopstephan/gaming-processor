package money

import (
	"fmt"
	"math"
)

// Add returns m + o. Both operands must share the currency.
func (m Money) Add(o Money) (Money, error) {
	if err := m.compatible(o); err != nil {
		return Money{}, err
	}
	r := m.minor + o.minor
	if (o.minor > 0 && r < m.minor) || (o.minor < 0 && r > m.minor) {
		return Money{}, fmt.Errorf("%w: %s + %s", ErrOverflow, m, o)
	}
	return Money{minor: r, currency: m.currency}, nil
}

// Sub returns m - o. Both operands must share the currency. The result may be
// negative (differences); non-negativity of balances is enforced by Wallet.
func (m Money) Sub(o Money) (Money, error) {
	if err := m.compatible(o); err != nil {
		return Money{}, err
	}
	r := m.minor - o.minor
	if (o.minor > 0 && r > m.minor) || (o.minor < 0 && r < m.minor) {
		return Money{}, fmt.Errorf("%w: %s - %s", ErrOverflow, m, o)
	}
	return Money{minor: r, currency: m.currency}, nil
}

// Negate returns -m.
func (m Money) Negate() (Money, error) {
	if !m.IsValid() {
		return Money{}, ErrUninitialized
	}
	if m.minor == math.MinInt64 {
		return Money{}, fmt.Errorf("%w: -(%s)", ErrOverflow, m)
	}
	return Money{minor: -m.minor, currency: m.currency}, nil
}

// Cmp returns -1, 0 or +1 when m is lower than, equal to or greater than o.
func (m Money) Cmp(o Money) (int, error) {
	if err := m.compatible(o); err != nil {
		return 0, err
	}
	switch {
	case m.minor < o.minor:
		return -1, nil
	case m.minor > o.minor:
		return 1, nil
	default:
		return 0, nil
	}
}

// Equal reports whether m and o have the same amount and currency.
func (m Money) Equal(o Money) bool { return m == o }

func (m Money) compatible(o Money) error {
	if !m.IsValid() || !o.IsValid() {
		return ErrUninitialized
	}
	if m.currency != o.currency {
		return fmt.Errorf("%w: %s vs %s", ErrCurrencyMismatch, m.currency, o.currency)
	}
	return nil
}
