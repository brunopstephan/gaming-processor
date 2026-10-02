//go:build !unit && !e2e

package httpapi_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
)

type wallet struct{ id, player string }

func (a *apiHarness) newWallet(t *testing.T, amount string) wallet {
	t.Helper()
	player := uuid.NewString()
	return wallet{id: a.openWallet(t, player, amount), player: player}
}

func txBody(provider, ext string, w wallet, kind, amount, ref string) string {
	refField := ""
	if ref != "" {
		refField = fmt.Sprintf(`,"referenceExternalTransactionId":%q`, ref)
	}
	return fmt.Sprintf(`{"providerId":%q,"externalTransactionId":%q,"playerId":%q,"walletId":%q,`+
		`"roundId":"round-1","gameId":"fortune-chimp","kind":%q,"money":{"amount":%q,"currency":"BRL"}%s}`,
		provider, ext, w.player, w.id, kind, amount, refField)
}

func (a *apiHarness) post(provider, token, ext string, w wallet, kind, amount, ref string) (int, string) {
	rec := a.Do("POST", "/wagering/transactions", token, txBody(provider, ext, w, kind, amount, ref),
		"Idempotency-Key", provider+":"+ext)
	return rec.Code, rec.Body.String()
}

func TestProcessOverHTTP(t *testing.T) {
	api := newAPI(t)
	w := api.newWallet(t, "1000.00")
	ext := uuid.NewString()

	rec := api.Do("POST", "/wagering/transactions", "provider-a", txBody("provider-a", ext, w, "BET", "25.00", ""),
		"Idempotency-Key", "provider-a:"+ext, "X-Correlation-Id", "corr-bet")
	body := decode(t, rec)
	if rec.Code != 200 || body["status"] != "PROCESSED" || body["idempotentReplay"] != false ||
		body["balance"].(map[string]any)["amount"] != "975.00" {
		t.Fatalf("bet = %d %v", rec.Code, body)
	}
	id := body["transactionId"].(string)

	// Another movement, then a replay: same body, original balance, idempotentReplay true.
	if code, out := api.post("provider-a", "provider-a", uuid.NewString(), w, "BET", "5.00", ""); code != 200 {
		t.Fatalf("second bet = %d %s", code, out)
	}
	replay := api.Do("POST", "/wagering/transactions", "provider-a", txBody("provider-a", ext, w, "BET", "25.00", ""),
		"Idempotency-Key", "provider-a:"+ext)
	rb := decode(t, replay)
	if replay.Code != 200 || rb["idempotentReplay"] != true || rb["transactionId"] != id ||
		rb["balance"].(map[string]any)["amount"] != "975.00" {
		t.Fatalf("replay = %d %v", replay.Code, rb)
	}

	conflict := api.Do("POST", "/wagering/transactions", "provider-a", txBody("provider-a", ext, w, "BET", "26.00", ""),
		"Idempotency-Key", "provider-a:"+ext)
	if conflict.Code != 409 || errorCode(t, conflict) != "IDEMPOTENCY_KEY_CONFLICT" {
		t.Fatalf("key conflict = %d %s", conflict.Code, conflict.Body)
	}
	otherKey := api.Do("POST", "/wagering/transactions", "provider-a", txBody("provider-a", ext, w, "BET", "25.00", ""),
		"Idempotency-Key", "another-key")
	if otherKey.Code != 409 || errorCode(t, otherKey) != "EXTERNAL_TRANSACTION_CONFLICT" {
		t.Fatalf("external conflict = %d %s", otherKey.Code, otherKey.Body)
	}

	get := api.Do("GET", "/wagering/transactions/"+id, "provider-a", "")
	gb := decode(t, get)
	if get.Code != 200 || gb["status"] != "PROCESSED" || gb["externalTransactionId"] != ext || gb["kind"] != "BET" {
		t.Fatalf("get = %d %v", get.Code, gb)
	}
	byExt := api.Do("GET", "/providers/provider-a/wagering/transactions/"+ext, "provider-a", "")
	if byExt.Code != 200 || decode(t, byExt)["transactionId"] != id {
		t.Fatalf("by external id = %d %s", byExt.Code, byExt.Body)
	}
	if api.Metrics == nil {
		t.Fatal("metrics missing")
	}
}

