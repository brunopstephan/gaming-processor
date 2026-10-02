// Package kctest obtains real tokens from the keycloak_test container
// (`docker compose --profile test up -d --wait keycloak_test`).
package kctest

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// BaseURL is the test Keycloak root (env KEYCLOAK_TEST_URL).
func BaseURL() string {
	if v := os.Getenv("KEYCLOAK_TEST_URL"); v != "" {
		return v
	}
	return "http://localhost:8081"
}

// IssuerURL is the wallet realm issuer.
func IssuerURL() string { return BaseURL() + "/realms/wallet" }

// JWKSURL is the wallet realm key set.
func JWKSURL() string { return IssuerURL() + "/protocol/openid-connect/certs" }

var client = &http.Client{Timeout: 10 * time.Second}

// Token returns an access token for clientID via client_credentials. The
// secret of every test client is "<clientID>-secret".
func Token(t testing.TB, clientID string) string {
	t.Helper()
	resp, err := client.PostForm(IssuerURL()+"/protocol/openid-connect/token", url.Values{
		"grant_type":    {"client_credentials"},
		"client_id":     {clientID},
		"client_secret": {clientID + "-secret"},
	})
	if err != nil {
		t.Fatalf("keycloak de teste indisponível (%v). Suba a infra: "+
			"`docker compose --profile test up -d --wait keycloak_test`", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token for %s: status %d: %s", clientID, resp.StatusCode, body)
	}
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.AccessToken == "" {
		t.Fatalf("token for %s: decode %q: %v", clientID, body, err)
	}
	return out.AccessToken
}

// Claims decodes the token payload WITHOUT verifying it (test inspection only).
func Claims(t testing.TB, token string) map[string]any {
	t.Helper()
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", token)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}
