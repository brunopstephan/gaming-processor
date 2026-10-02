// Package authtest provides a static Authenticator for HTTP handler tests:
// the bearer token is the key of a map of principals.
package authtest

import (
	"context"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
)

// Principals used by handler tests.
var (
	ProviderA = auth.Principal{ClientID: "provider-a", ProviderID: "provider-a", Scopes: map[string]bool{auth.ScopeWagering: true}}
	ProviderB = auth.Principal{ClientID: "provider-b", ProviderID: "provider-b", Scopes: map[string]bool{auth.ScopeWagering: true}}
	Internal  = auth.Principal{ClientID: "wallet-internal", Scopes: map[string]bool{auth.ScopeWallets: true, auth.ScopeWageringRead: true}}
	NoScope   = auth.Principal{ClientID: "no-scope", Scopes: map[string]bool{}}
)

// Static maps raw tokens to principals; unknown tokens are unauthenticated.
type Static map[string]auth.Principal

var _ auth.Authenticator = Static(nil)

// Authenticate implements auth.Authenticator.
func (s Static) Authenticate(_ context.Context, raw string) (auth.Principal, error) {
	p, ok := s[raw]
	if !ok {
		return auth.Principal{}, auth.ErrUnauthenticated
	}
	return p, nil
}

// Default returns tokens "provider-a", "provider-b", "internal" and "no-scope".
func Default() Static {
	return Static{"provider-a": ProviderA, "provider-b": ProviderB, "internal": Internal, "no-scope": NoScope}
}
