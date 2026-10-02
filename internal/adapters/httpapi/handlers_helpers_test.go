//go:build !unit && !e2e

package httpapi_test

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/httpapi"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/authtest"
)

type apiHarness struct {
	Router  http.Handler
	H       *apptest.Harness
	Metrics *metrics.Metrics
}

func newAPI(t *testing.T) *apiHarness {
	t.Helper()
	h := apptest.New(t)
	wageringSvc, err := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	m := metrics.New()
	router := httpapi.NewRouter(httpapi.RouterDeps{
		Auth: authtest.Default(), Wallets: app.NewWalletService(h.Deps), Wagering: wageringSvc,
		Queries: app.NewQueryService(h.Deps), Reconciliation: app.NewReconciliationService(h.Deps),
		Metrics: m, Readiness: health.NewReadiness(nil), Log: slog.New(slog.DiscardHandler),
	})
	return &apiHarness{Router: router, H: h, Metrics: m}
}

func (a *apiHarness) Do(method, path, token, body string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	a.Router.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return out
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	e, _ := decode(t, rec)["error"].(map[string]any)
	code, _ := e["failureCode"].(string)
	return code
}

// openWallet opens a wallet through the API and returns its id.
func (a *apiHarness) openWallet(t *testing.T, playerID, amount string) string {
	t.Helper()
	rec := a.Do("POST", "/wallets", "internal",
		`{"playerId":"`+playerID+`","initialBalance":{"amount":"`+amount+`","currency":"BRL"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("open wallet: %d %s", rec.Code, rec.Body)
	}
	return decode(t, rec)["id"].(string)
}
