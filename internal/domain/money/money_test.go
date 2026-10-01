//go:build !integration && !e2e

package money

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestParseValid(t *testing.T) {
	tests := []struct {
		amount    string
		wantMinor int64
	}{
		{"0.00", 0},
		{"0.01", 1},
		{"25.00", 2500},
		{"1000.00", 100000},
		{"92233720368547758.07", math.MaxInt64},
	}
	for _, tt := range tests {
		t.Run(tt.amount, func(t *testing.T) {
			m, err := Parse(tt.amount, "BRL")
			if err != nil {
				t.Fatalf("Parse(%q) unexpected error: %v", tt.amount, err)
			}
			if m.MinorUnits() != tt.wantMinor {
				t.Fatalf("MinorUnits() = %d, want %d", m.MinorUnits(), tt.wantMinor)
			}
			if m.Currency() != BRL {
				t.Fatalf("Currency() = %v, want BRL", m.Currency())
			}
			if m.Amount() != tt.amount {
				t.Fatalf("Amount() = %q, want %q (round trip)", m.Amount(), tt.amount)
			}
		})
	}
}

func TestParseInvalid(t *testing.T) {
	tests := []struct {
		name, amount, currency string
		want                   error
	}{
		{"empty", "", "BRL", ErrInvalidAmount},
		{"no decimals", "25", "BRL", ErrInvalidAmount},
		{"one decimal", "25.0", "BRL", ErrInvalidAmount},
		{"excess scale", "25.001", "BRL", ErrInvalidAmount},
		{"leading zero", "025.00", "BRL", ErrInvalidAmount},
		{"plus sign", "+25.00", "BRL", ErrInvalidAmount},
		{"negative", "-1.00", "BRL", ErrInvalidAmount},
		{"negative zero", "-0.00", "BRL", ErrInvalidAmount},
		{"plus zero", "+0.00", "BRL", ErrInvalidAmount},
		{"scientific", "1e3", "BRL", ErrInvalidAmount},
		{"scientific decimal", "1.00e2", "BRL", ErrInvalidAmount},
		{"NaN", "NaN", "BRL", ErrInvalidAmount},
		{"Infinity", "Infinity", "BRL", ErrInvalidAmount},
		{"spaces", " 25.00", "BRL", ErrInvalidAmount},
		{"comma", "25,00", "BRL", ErrInvalidAmount},
		{"missing integer", ".50", "BRL", ErrInvalidAmount},
		{"overflow by one cent", "92233720368547758.08", "BRL", ErrOverflow},
		{"overflow integer part", "99999999999999999999.00", "BRL", ErrOverflow},
		{"bad currency", "25.00", "XXX", ErrUnsupportedCurrency},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.amount, tt.currency)
			if !errors.Is(err, tt.want) {
				t.Fatalf("Parse(%q, %q) error = %v, want %v", tt.amount, tt.currency, err, tt.want)
			}
		})
	}
}

func TestFromMinorAndFormatting(t *testing.T) {
	tests := []struct {
		minor int64
		want  string
	}{
		{0, "0.00"},
		{-50, "-0.50"},
		{-2500, "-25.00"},
		{123456, "1234.56"},
		{math.MinInt64, "-92233720368547758.08"},
	}
	for _, tt := range tests {
		m, err := FromMinor(tt.minor, BRL)
		if err != nil {
			t.Fatalf("FromMinor(%d) unexpected error: %v", tt.minor, err)
		}
		if got := m.Amount(); got != tt.want {
			t.Errorf("Amount() = %q, want %q", got, tt.want)
		}
	}
	if _, err := FromMinor(1, Currency{}); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("FromMinor with zero currency error = %v, want ErrUninitialized", err)
	}
}

func TestZeroValueIsInvalid(t *testing.T) {
	var m Money
	if m.IsValid() || m.IsZero() || m.IsPositive() || m.IsNegative() {
		t.Fatal("zero Money must be invalid and report no sign")
	}
	if _, err := json.Marshal(m); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("Marshal(zero Money) error = %v, want ErrUninitialized", err)
	}
	z, err := Zero(BRL)
	if err != nil || !z.IsValid() || !z.IsZero() {
		t.Fatalf("Zero(BRL) = %v, %v; want valid zero", z, err)
	}
}

func TestJSONRoundTrip(t *testing.T) {
	m, err := Parse("25.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `{"amount":"25.00","currency":"BRL"}` {
		t.Fatalf("Marshal = %s", b)
	}
	var back Money
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back != m {
		t.Fatalf("round trip = %v, want %v", back, m)
	}
}

func TestUnmarshalRejectsNonCanonical(t *testing.T) {
	tests := []struct {
		name, input string
	}{
		{"numeric amount", `{"amount":25.00,"currency":"BRL"}`},
		{"missing currency", `{"amount":"25.00"}`},
		{"missing amount", `{"currency":"BRL"}`},
		{"null", `null`},
		{"bad scale", `{"amount":"25.0","currency":"BRL"}`},
		{"negative", `{"amount":"-25.00","currency":"BRL"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m Money
			if err := json.Unmarshal([]byte(tt.input), &m); err == nil {
				t.Fatalf("Unmarshal(%s) succeeded with %v, want error", tt.input, m)
			}
		})
	}
}
