// Package auth validates OAuth 2.0 access tokens issued by the external IdP
// (Keycloak) and turns them into a Principal: the client, the provider it
// represents (provider_id claim) and its scopes.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

// jwksFetchTimeout bounds JWKS fetches; go-oidc shares an in-flight fetch with a
// non-cancellable context, so a stalled IdP would otherwise block key refresh.
const jwksFetchTimeout = 5 * time.Second

// Scopes that authorize the API.
const (
	ScopeWagering     = "wagering"      // providers: submit and read their own operations
	ScopeWageringRead = "wagering:read" // internal service: read every operation
	ScopeWallets      = "wallets"       // internal service: wallet operations
)

// ErrUnauthenticated reports a missing, malformed, expired or untrusted token.
var ErrUnauthenticated = errors.New("auth: invalid or missing access token")

// Principal is the authenticated caller.
type Principal struct {
	ClientID   string
	ProviderID string
	Scopes     map[string]bool
}

// Has reports whether the principal was granted scope.
func (p Principal) Has(scope string) bool { return p.Scopes[scope] }

// Authenticator validates a raw bearer token.
type Authenticator interface {
	Authenticate(ctx context.Context, rawToken string) (Principal, error)
}

// OIDCAuthenticator verifies RS256 signatures against the IdP key set (cached
// and refreshed on key rotation), the issuer, the audience and the expiry.
type OIDCAuthenticator struct {
	verifier *oidc.IDTokenVerifier
}

var _ Authenticator = (*OIDCAuthenticator)(nil)

// NewOIDCAuthenticator builds the verifier. Keys are fetched lazily, so the
// process starts even if the IdP is briefly unavailable; tokens are rejected
// until it is reachable.
func NewOIDCAuthenticator(cfg config.Config) (*OIDCAuthenticator, error) {
	if cfg.Auth.IssuerURL == "" {
		return nil, errors.New("auth: OIDC_ISSUER_URL is required")
	}
	keys := oidc.NewRemoteKeySet(oidc.ClientContext(context.Background(), &http.Client{Timeout: jwksFetchTimeout}), cfg.Auth.JWKSURL)
	verifier := oidc.NewVerifier(cfg.Auth.IssuerURL, keys, &oidc.Config{
		ClientID:             cfg.Auth.Audience,
		SupportedSigningAlgs: []string{oidc.RS256},
	})
	return &OIDCAuthenticator{verifier: verifier}, nil
}

// Authenticate verifies rawToken and extracts the principal.
func (a *OIDCAuthenticator) Authenticate(ctx context.Context, rawToken string) (Principal, error) {
	if rawToken == "" {
		return Principal{}, ErrUnauthenticated
	}
	token, err := a.verifier.Verify(ctx, rawToken)
	if err != nil {
		return Principal{}, fmt.Errorf("%w: %v", ErrUnauthenticated, err)
	}
	var claims struct {
		Scope      string `json:"scope"`
		ProviderID string `json:"provider_id"`
		AZP        string `json:"azp"`
	}
	if err := token.Claims(&claims); err != nil {
		return Principal{}, fmt.Errorf("%w: claims: %v", ErrUnauthenticated, err)
	}
	scopes := map[string]bool{}
	for _, s := range strings.Fields(claims.Scope) {
		scopes[s] = true
	}
	return Principal{ClientID: claims.AZP, ProviderID: claims.ProviderID, Scopes: scopes}, nil
}

// Module provides the Authenticator.
var Module = fx.Module("auth",
	fx.Provide(fx.Annotate(NewOIDCAuthenticator, fx.As(new(Authenticator)))),
)