func TestStatusCodesByOutcome(t *testing.T) {
	api := newAPI(t)
	w := api.newWallet(t, "10.00")

	rejected := api.Do("POST", "/wagering/transactions", "provider-a", txBody("provider-a", "rej-"+uuid.NewString(), w, "BET", "50.00", ""),
		"Idempotency-Key", "k-"+uuid.NewString())
	rb := decode(t, rejected)
	if rejected.Code != 422 || rb["status"] != "REJECTED" || rb["failureCode"] != "INSUFFICIENT_FUNDS" || rb["category"] != "DEFINITIVE" {
		t.Fatalf("rejected = %d %v", rejected.Code, rb)
	}
	if _, ok := rb["balance"]; ok {
		t.Fatal("a rejection has no balance")
	}

	pending := api.Do("POST", "/wagering/transactions", "provider-a", txBody("provider-a", "ref-"+uuid.NewString(), w, "REFUND", "5.00", "not-yet"),
		"Idempotency-Key", "k-"+uuid.NewString())
	if pending.Code != 202 || decode(t, pending)["status"] != "PENDING_REFERENCE" {
		t.Fatalf("pending = %d %s", pending.Code, pending.Body)
	}
}

func TestTransactionInputErrors(t *testing.T) {
	api := newAPI(t)
	w := api.newWallet(t, "10.00")
	ext := uuid.NewString()
	cases := map[string]struct {
		body, key, code string
	}{
		"missing key":    {txBody("provider-a", ext, w, "BET", "1.00", ""), "", "MISSING_IDEMPOTENCY_KEY"},
		"numeric amount": {`{"providerId":"provider-a","externalTransactionId":"x","playerId":"` + w.player + `","walletId":"` + w.id + `","roundId":"r","gameId":"g","kind":"BET","money":{"amount":25,"currency":"BRL"}}`, "k", "INVALID_MONEY"},
		"opening":        {txBody("provider-a", ext, w, "OPENING", "1.00", ""), "k", "KIND_NOT_ALLOWED"},
		"loss non zero":  {txBody("provider-a", ext, w, "LOSS", "1.00", ""), "k", "INVALID_AMOUNT_FOR_KIND"},
		"refund no ref":  {txBody("provider-a", ext, w, "REFUND", "1.00", ""), "k", "MISSING_REFERENCE"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			headers := []string{}
			if tc.key != "" {
				headers = append(headers, "Idempotency-Key", tc.key)
			}
			rec := api.Do("POST", "/wagering/transactions", "provider-a", tc.body, headers...)
			if rec.Code != 400 || errorCode(t, rec) != tc.code {
				t.Fatalf("status %d code %s body %s", rec.Code, errorCode(t, rec), rec.Body)
			}
		})
	}
}

func TestProviderIsolationOverHTTP(t *testing.T) {
	api := newAPI(t)
	w := api.newWallet(t, "100.00")
	ext := uuid.NewString()
	rec := api.Do("POST", "/wagering/transactions", "provider-a", txBody("provider-a", ext, w, "BET", "10.00", ""),
		"Idempotency-Key", "provider-a:"+ext)
	id := decode(t, rec)["transactionId"].(string)

	if r := api.Do("GET", "/wagering/transactions/"+id, "provider-b", ""); r.Code != 404 {
		t.Fatalf("other provider by id = %d, want 404", r.Code)
	}
	if r := api.Do("GET", "/wagering/transactions/"+id, "internal", ""); r.Code != 200 {
		t.Fatalf("internal by id = %d", r.Code)
	}
	if r := api.Do("GET", "/providers/provider-a/wagering/transactions/"+ext, "provider-b", ""); r.Code != 403 {
		t.Fatalf("provider route for another provider = %d, want 403", r.Code)
	}
	if r := api.Do("GET", "/providers/provider-b/wagering/transactions/"+ext, "provider-b", ""); r.Code != 404 {
		t.Fatalf("provider-b has no such external id = %d, want 404", r.Code)
	}

	spoof := uuid.NewString()
	r := api.Do("POST", "/wagering/transactions", "provider-b", txBody("provider-a", spoof, w, "BET", "10.00", ""),
		"Idempotency-Key", "provider-a:"+spoof)
	if r.Code != 403 || errorCode(t, r) != "FORBIDDEN" {
		t.Fatalf("spoofed providerId = %d %s", r.Code, r.Body)
	}
	if get := api.Do("GET", "/wallets/"+w.id, "internal", ""); decode(t, get)["balance"].(map[string]any)["amount"] != "90.00" {
		t.Fatal("a forbidden request must not move money")
	}
	if r := api.Do("POST", "/wagering/transactions", "internal", txBody("provider-a", uuid.NewString(), w, "BET", "1.00", ""),
		"Idempotency-Key", "x"); r.Code != 403 {
		t.Fatalf("internal client cannot submit operations = %d", r.Code)
	}
	if r := api.Do("GET", "/wagering/transactions/not-a-uuid", "provider-a", ""); r.Code != http.StatusBadRequest {
		t.Fatalf("invalid id = %d", r.Code)
	}
}
