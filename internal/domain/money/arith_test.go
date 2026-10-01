//go:build !integration && !e2e

package money

import (
	"errors"
	"math"
	"testing"
)

func mustMinor(t *testing.T, minor int64, c Currency) Money {
	t.Helper()
	m, err := FromMinor(minor, c)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestAddSub(t *testing.T) {
	a, b := mustMinor(t, 10000, BRL), mustMinor(t, 8000, BRL)
	sum, err := a.Add(b)
	if err != nil || sum.MinorUnits() != 18000 {
		t.Fatalf("Add = %v, %v; want 180.00", sum, err)
	}
	diff, err := b.Sub(a)
	if err != nil || diff.MinorUnits() != -2000 || diff.Amount() != "-20.00" {
		t.Fatalf("Sub = %v, %v; want -20.00", diff, err)
	}
}

func TestOverflow(t *testing.T) {
	maxM, minM := mustMinor(t, math.MaxInt64, BRL), mustMinor(t, math.MinInt64, BRL)
	one, minusOne := mustMinor(t, 1, BRL), mustMinor(t, -1, BRL)
	tests := []struct {
		name string
		op   func() (Money, error)
	}{
		{"max+1", func() (Money, error) { return maxM.Add(one) }},
		{"min+(-1)", func() (Money, error) { return minM.Add(minusOne) }},
		{"min-1", func() (Money, error) { return minM.Sub(one) }},
		{"max-(-1)", func() (Money, error) { return maxM.Sub(minusOne) }},
		{"negate min", func() (Money, error) { return minM.Negate() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.op(); !errors.Is(err, ErrOverflow) {
				t.Fatalf("error = %v, want ErrOverflow", err)
			}
		})
	}
	if got, err := maxM.Sub(maxM); err != nil || !got.IsZero() {
		t.Fatalf("max-max = %v, %v; want 0", got, err)
	}
}

func TestNegate(t *testing.T) {
	n, err := mustMinor(t, 2500, BRL).Negate()
	if err != nil || n.MinorUnits() != -2500 {
		t.Fatalf("Negate = %v, %v", n, err)
	}
	if _, err := (Money{}).Negate(); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("Negate(zero Money) error = %v, want ErrUninitialized", err)
	}
}

func TestCurrencyMismatch(t *testing.T) {
	brl, usd := mustMinor(t, 100, BRL), mustMinor(t, 100, USD)
	if _, err := brl.Add(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Add error = %v, want ErrCurrencyMismatch", err)
	}
	if _, err := brl.Sub(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Sub error = %v, want ErrCurrencyMismatch", err)
	}
	if _, err := brl.Cmp(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("Cmp error = %v, want ErrCurrencyMismatch", err)
	}
	if brl.Equal(usd) {
		t.Fatal("Equal must be false across currencies")
	}
}

func TestUninitializedOperands(t *testing.T) {
	brl := mustMinor(t, 100, BRL)
	if _, err := brl.Add(Money{}); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("Add(zero Money) error = %v, want ErrUninitialized", err)
	}
	if _, err := (Money{}).Cmp(brl); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("Cmp on zero Money error = %v, want ErrUninitialized", err)
	}
}

func TestCmp(t *testing.T) {
	a, b := mustMinor(t, 100, BRL), mustMinor(t, 200, BRL)
	for _, tt := range []struct {
		x, y Money
		want int
	}{{a, b, -1}, {b, a, 1}, {a, a, 0}} {
		got, err := tt.x.Cmp(tt.y)
		if err != nil || got != tt.want {
			t.Errorf("Cmp(%v, %v) = %d, %v; want %d", tt.x, tt.y, got, err, tt.want)
		}
	}
}
