//go:build !unit && !e2e

package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
)

func TestOpenAndReadWallet(t *testing.T) {
	api := newAPI(t)
	player := uuid.NewString()
	rec := api.Do("POST", "/wallets", "internal",
		`{"playerId":"`+player+`","initialBalance":{"amount":"1000.00","currency":"BRL"}}`)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status %d %s", rec.Code, rec.Body)
	}
	body := decode(t, rec)
	balance := body["balance"].(map[string]any)
	if body["playerId"] != player || body["version"] != float64(1) || balance["amount"] != "1000.00" || balance["currency"] != "BRL" {
		t.Fatalf("body = %v", body)
	}
	id := body["id"].(string)

	get := api.Do("GET", "/wallets/"+id, "internal", "")
	if get.Code != 200 || decode(t, get)["id"] != id {
		t.Fatalf("get = %d %s", get.Code, get.Body)
	}
	dup := api.Do("POST", "/wallets", "internal",
		`{"playerId":"`+player+`","initialBalance":{"amount":"1.00","currency":"BRL"}}`)
	if dup.Code != http.StatusConflict || errorCode(t, dup) != "WALLET_ALREADY_EXISTS" {
		t.Fatalf("duplicate = %d %s", dup.Code, dup.Body)
	}
}

func TestWalletInputErrors(t *testing.T) {
	api := newAPI(t)
	cases := map[string]struct {
		method, path, body, code string
	}{
		"numeric amount": {"POST", "/wallets", `{"playerId":"` + uuid.NewString() + `","initialBalance":{"amount":10.00,"currency":"BRL"}}`, "INVALID_MONEY"},
		"bad scale":      {"POST", "/wallets", `{"playerId":"` + uuid.NewString() + `","initialBalance":{"amount":"10.0","currency":"BRL"}}`, "INVALID_MONEY"},
		"unknown field":  {"POST", "/wallets", `{"playerId":"` + uuid.NewString() + `","extra":1,"initialBalance":{"amount":"1.00","currency":"BRL"}}`, "MALFORMED_PAYLOAD"},
		"not json":       {"POST", "/wallets", `{`, "MALFORMED_PAYLOAD"},
		"bad player":     {"POST", "/wallets", `{"playerId":"x","initialBalance":{"amount":"1.00","currency":"BRL"}}`, "INVALID_ID"},
		"bad wallet id":  {"GET", "/wallets/NOT-A-UUID", "", "INVALID_ID"},
		"bad limit":      {"GET", "/wallets/" + uuid.NewString() + "/ledger?limit=abc", "", "INVALID_LIMIT"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			rec := api.Do(tc.method, tc.path, "internal", tc.body)
			if rec.Code != http.StatusBadRequest || errorCode(t, rec) != tc.code {
				t.Fatalf("status %d code %s body %s", rec.Code, errorCode(t, rec), rec.Body)
			}
		})
	}
	if rec := api.Do("GET", "/wallets/"+uuid.NewString(), "internal", ""); rec.Code != 404 || errorCode(t, rec) != "NOT_FOUND" {
		t.Fatalf("missing wallet = %d", rec.Code)
	}
}

func TestLedgerAndReconciliation(t *testing.T) {
	api := newAPI(t)
	id := api.openWallet(t, uuid.NewString(), "100.00")

	page := api.Do("GET", "/wallets/"+id+"/ledger?limit=1", "internal", "")
	body := decode(t, page)
	items := body["items"].([]any)
	if page.Code != 200 || len(items) != 1 || body["nextCursor"] != nil && body["nextCursor"] != "" {
		t.Fatalf("ledger page = %d %v", page.Code, body)
	}
	entry := items[0].(map[string]any)
	if entry["direction"] != "CREDIT" || entry["money"].(map[string]any)["amount"] != "100.00" ||
		entry["balanceBefore"].(map[string]any)["amount"] != "0.00" {
		t.Fatalf("entry = %v", entry)
	}

	rec := api.Do("POST", "/wallets/"+id+"/reconciliation", "internal", "")
	r := decode(t, rec)
	if rec.Code != 200 || r["consistent"] != true || r["checkedEntries"] != float64(1) || r["walletId"] != id ||
		r["difference"].(map[string]any)["amount"] != "0.00" || r["storedBalance"].(map[string]any)["amount"] != "100.00" {
		t.Fatalf("reconciliation = %d %v", rec.Code, r)
	}
}
