//go:build !unit && !e2e

package httpapi_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/httpapi"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
)

// realAPI wires the router with the real OIDC authenticator against keycloak_test.
func realAPI(t *testing.T) *apiHarness {
	t.Helper()
	h := apptest.New(t)
	authn, err := auth.NewOIDCAuthenticator(config.Config{Auth: config.Auth{
		IssuerURL: kctest.IssuerURL(), JWKSURL: kctest.JWKSURL(), Audience: "wallet-api",
	}})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	router := httpapi.NewRouter(httpapi.RouterDeps{
		Auth: authn, Wallets: app.NewWalletService(h.Deps), Wagering: svc, Queries: app.NewQueryService(h.Deps),
		Reconciliation: app.NewReconciliationService(h.Deps), Metrics: metrics.New(),
		Readiness: health.NewReadiness(nil), Log: slog.New(slog.DiscardHandler),
	})
	return &apiHarness{Router: router, H: h}
}

func (a *apiHarness) noRow(t *testing.T, provider, key string) {
	t.Helper()
	if _, err := a.H.Transactions.FindByIdempotencyKey(context.Background(), provider, key); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("a denied request must leave no row for %s/%s: %v", provider, key, err)
	}
}

func TestRealTokensAuthenticationMatrix(t *testing.T) {
	api := realAPI(t)
	internal := kctest.Token(t, "wallet-internal")
	providerA := kctest.Token(t, "provider-a")

	player := uuid.NewString()
	open := api.Do("POST", "/wallets", internal, `{"playerId":"`+player+`","initialBalance":{"amount":"100.00","currency":"BRL"}}`)
	if open.Code != http.StatusCreated {
		t.Fatalf("internal opens wallet = %d %s", open.Code, open.Body)
	}
	w := wallet{id: decode(t, open)["id"].(string), player: player}

	expiredToken := kctest.Token(t, "short-lived")
	time.Sleep(3 * time.Second)

	denied := []struct {
		name, token string
		status      int
	}{
		{"missing token", "", 401},
		{"garbage token", "garbage", 401},
		{"tampered token", providerA[:len(providerA)-4] + "AAAA", 401},
		{"expired token", expiredToken, 401},
		{"wrong audience", kctest.Token(t, "no-audience"), 401},
		{"internal lacks wagering scope", internal, 403},
	}
	for _, tc := range denied {
		t.Run(tc.name, func(t *testing.T) {
			ext := uuid.NewString()
			rec := api.Do("POST", "/wagering/transactions", tc.token, txBody("provider-a", ext, w, "BET", "10.00", ""),
				"Idempotency-Key", "provider-a:"+ext)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d (%s)", rec.Code, tc.status, rec.Body)
			}
			api.noRow(t, "provider-a", "provider-a:"+ext)
		})
	}
	intruder := uuid.NewString()
	if rec := api.Do("POST", "/wallets", providerA, `{"playerId":"`+intruder+`","initialBalance":{"amount":"1.00","currency":"BRL"}}`); rec.Code != 403 {
		t.Fatalf("providers cannot open wallets = %d", rec.Code)
	}
	var created int64
	if err := api.H.DB.Raw("SELECT count(*) FROM wallets WHERE player_id = ?", intruder).Scan(&created).Error; err != nil {
		t.Fatal(err)
	}
	if created != 0 {
		t.Fatalf("a denied wallet creation must leave no wallet, found %d", created)
	}
	api.requireBalance(t, internal, w.id, "100.00", "denied requests must not move money")
}

// requireBalance reads the wallet as the internal client and checks its balance amount.
func (a *apiHarness) requireBalance(t *testing.T, token, walletID, want, msg string) {
	t.Helper()
	rec := a.Do("GET", "/wallets/"+walletID, token, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: read wallet = %d %s", msg, rec.Code, rec.Body)
	}
	bal, ok := decode(t, rec)["balance"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no balance object in %s", msg, rec.Body)
	}
	if bal["amount"] != want {
		t.Fatalf("%s: balance %v, want %s", msg, bal["amount"], want)
	}
}

func TestRealTokensProviderIsolation(t *testing.T) {
	api := realAPI(t)
	internal := kctest.Token(t, "wallet-internal")
	providerA, providerB := kctest.Token(t, "provider-a"), kctest.Token(t, "provider-b")
	player := uuid.NewString()
	open := api.Do("POST", "/wallets", internal, `{"playerId":"`+player+`","initialBalance":{"amount":"100.00","currency":"BRL"}}`)
	w := wallet{id: decode(t, open)["id"].(string), player: player}

	ext := uuid.NewString()
	rec := api.Do("POST", "/wagering/transactions", providerA, txBody("provider-a", ext, w, "BET", "10.00", ""),
		"Idempotency-Key", "provider-a:"+ext)
	if rec.Code != 200 {
		t.Fatalf("provider-a bet = %d %s", rec.Code, rec.Body)
	}
	id := decode(t, rec)["transactionId"].(string)

	if r := api.Do("GET", "/wagering/transactions/"+id, providerB, ""); r.Code != 404 {
		t.Fatalf("provider-b reads provider-a by id = %d, want 404", r.Code)
	}
	if r := api.Do("GET", "/providers/provider-a/wagering/transactions/"+ext, providerB, ""); r.Code != 403 {
		t.Fatalf("provider-b on provider-a route = %d, want 403", r.Code)
	}
	// A replay attempt by provider-b of provider-a's operation is refused before any lookup.
	if r := api.Do("POST", "/wagering/transactions", providerB, txBody("provider-a", ext, w, "BET", "10.00", ""),
		"Idempotency-Key", "provider-a:"+ext); r.Code != 403 {
		t.Fatalf("provider-b replaying provider-a = %d, want 403", r.Code)
	}
	api.noRow(t, "provider-b", "provider-a:"+ext)
	if r := api.Do("GET", "/wagering/transactions/"+id, internal, ""); r.Code != 200 {
		t.Fatalf("internal reads any transaction = %d", r.Code)
	}
	api.requireBalance(t, internal, w.id, "90.00", "only provider-a's bet may have moved the wallet")
}
