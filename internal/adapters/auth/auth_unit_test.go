//go:build !integration && !e2e

package auth

import (
	"testing"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

func TestPrincipalHas(t *testing.T) {
	p := Principal{Scopes: map[string]bool{ScopeWagering: true}}
	if !p.Has(ScopeWagering) || p.Has(ScopeWallets) || (Principal{}).Has(ScopeWagering) {
		t.Fatal("Has misbehaves")
	}
}

func TestNewOIDCAuthenticatorRequiresIssuer(t *testing.T) {
	if _, err := NewOIDCAuthenticator(config.Config{Auth: config.Auth{Audience: "wallet-api"}}); err == nil {
		t.Fatal("missing OIDC_ISSUER_URL must fail at construction")
	}
}
