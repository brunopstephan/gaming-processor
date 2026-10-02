//go:build !unit && !e2e

package kctest

import (
	"slices"
	"strings"
	"testing"
)

func audiences(claims map[string]any) []string {
	switch aud := claims["aud"].(type) {
	case string:
		return []string{aud}
	case []any:
		out := make([]string, 0, len(aud))
		for _, a := range aud {
			if s, ok := a.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func TestRealmClients(t *testing.T) {
	tests := []struct {
		client      string
		wantScopes  []string
		provider    string
		hasAudience bool
	}{
		{"provider-a", []string{"wagering"}, "provider-a", true},
		{"provider-b", []string{"wagering"}, "provider-b", true},
		{"wallet-internal", []string{"wallets", "wagering:read"}, "", true},
		{"short-lived", []string{"wagering"}, "provider-a", true},
		{"no-audience", []string{"wagering"}, "provider-a", false},
	}
	for _, tt := range tests {
		t.Run(tt.client, func(t *testing.T) {
			c := Claims(t, Token(t, tt.client))
			if c["iss"] != IssuerURL() || c["azp"] != tt.client {
				t.Fatalf("iss %v azp %v", c["iss"], c["azp"])
			}
			scopes := strings.Fields(c["scope"].(string))
			for _, s := range tt.wantScopes {
				if !slices.Contains(scopes, s) {
					t.Errorf("scope %q missing from %v", s, scopes)
				}
			}
			if got, _ := c["provider_id"].(string); got != tt.provider {
				t.Errorf("provider_id %q, want %q", got, tt.provider)
			}
			if slices.Contains(audiences(c), "wallet-api") != tt.hasAudience {
				t.Errorf("aud %v, want wallet-api=%v", c["aud"], tt.hasAudience)
			}
		})
	}
	short := Claims(t, Token(t, "short-lived"))
	if exp, iat := short["exp"].(float64), short["iat"].(float64); exp-iat > 2 {
		t.Fatalf("short-lived token lives %vs, want <= 2", exp-iat)
	}
}
