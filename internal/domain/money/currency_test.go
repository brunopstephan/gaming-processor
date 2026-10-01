//go:build !integration && !e2e

package money

import (
	"errors"
	"testing"
)

func TestParseCurrency(t *testing.T) {
	tests := []struct {
		name    string
		code    string
		wantErr bool
	}{
		{name: "BRL", code: "BRL"},
		{name: "USD", code: "USD"},
		{name: "EUR", code: "EUR"},
		{name: "lowercase", code: "brl", wantErr: true},
		{name: "empty", code: "", wantErr: true},
		{name: "exponent 0 (JPY)", code: "JPY", wantErr: true},
		{name: "exponent 3 (BHD)", code: "BHD", wantErr: true},
		{name: "no exponent (XAU)", code: "XAU", wantErr: true},
		{name: "unknown", code: "ZZZ", wantErr: true},
		{name: "too long", code: "BRLL", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := ParseCurrency(tt.code)
			if tt.wantErr {
				if !errors.Is(err, ErrUnsupportedCurrency) {
					t.Fatalf("ParseCurrency(%q) error = %v, want ErrUnsupportedCurrency", tt.code, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCurrency(%q) unexpected error: %v", tt.code, err)
			}
			if c.Code() != tt.code {
				t.Fatalf("Code() = %q, want %q", c.Code(), tt.code)
			}
		})
	}
}

func TestCurrencyZeroValue(t *testing.T) {
	var c Currency
	if !c.IsZero() {
		t.Fatal("zero Currency must report IsZero")
	}
	if BRL.IsZero() || USD.IsZero() {
		t.Fatal("BRL/USD must not be zero")
	}
	if BRL == USD {
		t.Fatal("BRL must differ from USD")
	}
}
