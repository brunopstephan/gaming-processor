//go:build !unit && !e2e

package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
)

func authenticator(t *testing.T, issuer string) *OIDCAuthenticator {
	t.Helper()
	a, err := NewOIDCAuthenticator(config.Config{Auth: config.Auth{
		IssuerURL: issuer, JWKSURL: kctest.JWKSURL(), Audience: "wallet-api",
	}})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func TestAuthenticateValidTokens(t *testing.T) {
	a := authenticator(t, kctest.IssuerURL())
	ctx := context.Background()

	p, err := a.Authenticate(ctx, kctest.Token(t, "provider-a"))
	if err != nil || p.ProviderID != "provider-a" || p.ClientID != "provider-a" || !p.Has(ScopeWagering) || p.Has(ScopeWallets) {
		t.Fatalf("provider-a principal %+v err %v", p, err)
	}
	p, err = a.Authenticate(ctx, kctest.Token(t, "wallet-internal"))
	if err != nil || p.ProviderID != "" || !p.Has(ScopeWallets) || !p.Has(ScopeWageringRead) || p.Has(ScopeWagering) {
		t.Fatalf("internal principal %+v err %v", p, err)
	}
}

func TestAuthenticateRejectsBadTokens(t *testing.T) {
	a := authenticator(t, kctest.IssuerURL())
	ctx := context.Background()
	valid := kctest.Token(t, "provider-a")
	tampered := valid[:len(valid)-4] + "AAAA"

	cases := map[string]string{
		"empty":       "",
		"garbage":     "not-a-jwt",
		"tampered":    tampered,
		"no audience": kctest.Token(t, "no-audience"),
	}
	for name, token := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := a.Authenticate(ctx, token); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("error = %v, want ErrUnauthenticated", err)
			}
		})
	}
	if _, err := authenticator(t, "http://other-issuer/realms/wallet").Authenticate(ctx, valid); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("wrong issuer: %v", err)
	}
}

func TestAuthenticateRejectsExpiredToken(t *testing.T) {
	a := authenticator(t, kctest.IssuerURL())
	token := kctest.Token(t, "short-lived")
	time.Sleep(3 * time.Second)
	if _, err := a.Authenticate(context.Background(), token); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("expired token: %v", err)
	}
}
