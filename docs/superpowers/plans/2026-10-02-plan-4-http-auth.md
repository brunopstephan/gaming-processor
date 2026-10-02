# Plano 4 — HTTP e Autenticação: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Expor os casos de uso do Plano 3 por HTTP (Gin) com autenticação OAuth2/OIDC real via Keycloak
(`client_credentials`) e autorização por scopes e `provider_id`. Inclui:
- contrato de erros e status da spec;
- health live/ready;
- logs JSON (slog);
- métricas Prometheus implementando `app.Metrics`;
- servidor com ciclo de vida Fx e drenagem no shutdown;
- binário `cmd/wallet`.

Os testes rodam com tokens reais do `keycloak_test`.

**Architecture:**
- **Autenticação:** `internal/adapters/auth` valida JWTs com go-oidc (JWKS remoto com cache e rotação, `iss`,
  `aud=wallet-api`, `exp` e RS256) e produz um `Principal{ClientID, ProviderID, Scopes}`.
- **HTTP:** `internal/adapters/httpapi` tem os middlewares (correlation id, recovery, log, métricas, autenticação e
  scope), os handlers finos sobre os serviços de `internal/app` e o mapeamento de erros. Os testes de handler usam um
  autenticador estático e os testes de autorização usam o Keycloak real.
- **Plataforma:** `internal/platform/metrics` (Prometheus, registry próprio), `internal/platform/logging` (slog JSON),
  `internal/platform/health` (checks de readiness por grupo Fx) e `internal/platform/composition`, a raiz de
  composição usada por `cmd/wallet` e pelos testes.

**Tech Stack:** Go 1.25.11, Gin v1.12, `github.com/coreos/go-oidc/v3` (v3.21), `github.com/prometheus/client_golang` (v1.24), Uber Fx, Keycloak `quay.io/keycloak/keycloak:26.6`, slog.

**Spec:** `docs/superpowers/specs/2026-10-01-wallet-service-design.md` (seções 9, 10, 13 e 14). Enunciado: `docs/CHALLENGE.md`
(seções 2, 9, 12 e 13). As obrigações herdadas estão na memória do projeto (mapeamento de erros).

## Roadmap

1. ~~Domínio~~ 2. ~~Persistência~~ 3. ~~Casos de uso~~ 4. **HTTP e auth** (este)
5. **Mensageria:** MiniStack, consumer SQS com inbox via `alongside`, outbox publisher, worker de referências, DLQ e
   checks de readiness do SQS.
6. **Entrega:** Dockerfile, app no compose com nginx e 3 réplicas, Prometheus/Grafana, E2E multi-processo e docs.

## Global Constraints

- Módulo `github.com/brunopstephan/backend-challenge-go`, diretiva `go 1.25.11`. `internal/domain/**` não muda.
  `internal/app` não importa Gin, go-oidc, prometheus, GORM, AWS nem adapters.
- Dinheiro nunca passa por float. No JSON de entrada, `amount` numérico é rejeitado com `INVALID_MONEY`.
- **Autorização** (spec §9):

  | Rota | Scope exigido | Regra de provedor |
  | --- | --- | --- |
  | `POST /wagering/transactions` | `wagering` | `providerId` do corpo == `provider_id` do token, senão 403 sem efeito |
  | `GET /wagering/transactions/:transactionId` | `wagering` (só as próprias) ou `wagering:read` (interno, todas) | transação de outro provedor → 404 |
  | `GET /providers/:providerId/wagering/transactions/:externalTransactionId` | `wagering` | `:providerId` == token, senão 403 |
  | `/wallets/**` | `wallets` | — |
  | `/health/*`, `/metrics` | públicos | — |

  Token ausente, inválido, expirado ou com audiência errada → 401 com `WWW-Authenticate: Bearer`. Scope ausente → 403.
- **Status e corpos** (spec §10):

  | Situação | Status | Corpo |
  | --- | --- | --- |
  | PROCESSED | 200 | `{transactionId, status, balance, idempotentReplay}` |
  | PENDING_REFERENCE | 202 | `{transactionId, status, idempotentReplay}` |
  | REJECTED | 422 | `{transactionId, status, failureCode, category, idempotentReplay}` |
  | FAILED | 500 | `{transactionId, status, failureCode, category, idempotentReplay}` |
  | Entrada inválida | 400 | `{"error":{"failureCode","category":"CORRECTABLE","message","details":[{field,code,reason}]}}` |
  | Conflito | 409 | `IDEMPOTENCY_KEY_CONFLICT`, `EXTERNAL_TRANSACTION_CONFLICT` ou `WALLET_ALREADY_EXISTS` |
  | Não encontrado | 404 | `NOT_FOUND` |
  | Transitório | 503 | `TEMPORARILY_UNAVAILABLE`, com `Retry-After: 1` |
  | Autenticação / autorização | 401 / 403 | `UNAUTHENTICATED` / `FORBIDDEN` |
  | Outros | 500 | `INTERNAL_ERROR`, sem detalhes |

  O mapeamento confere `app.ErrTransient` **primeiro**.
- `POST /wallets` responde **201**.
- `Idempotency-Key` é obrigatório: ausente → 400 `MISSING_IDEMPOTENCY_KEY`. A chave nunca é substituída.
- Logs JSON por requisição com `correlationId`, `method`, `route`, `status`, `latencyMs` e `providerId` quando
  houver. Nunca o token nem o corpo.
- Tags de build:
  - teste unitário: `//go:build !integration && !e2e`;
  - teste de integração: `//go:build !unit && !e2e`.

  Infra de teste: `docker compose --profile test up -d --wait postgres_test keycloak_test && docker compose --profile
  test run --rm migrate_test`. Os testes falham com instrução clara se a infra não responder.
- Testes só com `testing`, nunca `t.Fatal` fora da goroutine do teste. Código formatado com `gofmt`, e
  `go vet ./...` limpo.
- Toda mensagem de commit termina exatamente com `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. Uma requisição negada (401 ou 403) não deixa efeito financeiro nem linha no banco. Teste na Task 9.
2. Um provedor nunca vê transação de outro, nem por id, nem pela rota de provedor, nem por replay. Testes nas Tasks 7
   e 9.
3. O replay HTTP devolve o mesmo corpo que a resposta original, inclusive o saldo. Teste na Task 7.
4. No shutdown, o readiness passa a 503 antes de o servidor parar. Teste na Task 8.
5. `amount` como número JSON (`25.00` sem aspas) é rejeitado, nunca convertido. Teste na Task 6.

## File Structure

```
docker-compose.yml                       (modify) + keycloak, keycloak_test
deploy/keycloak/realm-wallet.json        realm, scopes, audience, clientes
internal/testsupport/kctest/kctest.go    URLs, Token(), Claims()
internal/testsupport/authtest/authtest.go Static authenticator para testes de handler
internal/platform/config/config.go       (modify) + HTTP, Auth, Log
internal/platform/logging/logging.go     slog JSON + módulo
internal/platform/metrics/metrics.go     Prometheus, implementa app.Metrics + HTTP + módulo
internal/platform/health/health.go       Check, Readiness (drenagem)
internal/adapters/auth/auth.go           Principal, Authenticator, OIDCAuthenticator, módulo
internal/adapters/postgres/module.go     (modify) + health.Check do Postgres
internal/adapters/httpapi/
  errors.go, middleware.go, decode.go, dto.go, router.go, health_handlers.go,
  wallet_handlers.go, transaction_handlers.go, server.go, module.go
internal/platform/composition/composition.go  Modules()
cmd/wallet/main.go
```

---

### Task 1: Keycloak no compose, realm provisionado e suporte de teste

**Files:**
- Modify: `docker-compose.yml`
- Create: `deploy/keycloak/realm-wallet.json`, `internal/testsupport/kctest/kctest.go`
- Test: `internal/testsupport/kctest/kctest_test.go` (integration)

**Interfaces:**
- Produces:
  - Serviços `keycloak` (dev, `${KEYCLOAK_PORT:-8080}`) e `keycloak_test` (profile `test`, `${KEYCLOAK_TEST_PORT:-8081}`).
  - Realm `wallet`, com os clientes `provider-a`, `provider-b`, `wallet-internal`, `short-lived` e `no-audience`. O
    secret de cada um é `<clientId>-secret`.
  - Helpers de teste:
    - `kctest.BaseURL()`, `IssuerURL()` e `JWKSURL() string`;
    - `kctest.Token(t, clientID string) string`;
    - `kctest.Claims(t, token string) map[string]any`, que decodifica o payload sem verificar a assinatura.

- [ ] **Step 1: Criar o realm**

`deploy/keycloak/realm-wallet.json`:

```json
{
  "realm": "wallet",
  "enabled": true,
  "accessTokenLifespan": 300,
  "defaultDefaultClientScopes": [],
  "defaultOptionalClientScopes": [],
  "clientScopes": [
    { "name": "wagering", "protocol": "openid-connect",
      "attributes": { "include.in.token.scope": "true", "display.on.consent.screen": "false" } },
    { "name": "wagering:read", "protocol": "openid-connect",
      "attributes": { "include.in.token.scope": "true", "display.on.consent.screen": "false" } },
    { "name": "wallets", "protocol": "openid-connect",
      "attributes": { "include.in.token.scope": "true", "display.on.consent.screen": "false" } },
    { "name": "wallet-api-audience", "protocol": "openid-connect",
      "attributes": { "include.in.token.scope": "false", "display.on.consent.screen": "false" },
      "protocolMappers": [
        { "name": "wallet-api-audience", "protocol": "openid-connect", "protocolMapper": "oidc-audience-mapper",
          "consentRequired": false,
          "config": { "included.custom.audience": "wallet-api", "access.token.claim": "true",
                      "id.token.claim": "false", "introspection.token.claim": "true" } }
      ] }
  ],
  "clients": [
    { "clientId": "provider-a", "enabled": true, "protocol": "openid-connect", "publicClient": false,
      "clientAuthenticatorType": "client-secret", "secret": "provider-a-secret",
      "serviceAccountsEnabled": true, "standardFlowEnabled": false, "implicitFlowEnabled": false,
      "directAccessGrantsEnabled": false, "fullScopeAllowed": false,
      "defaultClientScopes": ["wagering", "wallet-api-audience"], "optionalClientScopes": [],
      "protocolMappers": [
        { "name": "provider-id", "protocol": "openid-connect", "protocolMapper": "oidc-hardcoded-claim-mapper",
          "consentRequired": false,
          "config": { "claim.name": "provider_id", "claim.value": "provider-a", "jsonType.label": "String",
                      "access.token.claim": "true", "id.token.claim": "false",
                      "introspection.token.claim": "true", "userinfo.token.claim": "false" } }
      ] },
    { "clientId": "provider-b", "enabled": true, "protocol": "openid-connect", "publicClient": false,
      "clientAuthenticatorType": "client-secret", "secret": "provider-b-secret",
      "serviceAccountsEnabled": true, "standardFlowEnabled": false, "implicitFlowEnabled": false,
      "directAccessGrantsEnabled": false, "fullScopeAllowed": false,
      "defaultClientScopes": ["wagering", "wallet-api-audience"], "optionalClientScopes": [],
      "protocolMappers": [
        { "name": "provider-id", "protocol": "openid-connect", "protocolMapper": "oidc-hardcoded-claim-mapper",
          "consentRequired": false,
          "config": { "claim.name": "provider_id", "claim.value": "provider-b", "jsonType.label": "String",
                      "access.token.claim": "true", "id.token.claim": "false",
                      "introspection.token.claim": "true", "userinfo.token.claim": "false" } }
      ] },
    { "clientId": "wallet-internal", "enabled": true, "protocol": "openid-connect", "publicClient": false,
      "clientAuthenticatorType": "client-secret", "secret": "wallet-internal-secret",
      "serviceAccountsEnabled": true, "standardFlowEnabled": false, "implicitFlowEnabled": false,
      "directAccessGrantsEnabled": false, "fullScopeAllowed": false,
      "defaultClientScopes": ["wallets", "wagering:read", "wallet-api-audience"], "optionalClientScopes": [] },
    { "clientId": "short-lived", "enabled": true, "protocol": "openid-connect", "publicClient": false,
      "clientAuthenticatorType": "client-secret", "secret": "short-lived-secret",
      "serviceAccountsEnabled": true, "standardFlowEnabled": false, "implicitFlowEnabled": false,
      "directAccessGrantsEnabled": false, "fullScopeAllowed": false,
      "attributes": { "access.token.lifespan": "2" },
      "defaultClientScopes": ["wagering", "wallet-api-audience"], "optionalClientScopes": [],
      "protocolMappers": [
        { "name": "provider-id", "protocol": "openid-connect", "protocolMapper": "oidc-hardcoded-claim-mapper",
          "consentRequired": false,
          "config": { "claim.name": "provider_id", "claim.value": "provider-a", "jsonType.label": "String",
                      "access.token.claim": "true", "id.token.claim": "false",
                      "introspection.token.claim": "true", "userinfo.token.claim": "false" } }
      ] },
    { "clientId": "no-audience", "enabled": true, "protocol": "openid-connect", "publicClient": false,
      "clientAuthenticatorType": "client-secret", "secret": "no-audience-secret",
      "serviceAccountsEnabled": true, "standardFlowEnabled": false, "implicitFlowEnabled": false,
      "directAccessGrantsEnabled": false, "fullScopeAllowed": false,
      "defaultClientScopes": ["wagering"], "optionalClientScopes": [],
      "protocolMappers": [
        { "name": "provider-id", "protocol": "openid-connect", "protocolMapper": "oidc-hardcoded-claim-mapper",
          "consentRequired": false,
          "config": { "claim.name": "provider_id", "claim.value": "provider-a", "jsonType.label": "String",
                      "access.token.claim": "true", "id.token.claim": "false",
                      "introspection.token.claim": "true", "userinfo.token.claim": "false" } }
      ] }
  ],
  "users": [
    { "username": "service-account-provider-a", "enabled": true, "serviceAccountClientId": "provider-a" },
    { "username": "service-account-provider-b", "enabled": true, "serviceAccountClientId": "provider-b" },
    { "username": "service-account-wallet-internal", "enabled": true, "serviceAccountClientId": "wallet-internal" },
    { "username": "service-account-short-lived", "enabled": true, "serviceAccountClientId": "short-lived" },
    { "username": "service-account-no-audience", "enabled": true, "serviceAccountClientId": "no-audience" }
  ]
}
```

- [ ] **Step 2: Acrescentar o Keycloak ao compose**

Em `docker-compose.yml`, acrescentar os serviços abaixo. O healthcheck usa `/dev/tcp`, porque a imagem não tem curl:

```yaml
  keycloak:
    image: quay.io/keycloak/keycloak:26.6
    command: ["start-dev", "--import-realm", "--health-enabled=true"]
    environment:
      KC_BOOTSTRAP_ADMIN_USERNAME: admin
      KC_BOOTSTRAP_ADMIN_PASSWORD: ${KEYCLOAK_ADMIN_PASSWORD:-admin}
      KC_HOSTNAME: http://localhost:${KEYCLOAK_PORT:-8080}
      KC_HOSTNAME_BACKCHANNEL_DYNAMIC: "true"
    ports:
      - "${KEYCLOAK_PORT:-8080}:8080"
    volumes:
      - ./deploy/keycloak:/opt/keycloak/data/import:ro
    healthcheck: &keycloak-healthcheck
      test: ["CMD-SHELL", "exec 3<>/dev/tcp/127.0.0.1/9000 && printf 'GET /health/ready HTTP/1.1\\r\\nHost: localhost\\r\\nConnection: close\\r\\n\\r\\n' >&3 && grep -q UP <&3"]
      interval: 5s
      timeout: 5s
      retries: 40
      start_period: 20s

  keycloak_test:
    image: quay.io/keycloak/keycloak:26.6
    profiles: ["test"]
    command: ["start-dev", "--import-realm", "--health-enabled=true"]
    environment:
      KC_BOOTSTRAP_ADMIN_USERNAME: admin
      KC_BOOTSTRAP_ADMIN_PASSWORD: admin
      KC_HOSTNAME: http://localhost:${KEYCLOAK_TEST_PORT:-8081}
      KC_HOSTNAME_BACKCHANNEL_DYNAMIC: "true"
    ports:
      - "${KEYCLOAK_TEST_PORT:-8081}:8080"
    volumes:
      - ./deploy/keycloak:/opt/keycloak/data/import:ro
    healthcheck: *keycloak-healthcheck
```

- [ ] **Step 3: Criar o suporte de teste**

`internal/testsupport/kctest/kctest.go`:

```go
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
```

- [ ] **Step 4: Escrever o teste do realm**

`internal/testsupport/kctest/kctest_test.go`:

```go
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
```

- [ ] **Step 5: Subir e rodar**

Run:

```bash
docker compose config --quiet && docker compose --profile test up -d --wait keycloak_test
go test -count=1 -tags=integration ./internal/testsupport/kctest/...
```

Expected: `ok`. Se a imagem `26.6` não existir, use a tag 26.x mais recente que exista e registre a troca no relatório.
Se o import do realm falhar, pegue o motivo nos logs (`docker compose logs keycloak_test`), ajuste o JSON pelo mínimo
necessário e reporte o que mudou. Os testes do Plano 1 a 3 continuam passando com `go test ./...`.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./...
git add docker-compose.yml deploy/keycloak internal/testsupport/kctest
git commit -m "chore(auth): keycloak dev/test with provisioned wallet realm and clients" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Configuração de HTTP, auth e log; logger slog JSON

**Files:**
- Modify: `internal/platform/config/config.go`, `internal/platform/config/config_test.go`
- Create: `internal/platform/logging/logging.go`
- Test: `internal/platform/logging/logging_test.go` (unit)

**Interfaces:**
- Produces:
  - `config.HTTP{Addr string; ShutdownTimeout time.Duration}`, com as variáveis `HTTP_ADDR` (padrão `:8080`) e
    `HTTP_SHUTDOWN_TIMEOUT` (padrão 15s).
  - `config.Auth{IssuerURL, JWKSURL, Audience string}`, com as variáveis `OIDC_ISSUER_URL` (opcional no Load; o
    módulo de auth exige), `OIDC_JWKS_URL` (padrão o issuer + `/protocol/openid-connect/certs`) e `OIDC_AUDIENCE`
    (padrão `wallet-api`).
  - `config.Log{Level slog.Level}`, com a variável `LOG_LEVEL` (`debug`, `info`, `warn` ou `error`; padrão `info`).
  - `logging.New(w io.Writer, level slog.Level) *slog.Logger` e `logging.Module`, que fornece `*slog.Logger` em
    stdout.

- [ ] **Step 1: Escrever os testes que falham**

Acrescentar a `internal/platform/config/config_test.go`:

```go
func TestLoadHTTPAuthLogDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":8080" || cfg.HTTP.ShutdownTimeout != 15*time.Second {
		t.Fatalf("HTTP = %+v", cfg.HTTP)
	}
	if cfg.Auth.IssuerURL != "" || cfg.Auth.Audience != "wallet-api" || cfg.Log.Level != slog.LevelInfo {
		t.Fatalf("Auth %+v Log %+v", cfg.Auth, cfg.Log)
	}
}

func TestLoadAuthDerivesJWKS(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "OIDC_ISSUER_URL": "http://kc/realms/wallet",
		"HTTP_ADDR": "127.0.0.1:0", "LOG_LEVEL": "DEBUG",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.JWKSURL != "http://kc/realms/wallet/protocol/openid-connect/certs" ||
		cfg.HTTP.Addr != "127.0.0.1:0" || cfg.Log.Level != slog.LevelDebug {
		t.Fatalf("cfg = %+v", cfg)
	}
	cfg, _ = Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "OIDC_ISSUER_URL": "http://a", "OIDC_JWKS_URL": "http://internal/certs",
	}))
	if cfg.Auth.JWKSURL != "http://internal/certs" {
		t.Fatalf("explicit JWKS ignored: %s", cfg.Auth.JWKSURL)
	}
}

func TestLoadInvalidLogLevelAndTimeout(t *testing.T) {
	_, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x", "LOG_LEVEL": "loud", "HTTP_SHUTDOWN_TIMEOUT": "0s"}))
	if err == nil || !strings.Contains(err.Error(), "LOG_LEVEL") || !strings.Contains(err.Error(), "HTTP_SHUTDOWN_TIMEOUT") {
		t.Fatalf("error = %v", err)
	}
}
```

(acrescente `log/slog` aos imports).

`internal/platform/logging/logging_test.go`:

```go
//go:build !integration && !e2e

package logging

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"
)

func TestNewWritesJSONAtLevel(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, slog.LevelInfo)
	log.Debug("hidden")
	log.Info("visible", "correlationId", "c-1")
	var rec map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(buf.Bytes()), &rec); err != nil {
		t.Fatalf("not one JSON line: %q (%v)", buf.String(), err)
	}
	if rec["msg"] != "visible" || rec["correlationId"] != "c-1" || rec["level"] != "INFO" {
		t.Fatalf("record = %v", rec)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test -tags=unit ./internal/platform/...`
Expected: FAIL de compilação, `cfg.HTTP undefined` e `undefined: New`.

- [ ] **Step 3: Implementar**

Em `config.go`, acrescentar ao `Config` os campos `HTTP HTTP`, `Auth Auth` e `Log Log`, os tipos e o carregamento.
Em `Load`, antes do `if len(errs) > 0`:

```go
	httpCfg := HTTP{
		Addr:            envOr(getenv, "HTTP_ADDR", ":8080"),
		ShutdownTimeout: positiveDuration(getenv, "HTTP_SHUTDOWN_TIMEOUT", 15*time.Second, &errs),
	}
	authCfg := Auth{
		IssuerURL: getenv("OIDC_ISSUER_URL"),
		JWKSURL:   getenv("OIDC_JWKS_URL"),
		Audience:  envOr(getenv, "OIDC_AUDIENCE", "wallet-api"),
	}
	if authCfg.JWKSURL == "" && authCfg.IssuerURL != "" {
		authCfg.JWKSURL = strings.TrimRight(authCfg.IssuerURL, "/") + "/protocol/openid-connect/certs"
	}
	logCfg := Log{Level: logLevel(getenv, &errs)}
```

e retornar `Config{Database: db, Wagering: wagering, HTTP: httpCfg, Auth: authCfg, Log: logCfg}`. Tipos e helpers
(acrescente os imports `log/slog` e `strings`):

```go
// HTTP configures the API server.
type HTTP struct {
	Addr            string
	ShutdownTimeout time.Duration
}

// Auth configures OIDC access-token validation. IssuerURL is required by the
// auth module (the HTTP API cannot start without it); JWKSURL may point to an
// internal host while IssuerURL is the public issuer in the tokens.
type Auth struct {
	IssuerURL string
	JWKSURL   string
	Audience  string
}

// Log configures the structured logger.
type Log struct {
	Level slog.Level
}

func envOr(getenv func(string) string, key, fallback string) string {
	if v := getenv(key); v != "" {
		return v
	}
	return fallback
}

func logLevel(getenv func(string) string, errs *[]error) slog.Level {
	switch strings.ToLower(getenv("LOG_LEVEL")) {
	case "", "info":
		return slog.LevelInfo
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		*errs = append(*errs, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", getenv("LOG_LEVEL")))
		return slog.LevelInfo
	}
}
```

`internal/platform/logging/logging.go`:

```go
// Package logging builds the JSON structured logger.
package logging

import (
	"io"
	"log/slog"
	"os"

	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

// New returns a JSON slog.Logger writing to w at level.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// Module provides the process logger (stdout, JSON).
var Module = fx.Module("logging",
	fx.Provide(func(cfg config.Config) *slog.Logger { return New(os.Stdout, cfg.Log.Level) }),
)
```

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=unit ./internal/platform/... && go test -race ./internal/platform/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/platform
git commit -m "feat(platform): http, oidc and log configuration with a JSON slog logger" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Autenticação OIDC

**Files:**
- Create: `internal/adapters/auth/auth.go`, `internal/testsupport/authtest/authtest.go`
- Test: `internal/adapters/auth/auth_unit_test.go` (unit), `internal/adapters/auth/auth_test.go` (integration)

**Interfaces:**
- Consumes: `config.Auth`; `kctest.*`.
- Produces:
  - Tipos: `auth.Principal{ClientID, ProviderID string; Scopes map[string]bool}`, com `(Principal) Has(scope string)
    bool`; e `auth.Authenticator{ Authenticate(ctx context.Context, rawToken string) (Principal, error) }`.
  - Erro e scopes: `auth.ErrUnauthenticated` e as constantes `ScopeWagering`, `ScopeWageringRead` e `ScopeWallets`.
  - Implementação real: `auth.NewOIDCAuthenticator(cfg config.Config) (*OIDCAuthenticator, error)`, que exige
    `IssuerURL`, e `auth.Module`, que fornece `Authenticator`.
  - Para testes de handler: `authtest.Static` (`map[string]auth.Principal` que implementa `Authenticator`) e os
    principals prontos `authtest.ProviderA`, `ProviderB`, `Internal` e `NoScope`.

- [ ] **Step 1: Adicionar a dependência**

```bash
go get github.com/coreos/go-oidc/v3@v3.21.0
```

- [ ] **Step 2: Escrever os testes que falham**

`internal/adapters/auth/auth_unit_test.go`:

```go
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
```

`internal/adapters/auth/auth_test.go`:

```go
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
		"empty":        "",
		"garbage":      "not-a-jwt",
		"tampered":     tampered,
		"no audience":  kctest.Token(t, "no-audience"),
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
```

- [ ] **Step 3: Rodar e confirmar que falham**

Run: `go test -tags=unit ./internal/adapters/auth/...`
Expected: FAIL de compilação, `undefined: Principal`.

- [ ] **Step 4: Implementar**

`internal/adapters/auth/auth.go`:

```go
// Package auth validates OAuth 2.0 access tokens issued by the external IdP
// (Keycloak) and turns them into a Principal: the client, the provider it
// represents (provider_id claim) and its scopes.
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

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
	keys := oidc.NewRemoteKeySet(context.Background(), cfg.Auth.JWKSURL)
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
```

`internal/testsupport/authtest/authtest.go`:

```go
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
```

- [ ] **Step 5: Rodar os testes**

Run: `go test -race -tags=unit ./internal/adapters/auth/... && go test -race -count=1 -tags=integration ./internal/adapters/auth/...`
Expected: `ok`. O teste de expiração leva cerca de 3s.

- [ ] **Step 6: Commit**

```bash
go mod tidy && gofmt -l . && go vet ./...
git add go.mod go.sum internal/adapters/auth internal/testsupport/authtest
git commit -m "feat(auth): OIDC access-token validation with scopes and provider identity" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Métricas Prometheus

**Files:**
- Create: `internal/platform/metrics/metrics.go`
- Test: `internal/platform/metrics/metrics_test.go` (unit)

**Interfaces:**
- Consumes: `app.Metrics`, `app.Channel`, `wagering.Kind`/`Status`.
- Produces:
  - `metrics.New() *Metrics`, que implementa `app.Metrics`, com os campos `Registry *prometheus.Registry`,
    `HTTPRequests *prometheus.CounterVec{method,route,status}` e
    `HTTPDuration *prometheus.HistogramVec{method,route}`.
  - `metrics.Module`, que fornece `*Metrics` e `app.Metrics`.
  - Nomes (spec §14): `wager_transactions_total{kind,status,channel}`, `idempotent_replays_total{channel}`,
    `concurrency_conflicts_total{type}`, `processing_duration_seconds{channel}`, `reconciliation_divergences_total`,
    `http_requests_total{method,route,status}` e `http_request_duration_seconds{method,route}`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/platform/metrics/metrics_test.go`:

```go
//go:build !integration && !e2e

package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

func TestMetricsImplementAppMetrics(t *testing.T) {
	m := New()
	var _ app.Metrics = m
	m.TransactionCompleted(wagering.KindBet, wagering.StatusProcessed, app.ChannelHTTP)
	m.TransactionCompleted(wagering.KindBet, wagering.StatusProcessed, app.ChannelHTTP)
	m.IdempotentReplay(app.ChannelSQS)
	m.ConcurrencyConflict(app.ConflictLockTimeout)
	m.ProcessingDuration(app.ChannelHTTP, 15*time.Millisecond)
	m.ReconciliationDivergence()

	if got := testutil.ToFloat64(m.transactions.WithLabelValues("BET", "PROCESSED", "http")); got != 2 {
		t.Fatalf("wager_transactions_total = %v", got)
	}
	if testutil.ToFloat64(m.replays.WithLabelValues("sqs")) != 1 ||
		testutil.ToFloat64(m.conflicts.WithLabelValues("lock_timeout")) != 1 ||
		testutil.ToFloat64(m.divergences) != 1 {
		t.Fatal("counters not recorded")
	}

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, want := range []string{
		"wager_transactions_total", "idempotent_replays_total", "concurrency_conflicts_total",
		"processing_duration_seconds", "reconciliation_divergences_total", "go_goroutines",
	} {
		if !names[want] {
			t.Errorf("metric %s not registered", want)
		}
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go get github.com/prometheus/client_golang@v1.24.1 && go test -tags=unit ./internal/platform/metrics/...`
Expected: FAIL de compilação, `undefined: New`.

- [ ] **Step 3: Implementar**

`internal/platform/metrics/metrics.go`:

```go
// Package metrics exposes Prometheus metrics on a dedicated registry and
// implements app.Metrics.
package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// Metrics holds every collector of the service.
type Metrics struct {
	Registry     *prometheus.Registry
	HTTPRequests *prometheus.CounterVec
	HTTPDuration *prometheus.HistogramVec

	transactions *prometheus.CounterVec
	replays      *prometheus.CounterVec
	conflicts    *prometheus.CounterVec
	processing   *prometheus.HistogramVec
	divergences  prometheus.Counter
}

var _ app.Metrics = (*Metrics)(nil)

// New registers all collectors on a fresh registry.
func New() *Metrics {
	m := &Metrics{
		Registry: prometheus.NewRegistry(),
		HTTPRequests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total", Help: "HTTP requests by method, route and status.",
		}, []string{"method", "route", "status"}),
		HTTPDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "http_request_duration_seconds", Help: "HTTP request latency.", Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		transactions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "wager_transactions_total", Help: "Completed wager operations by kind, status and channel.",
		}, []string{"kind", "status", "channel"}),
		replays: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "idempotent_replays_total", Help: "Operations answered from the persisted result.",
		}, []string{"channel"}),
		conflicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "concurrency_conflicts_total", Help: "Transient concurrency conflicts by type.",
		}, []string{"type"}),
		processing: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "processing_duration_seconds", Help: "Wager processing latency by channel.", Buckets: prometheus.DefBuckets,
		}, []string{"channel"}),
		divergences: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "reconciliation_divergences_total", Help: "Reconciliations whose ledger disagrees with the balance.",
		}),
	}
	m.Registry.MustRegister(
		collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.HTTPRequests, m.HTTPDuration, m.transactions, m.replays, m.conflicts, m.processing, m.divergences,
	)
	return m
}

// TransactionCompleted implements app.Metrics.
func (m *Metrics) TransactionCompleted(kind wagering.Kind, status wagering.Status, channel app.Channel) {
	m.transactions.WithLabelValues(string(kind), string(status), string(channel)).Inc()
}

// IdempotentReplay implements app.Metrics.
func (m *Metrics) IdempotentReplay(channel app.Channel) { m.replays.WithLabelValues(string(channel)).Inc() }

// ConcurrencyConflict implements app.Metrics.
func (m *Metrics) ConcurrencyConflict(reason string) { m.conflicts.WithLabelValues(reason).Inc() }

// ProcessingDuration implements app.Metrics.
func (m *Metrics) ProcessingDuration(channel app.Channel, d time.Duration) {
	m.processing.WithLabelValues(string(channel)).Observe(d.Seconds())
}

// ReconciliationDivergence implements app.Metrics.
func (m *Metrics) ReconciliationDivergence() { m.divergences.Inc() }

// Module provides *Metrics and app.Metrics.
var Module = fx.Module("metrics",
	fx.Provide(New, func(m *Metrics) app.Metrics { return m }),
)
```

- [ ] **Step 4: Rodar e commit**

Run: `go test -race -tags=unit ./internal/platform/metrics/...` (expected `ok`)

```bash
go mod tidy && gofmt -l . && go vet ./...
git add go.mod go.sum internal/platform/metrics
git commit -m "feat(metrics): prometheus metrics implementing the application metrics port" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Fundação HTTP — erros, middlewares, decodificação, health e router

**Files:**
- Create: `internal/platform/health/health.go`, `internal/adapters/httpapi/errors.go`, `internal/adapters/httpapi/middleware.go`, `internal/adapters/httpapi/decode.go`, `internal/adapters/httpapi/router.go`, `internal/adapters/httpapi/health_handlers.go`
- Modify: `internal/adapters/postgres/module.go` (fornece `health.Check`)
- Test: `internal/platform/health/health_test.go` (unit), `internal/adapters/httpapi/foundation_test.go` (unit, package `httpapi`)

**Interfaces:**
- Consumes: `auth.Authenticator`, `auth.Principal`, `metrics.Metrics`, `app.*` errors e `wagering.InputError`.
- Produces:
  - Health:
    - `health.Check{Name string; Probe func(ctx context.Context) error}`;
    - `health.NewReadiness(checks []health.Check) *Readiness`, com os métodos `Check(ctx) (ok bool, results map[string]string)`
      e `StartDraining()`;
    - `postgres.Module` passa a fornecer um `health.Check` "postgres" no grupo `readiness`.
  - Router:
    - `httpapi.RouterDeps{Auth auth.Authenticator; Wallets *app.WalletService; Wagering *app.WageringService;
      Queries *app.QueryService; Reconciliation *app.ReconciliationService; Metrics *metrics.Metrics;
      Readiness *health.Readiness; Log *slog.Logger}`;
    - `httpapi.NewRouter(d RouterDeps) *gin.Engine`.
  - Unexported:
    - erros: `errorResponse(err error) (int, errorBody)` e `writeError(c *gin.Context, err error)`;
    - middlewares: `correlationID(c)`, `principal(c)`, `authenticate`, `requireAnyScope`;
    - helpers: `decodeJSON(c, v any) error` (devolve `*wagering.InputError`) e `parseUUIDParam(c, name)`.

- [ ] **Step 1: Escrever os testes que falham**

`internal/platform/health/health_test.go`:

```go
//go:build !integration && !e2e

package health

import (
	"context"
	"errors"
	"testing"
)

func TestReadiness(t *testing.T) {
	failing := errors.New("down")
	r := NewReadiness([]Check{
		{Name: "postgres", Probe: func(context.Context) error { return nil }},
		{Name: "sqs", Probe: func(context.Context) error { return failing }},
	})
	ok, results := r.Check(context.Background())
	if ok || results["postgres"] != "UP" || results["sqs"] != "DOWN" {
		t.Fatalf("ok %v results %v", ok, results)
	}
	healthy := NewReadiness([]Check{{Name: "postgres", Probe: func(context.Context) error { return nil }}})
	if ok, _ := healthy.Check(context.Background()); !ok {
		t.Fatal("healthy readiness reported not ready")
	}
	healthy.StartDraining()
	if ok, results := healthy.Check(context.Background()); ok || results["draining"] != "true" {
		t.Fatalf("draining must report not ready: %v", results)
	}
}
```

`internal/adapters/httpapi/foundation_test.go`:

```go
//go:build !integration && !e2e

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/authtest"
)

func TestErrorResponseMapping(t *testing.T) {
	input := &wagering.InputError{Violations: []wagering.Violation{{Code: wagering.FailureInvalidMoney, Field: "money.amount", Reason: "bad"}}}
	tests := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"input", input, 400, "INVALID_MONEY"},
		{"transient first", fmt.Errorf("x: %w", app.ErrVersionConflict), 503, "TEMPORARILY_UNAVAILABLE"},
		{"key conflict", app.ErrIdempotencyKeyConflict, 409, "IDEMPOTENCY_KEY_CONFLICT"},
		{"external conflict", app.ErrExternalTransactionConflict, 409, "EXTERNAL_TRANSACTION_CONFLICT"},
		{"wallet exists", app.ErrWalletAlreadyExists, 409, "WALLET_ALREADY_EXISTS"},
		{"not found", fmt.Errorf("x: %w", app.ErrNotFound), 404, "NOT_FOUND"},
		{"cursor", app.ErrInvalidCursor, 400, "INVALID_CURSOR"},
		{"limit", app.ErrInvalidLimit, 400, "INVALID_LIMIT"},
		{"unknown", errors.New("boom"), 500, "INTERNAL_ERROR"},
		{"inside tx", app.ErrInsideTransaction, 500, "INTERNAL_ERROR"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := errorResponse(tt.err)
			if status != tt.status || body.Error.FailureCode != tt.code {
				t.Fatalf("errorResponse = %d %s, want %d %s", status, body.Error.FailureCode, tt.status, tt.code)
			}
		})
	}
	_, body := errorResponse(input)
	if body.Error.Category != "CORRECTABLE" || len(body.Error.Details) != 1 || body.Error.Details[0].Field != "money.amount" {
		t.Fatalf("input body = %+v", body)
	}
	if _, body := errorResponse(errors.New("secret detail")); strings.Contains(body.Error.Message, "secret") {
		t.Fatal("500 must not leak internal error text")
	}
}

func testRouter(t *testing.T, checks ...health.Check) (*gin.Engine, *health.Readiness) {
	t.Helper()
	r := health.NewReadiness(checks)
	return NewRouter(RouterDeps{
		Auth: authtest.Default(), Metrics: metrics.New(), Readiness: r, Log: slog.New(slog.DiscardHandler),
	}), r
}

func do(router http.Handler, method, path, token string, body string, header ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestHealthAndMetricsArePublic(t *testing.T) {
	ok := health.Check{Name: "postgres", Probe: func(context.Context) error { return nil }}
	router, readiness := testRouter(t, ok)
	if rec := do(router, "GET", "/health/live", "", ""); rec.Code != 200 {
		t.Fatalf("live = %d", rec.Code)
	}
	rec := do(router, "GET", "/health/ready", "", "")
	var body map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || body["status"] != "UP" {
		t.Fatalf("ready = %d %s", rec.Code, rec.Body)
	}
	readiness.StartDraining()
	if rec := do(router, "GET", "/health/ready", "", ""); rec.Code != 503 {
		t.Fatalf("ready while draining = %d", rec.Code)
	}
	if rec := do(router, "GET", "/metrics", "", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "http_requests_total") {
		t.Fatalf("metrics = %d", rec.Code)
	}
}

func TestReadinessDown(t *testing.T) {
	down := health.Check{Name: "postgres", Probe: func(context.Context) error { return errors.New("x") }}
	router, _ := testRouter(t, down)
	if rec := do(router, "GET", "/health/ready", "", ""); rec.Code != 503 {
		t.Fatalf("ready = %d", rec.Code)
	}
}

func TestCorrelationID(t *testing.T) {
	router, _ := testRouter(t)
	rec := do(router, "GET", "/health/live", "", "", "X-Correlation-Id", "corr-123")
	if rec.Header().Get("X-Correlation-Id") != "corr-123" {
		t.Fatalf("correlation not echoed: %q", rec.Header().Get("X-Correlation-Id"))
	}
	if rec := do(router, "GET", "/health/live", "", ""); rec.Header().Get("X-Correlation-Id") == "" {
		t.Fatal("a correlation id must be generated")
	}
	long := strings.Repeat("x", 200)
	if rec := do(router, "GET", "/health/live", "", "", "X-Correlation-Id", long); rec.Header().Get("X-Correlation-Id") == long {
		t.Fatal("oversized correlation ids must be replaced")
	}
}

func TestAuthMiddleware(t *testing.T) {
	router, _ := testRouter(t)
	for name, tc := range map[string]struct {
		header string
		status int
	}{
		"missing":      {"", 401},
		"basic":        {"Basic abc", 401},
		"unknown":      {"Bearer nope", 401},
		"wrong scope":  {"Bearer provider-a", 403},
	} {
		t.Run(name, func(t *testing.T) {
			rec := do(router, "GET", "/wallets/0192f291-27dd-7d3f-8071-5f8685deef37", "", "", "Authorization", tc.header)
			if rec.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", rec.Code, tc.status, rec.Body)
			}
			if tc.status == 401 && !strings.HasPrefix(rec.Header().Get("WWW-Authenticate"), "Bearer") {
				t.Fatal("401 must carry WWW-Authenticate: Bearer")
			}
		})
	}
}
```

Este teste de autenticação usa `/wallets/:walletId`, registrada na Task 6. Até lá, no router desta task, registre
o grupo `/wallets` com `authenticate` e `requireAnyScope(auth.ScopeWallets)` e um handler provisório que responde
`501`. A Task 6 substitui o provisório.

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go get github.com/gin-gonic/gin@v1.12.0 && go test -tags=unit ./internal/platform/health/... ./internal/adapters/httpapi/...`
Expected: FAIL de compilação, `undefined: NewReadiness` e `undefined: errorResponse`.

- [ ] **Step 3: Implementar**

`internal/platform/health/health.go`:

```go
// Package health aggregates readiness probes (PostgreSQL now, SQS later)
// and reports not-ready while the process drains during shutdown.
package health

import (
	"context"
	"sync/atomic"
	"time"
)

// Check is one named readiness probe, contributed via the Fx group "readiness".
type Check struct {
	Name  string
	Probe func(ctx context.Context) error
}

// Readiness runs every check with a timeout.
type Readiness struct {
	checks   []Check
	draining atomic.Bool
}

// NewReadiness builds the aggregate.
func NewReadiness(checks []Check) *Readiness { return &Readiness{checks: checks} }

// StartDraining makes Check report not-ready (load balancers stop routing).
func (r *Readiness) StartDraining() { r.draining.Store(true) }

// Check runs the probes; ok is false if any fails or the process is draining.
func (r *Readiness) Check(ctx context.Context) (bool, map[string]string) {
	results := map[string]string{}
	ok := true
	if r.draining.Load() {
		results["draining"] = "true"
		ok = false
	}
	for _, c := range r.checks {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := c.Probe(pctx)
		cancel()
		if err != nil {
			results[c.Name] = "DOWN"
			ok = false
			continue
		}
		results[c.Name] = "UP"
	}
	return ok, results
}
```

Em `internal/adapters/postgres/module.go`, acrescentar ao `fx.Provide` do `Module`:

```go
		fx.Annotate(newHealthCheck, fx.ResultTags(`group:"readiness"`)),
```

e a função (com o import `internal/platform/health`):

```go
// newHealthCheck reports PostgreSQL readiness with a ping.
func newHealthCheck(db *gorm.DB) (health.Check, error) {
	sqlDB, err := db.DB()
	if err != nil {
		return health.Check{}, err
	}
	return health.Check{Name: "postgres", Probe: sqlDB.PingContext}, nil
}
```

`internal/adapters/httpapi/errors.go`:

```go
package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

type violationDTO struct {
	Field  string `json:"field"`
	Code   string `json:"code"`
	Reason string `json:"reason"`
}

type errorDetail struct {
	FailureCode string         `json:"failureCode"`
	Category    string         `json:"category,omitempty"`
	Message     string         `json:"message"`
	Details     []violationDTO `json:"details,omitempty"`
}

type errorBody struct {
	Error errorDetail `json:"error"`
}

func failure(code, category, message string) errorBody {
	return errorBody{Error: errorDetail{FailureCode: code, Category: category, Message: message}}
}

// errorResponse maps application errors to the HTTP contract. Transient
// errors are checked first: a retryable failure must never surface as 409.
func errorResponse(err error) (int, errorBody) {
	var input *wagering.InputError
	switch {
	case errors.As(err, &input):
		body := failure(string(input.Code()), string(wagering.CategoryCorrectable), "invalid request")
		for _, v := range input.Violations {
			body.Error.Details = append(body.Error.Details, violationDTO{Field: v.Field, Code: string(v.Code), Reason: v.Reason})
		}
		return http.StatusBadRequest, body
	case errors.Is(err, app.ErrTransient):
		return http.StatusServiceUnavailable, failure("TEMPORARILY_UNAVAILABLE", "", "temporarily unavailable, retry later")
	case errors.Is(err, app.ErrIdempotencyKeyConflict):
		return http.StatusConflict, failure("IDEMPOTENCY_KEY_CONFLICT", string(wagering.CategoryDefinitive), "idempotency key reused with a different payload")
	case errors.Is(err, app.ErrExternalTransactionConflict):
		return http.StatusConflict, failure("EXTERNAL_TRANSACTION_CONFLICT", string(wagering.CategoryDefinitive), "external transaction already registered under another idempotency key")
	case errors.Is(err, app.ErrWalletAlreadyExists):
		return http.StatusConflict, failure("WALLET_ALREADY_EXISTS", string(wagering.CategoryDefinitive), "wallet already exists for player and currency")
	case errors.Is(err, app.ErrNotFound):
		return http.StatusNotFound, failure("NOT_FOUND", "", "resource not found")
	case errors.Is(err, app.ErrInvalidCursor):
		return http.StatusBadRequest, failure("INVALID_CURSOR", string(wagering.CategoryCorrectable), "invalid ledger cursor")
	case errors.Is(err, app.ErrInvalidLimit):
		return http.StatusBadRequest, failure("INVALID_LIMIT", string(wagering.CategoryCorrectable), "limit must be between 1 and 200")
	}
	return http.StatusInternalServerError, failure("INTERNAL_ERROR", "", "internal error")
}

// writeError answers err and logs unexpected failures (without request bodies).
func writeError(c *gin.Context, err error) {
	status, body := errorResponse(err)
	if status == http.StatusServiceUnavailable {
		c.Header("Retry-After", "1")
	}
	if status == http.StatusInternalServerError {
		logger(c).ErrorContext(c.Request.Context(), "request failed",
			"correlationId", correlationID(c), "route", c.FullPath(), "error", err.Error())
	}
	c.AbortWithStatusJSON(status, body)
}
```

`internal/adapters/httpapi/middleware.go`:

```go
package httpapi

import (
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
)

const (
	correlationHeader = "X-Correlation-Id"
	ctxCorrelation    = "correlationId"
	ctxPrincipal      = "principal"
	ctxLogger         = "logger"
)

// withCorrelation accepts a sane X-Correlation-Id or generates one, and echoes it.
func withCorrelation(c *gin.Context) {
	id := c.GetHeader(correlationHeader)
	if !validCorrelation(id) {
		id = uuid.Must(uuid.NewV7()).String()
	}
	c.Set(ctxCorrelation, id)
	c.Header(correlationHeader, id)
	c.Next()
}

func validCorrelation(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if r > unicode.MaxASCII || !unicode.IsPrint(r) {
			return false
		}
	}
	return true
}

func correlationID(c *gin.Context) string { return c.GetString(ctxCorrelation) }

func withLogger(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) { c.Set(ctxLogger, log); c.Next() }
}

func logger(c *gin.Context) *slog.Logger {
	if l, ok := c.Get(ctxLogger); ok {
		return l.(*slog.Logger)
	}
	return slog.Default()
}

// accessLog writes one JSON line per request; never bodies or tokens.
func accessLog(c *gin.Context) {
	start := time.Now()
	c.Next()
	attrs := []any{
		"correlationId", correlationID(c), "method", c.Request.Method, "route", routeOf(c),
		"status", c.Writer.Status(), "latencyMs", time.Since(start).Milliseconds(),
	}
	if p, ok := principalIfAny(c); ok && p.ProviderID != "" {
		attrs = append(attrs, "providerId", p.ProviderID)
	}
	logger(c).InfoContext(c.Request.Context(), "http request", attrs...)
}

// instrument records request counts and latency per route template.
func instrument(m *metrics.Metrics) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		route := routeOf(c)
		m.HTTPRequests.WithLabelValues(c.Request.Method, route, strconv.Itoa(c.Writer.Status())).Inc()
		m.HTTPDuration.WithLabelValues(c.Request.Method, route).Observe(time.Since(start).Seconds())
	}
}

func routeOf(c *gin.Context) string {
	if r := c.FullPath(); r != "" {
		return r
	}
	return "unmatched"
}

// recoverJSON turns panics into a 500 contract body.
func recoverJSON(c *gin.Context) {
	defer func() {
		if r := recover(); r != nil {
			logger(c).ErrorContext(c.Request.Context(), "panic", "correlationId", correlationID(c), "panic", r)
			c.AbortWithStatusJSON(http.StatusInternalServerError, failure("INTERNAL_ERROR", "", "internal error"))
		}
	}()
	c.Next()
}

// authenticate requires a valid Bearer token.
func authenticate(a auth.Authenticator) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw, ok := strings.CutPrefix(c.GetHeader("Authorization"), "Bearer ")
		if !ok || raw == "" {
			unauthorized(c)
			return
		}
		p, err := a.Authenticate(c.Request.Context(), raw)
		if err != nil {
			unauthorized(c)
			return
		}
		c.Set(ctxPrincipal, p)
		c.Next()
	}
}

func unauthorized(c *gin.Context) {
	c.Header("WWW-Authenticate", `Bearer realm="wallet"`)
	c.AbortWithStatusJSON(http.StatusUnauthorized, failure("UNAUTHENTICATED", "", "missing, invalid or expired access token"))
}

func forbidden(c *gin.Context, message string) {
	c.AbortWithStatusJSON(http.StatusForbidden, failure("FORBIDDEN", "", message))
}

// requireAnyScope lets the request through if the principal has one of scopes.
func requireAnyScope(scopes ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		p := principal(c)
		for _, s := range scopes {
			if p.Has(s) {
				c.Next()
				return
			}
		}
		forbidden(c, "insufficient scope")
	}
}

func principalIfAny(c *gin.Context) (auth.Principal, bool) {
	v, ok := c.Get(ctxPrincipal)
	if !ok {
		return auth.Principal{}, false
	}
	p, ok := v.(auth.Principal)
	return p, ok
}

func principal(c *gin.Context) auth.Principal {
	p, _ := principalIfAny(c)
	return p
}
```

`internal/adapters/httpapi/decode.go`:

```go
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

const maxBodyBytes = 1 << 20

func inputError(code wagering.FailureCode, field, reason string) *wagering.InputError {
	return &wagering.InputError{Violations: []wagering.Violation{{Code: code, Field: field, Reason: reason}}}
}

// decodeJSON strictly decodes one JSON object into v: unknown fields, trailing
// data, oversized bodies and wrong JSON types are correctable input errors.
// A numeric money amount is INVALID_MONEY: amounts are decimal strings.
func decodeJSON(c *gin.Context, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			if strings.HasSuffix(typeErr.Field, "amount") {
				return inputError(wagering.FailureInvalidMoney, typeErr.Field, "amount must be a decimal string such as \"25.00\"")
			}
			return inputError(wagering.FailureInvalidField, typeErr.Field, "must be a "+typeErr.Type.String())
		}
		return inputError(wagering.FailureMalformedPayload, "body", fmt.Sprintf("invalid JSON: %v", err))
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return inputError(wagering.FailureMalformedPayload, "body", "unexpected data after the JSON object")
	}
	return nil
}

// parseUUIDParam reads a lowercase canonical UUID path parameter.
func parseUUIDParam(c *gin.Context, name string) (uuid.UUID, error) {
	raw := c.Param(name)
	id, err := uuid.Parse(raw)
	if err != nil || id.String() != raw {
		return uuid.Nil, inputError(wagering.FailureInvalidID, name, "must be a lowercase canonical UUID")
	}
	return id, nil
}
```

`internal/adapters/httpapi/health_handlers.go`:

```go
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
)

func liveHandler(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "UP"}) }

func readyHandler(r *health.Readiness) gin.HandlerFunc {
	return func(c *gin.Context) {
		ok, results := r.Check(c.Request.Context())
		status, word := http.StatusOK, "UP"
		if !ok {
			status, word = http.StatusServiceUnavailable, "DOWN"
		}
		c.JSON(status, gin.H{"status": word, "checks": results})
	}
}
```

`internal/adapters/httpapi/router.go`:

```go
// Package httpapi is the HTTP adapter: Gin router, authentication and
// authorization middleware, handlers over the application services and the
// error contract.
package httpapi

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
)

// RouterDeps are the collaborators of the HTTP API.
type RouterDeps struct {
	Auth           auth.Authenticator
	Wallets        *app.WalletService
	Wagering       *app.WageringService
	Queries        *app.QueryService
	Reconciliation *app.ReconciliationService
	Metrics        *metrics.Metrics
	Readiness      *health.Readiness
	Log            *slog.Logger
}

func init() { gin.SetMode(gin.ReleaseMode) }

// NewRouter builds the API.
func NewRouter(d RouterDeps) *gin.Engine {
	r := gin.New()
	r.Use(withCorrelation, withLogger(d.Log), recoverJSON, accessLog, instrument(d.Metrics))

	r.GET("/health/live", liveHandler)
	r.GET("/health/ready", readyHandler(d.Readiness))
	r.GET("/metrics", gin.WrapH(promhttp.HandlerFor(d.Metrics.Registry, promhttp.HandlerOpts{})))

	authn := authenticate(d.Auth)
	wallets := r.Group("/wallets", authn, requireAnyScope(auth.ScopeWallets))
	wallets.GET("/:walletId", func(c *gin.Context) { c.Status(http.StatusNotImplemented) }) // replaced in Task 6

	return r
}
```

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=unit ./internal/platform/health/... ./internal/adapters/httpapi/... && go test -race ./internal/adapters/postgres/...`
Expected: `ok`. O teste de módulo do Postgres continua válido com o novo `health.Check`.

- [ ] **Step 5: Commit**

```bash
go mod tidy && gofmt -l . && go vet ./...
git add go.mod go.sum internal/platform/health internal/adapters/httpapi internal/adapters/postgres
git commit -m "feat(http): router foundation with error contract, auth middleware, health and metrics" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Handlers de carteira

**Files:**
- Create: `internal/adapters/httpapi/dto.go`, `internal/adapters/httpapi/wallet_handlers.go`
- Modify: `internal/adapters/httpapi/router.go` (rotas reais de `/wallets`)
- Test: `internal/adapters/httpapi/wallet_handlers_test.go` (integration, package `httpapi_test`), `internal/adapters/httpapi/handlers_helpers_test.go` (integration, package `httpapi_test`)

**Interfaces:**
- Consumes: `app.ParseOpenWallet`, `WalletService.Open`, `QueryService.Wallet/Ledger`, `ReconciliationService.Reconcile`; `apptest.New`; `authtest.Default`.
- Produces:
  - Rotas, todas exigindo o scope `wallets`:
    - `POST /wallets` → 201;
    - `GET /wallets/:walletId`;
    - `GET /wallets/:walletId/ledger?cursor&limit`;
    - `POST /wallets/:walletId/reconciliation`.
  - DTOs: `moneyRequest`, `walletResponse`, `ledgerEntryResponse`, `ledgerPageResponse` e `reconciliationResponse`.
  - Helper de teste `newAPI(t) (*apiHarness)`, com `Router http.Handler`, `H *apptest.Harness` e o método
    `(*apiHarness).Do(method, path, token, body string, headers ...string) *httptest.ResponseRecorder`.

- [ ] **Step 1: Escrever os testes que falham**

`internal/adapters/httpapi/handlers_helpers_test.go`:

```go
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
	wagering, err := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	m := metrics.New()
	router := httpapi.NewRouter(httpapi.RouterDeps{
		Auth: authtest.Default(), Wallets: app.NewWalletService(h.Deps), Wagering: wagering,
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
```

`internal/adapters/httpapi/wallet_handlers_test.go`:

```go
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
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test -tags=integration ./internal/adapters/httpapi/...`
Expected: FAIL. `POST /wallets` dá 404 (rota inexistente) e `GET` dá 501.

- [ ] **Step 3: Implementar**

`internal/adapters/httpapi/dto.go`:

```go
package httpapi

import (
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

type moneyRequest struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type walletResponse struct {
	ID       string      `json:"id"`
	PlayerID string      `json:"playerId"`
	Balance  money.Money `json:"balance"`
	Version  int64       `json:"version"`
}

func newWalletResponse(w *wallet.Wallet) walletResponse {
	return walletResponse{ID: w.ID().String(), PlayerID: w.PlayerID().String(), Balance: w.Balance(), Version: w.Version()}
}

type ledgerEntryResponse struct {
	ID            string      `json:"id"`
	TransactionID string      `json:"transactionId"`
	Direction     string      `json:"direction"`
	Money         money.Money `json:"money"`
	BalanceBefore money.Money `json:"balanceBefore"`
	BalanceAfter  money.Money `json:"balanceAfter"`
	CreatedAt     string      `json:"createdAt"`
}

type ledgerPageResponse struct {
	Items      []ledgerEntryResponse `json:"items"`
	NextCursor string                `json:"nextCursor,omitempty"`
}

func newLedgerEntryResponse(e wallet.LedgerEntry) ledgerEntryResponse {
	return ledgerEntryResponse{
		ID: e.ID().String(), TransactionID: e.TransactionID().String(), Direction: string(e.Direction()),
		Money: e.Amount(), BalanceBefore: e.BalanceBefore(), BalanceAfter: e.BalanceAfter(),
		CreatedAt: formatTime(e.CreatedAt()),
	}
}

type reconciliationResponse struct {
	WalletID          string      `json:"walletId"`
	StoredBalance     money.Money `json:"storedBalance"`
	CalculatedBalance money.Money `json:"calculatedBalance"`
	Difference        money.Money `json:"difference"`
	Consistent        bool        `json:"consistent"`
	CheckedEntries    int64       `json:"checkedEntries"`
}

// formatTime renders RFC 3339 UTC with milliseconds, like the events.
func formatTime(t time.Time) string { return events.FormatTime(t) }
```

`internal/adapters/httpapi/wallet_handlers.go`:

```go
package httpapi

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
)

type walletHandlers struct {
	wallets        *app.WalletService
	queries        *app.QueryService
	reconciliation *app.ReconciliationService
}

func meta(c *gin.Context) app.Meta {
	return app.Meta{CorrelationID: correlationID(c), Channel: app.ChannelHTTP}
}

func (h walletHandlers) open(c *gin.Context) {
	var req struct {
		PlayerID       string       `json:"playerId"`
		InitialBalance moneyRequest `json:"initialBalance"`
	}
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, err)
		return
	}
	cmd, err := app.ParseOpenWallet(req.PlayerID, req.InitialBalance.Amount, req.InitialBalance.Currency)
	if err != nil {
		writeError(c, err)
		return
	}
	w, err := h.wallets.Open(c.Request.Context(), cmd, meta(c))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, newWalletResponse(w))
}

func (h walletHandlers) get(c *gin.Context) {
	id, err := parseUUIDParam(c, "walletId")
	if err != nil {
		writeError(c, err)
		return
	}
	w, err := h.queries.Wallet(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, newWalletResponse(w))
}

func (h walletHandlers) ledger(c *gin.Context) {
	id, err := parseUUIDParam(c, "walletId")
	if err != nil {
		writeError(c, err)
		return
	}
	limit := 0
	if raw := c.Query("limit"); raw != "" {
		if limit, err = strconv.Atoi(raw); err != nil {
			writeError(c, app.ErrInvalidLimit)
			return
		}
	}
	page, err := h.queries.Ledger(c.Request.Context(), id, c.Query("cursor"), limit)
	if err != nil {
		writeError(c, err)
		return
	}
	resp := ledgerPageResponse{Items: make([]ledgerEntryResponse, 0, len(page.Entries)), NextCursor: page.NextCursor}
	for _, e := range page.Entries {
		resp.Items = append(resp.Items, newLedgerEntryResponse(e))
	}
	c.JSON(http.StatusOK, resp)
}

func (h walletHandlers) reconcile(c *gin.Context) {
	id, err := parseUUIDParam(c, "walletId")
	if err != nil {
		writeError(c, err)
		return
	}
	r, err := h.reconciliation.Reconcile(c.Request.Context(), id)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, reconciliationResponse{
		WalletID: r.WalletID.String(), StoredBalance: r.Stored, CalculatedBalance: r.Calculated,
		Difference: r.Difference, Consistent: r.Consistent, CheckedEntries: r.CheckedEntries,
	})
}
```

Em `router.go`, substituir a rota provisória por:

```go
	wh := walletHandlers{wallets: d.Wallets, queries: d.Queries, reconciliation: d.Reconciliation}
	wallets := r.Group("/wallets", authn, requireAnyScope(auth.ScopeWallets))
	wallets.POST("", wh.open)
	wallets.GET("/:walletId", wh.get)
	wallets.GET("/:walletId/ledger", wh.ledger)
	wallets.POST("/:walletId/reconciliation", wh.reconcile)
```

(remova o import `net/http` do router, se ficar sem uso).

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=unit ./internal/adapters/httpapi/... && go test -race -count=1 -tags=integration ./internal/adapters/httpapi/...`
Expected: `ok`. O `TestAuthMiddleware` da Task 5 agora recebe 403 do scope e 404 da carteira inexistente para o
token interno. Ele só usa tokens sem o scope `wallets`, então continua válido.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/adapters/httpapi
git commit -m "feat(http): wallet endpoints (open, read, ledger page, reconciliation)" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Handlers de operações de aposta

**Files:**
- Create: `internal/adapters/httpapi/transaction_handlers.go`
- Modify: `internal/adapters/httpapi/dto.go`, `internal/adapters/httpapi/router.go`
- Test: `internal/adapters/httpapi/transaction_handlers_test.go` (integration)

**Interfaces:**
- Consumes: `WageringService.Process`, `QueryService.Transaction/TransactionByExternalID`, `wagering.ParseCommand`, `app.Viewer`; `newAPI`, `decode`, `errorCode`, `openWallet` (Task 6).
- Produces:
  - Rotas:
    - `POST /wagering/transactions` (scope `wagering`);
    - `GET /wagering/transactions/:transactionId` (scope `wagering` ou `wagering:read`);
    - `GET /providers/:providerId/wagering/transactions/:externalTransactionId` (scope `wagering`).
  - DTOs `processResponse` e `transactionView`, mais `statusCode(wagering.Status) int`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/adapters/httpapi/transaction_handlers_test.go`:

```go
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
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/adapters/httpapi/ -run 'Process|Status|TransactionInput|Isolation'`
Expected: FAIL com 404, porque as rotas ainda não existem.

- [ ] **Step 3: Implementar**

Acrescentar a `dto.go` (e os imports `net/http`, `github.com/google/uuid` e `internal/domain/wagering`):

```go
type transactionRequest struct {
	ProviderID                     string       `json:"providerId"`
	ExternalTransactionID          string       `json:"externalTransactionId"`
	PlayerID                       string       `json:"playerId"`
	WalletID                       string       `json:"walletId"`
	RoundID                        string       `json:"roundId"`
	GameID                         string       `json:"gameId"`
	Kind                           string       `json:"kind"`
	Money                          moneyRequest `json:"money"`
	ReferenceExternalTransactionID string       `json:"referenceExternalTransactionId"`
}

type processResponse struct {
	TransactionID    string       `json:"transactionId"`
	Status           string       `json:"status"`
	Balance          *money.Money `json:"balance,omitempty"`
	FailureCode      string       `json:"failureCode,omitempty"`
	Category         string       `json:"category,omitempty"`
	IdempotentReplay bool         `json:"idempotentReplay"`
}

func newProcessResponse(t *wagering.WagerTransaction, replay bool) processResponse {
	r := processResponse{TransactionID: t.ID().String(), Status: string(t.Status()), IdempotentReplay: replay}
	if t.Status() == wagering.StatusProcessed {
		b := t.ResultBalance()
		r.Balance = &b
	}
	if code := t.FailureCode(); code != "" {
		r.FailureCode, r.Category = string(code), string(code.Category())
	}
	return r
}

// statusCode maps an outcome to the HTTP status of the contract.
func statusCode(s wagering.Status) int {
	switch s {
	case wagering.StatusProcessed:
		return http.StatusOK
	case wagering.StatusRejected:
		return http.StatusUnprocessableEntity
	case wagering.StatusFailed:
		return http.StatusInternalServerError
	default: // PENDING_REFERENCE (and PENDING, never persisted)
		return http.StatusAccepted
	}
}

type transactionView struct {
	TransactionID                  string       `json:"transactionId"`
	Origin                         string       `json:"origin"`
	Kind                           string       `json:"kind"`
	Status                         string       `json:"status"`
	WalletID                       string       `json:"walletId"`
	PlayerID                       string       `json:"playerId"`
	Money                          money.Money  `json:"money"`
	ProviderID                     string       `json:"providerId,omitempty"`
	ExternalTransactionID          string       `json:"externalTransactionId,omitempty"`
	RoundID                        string       `json:"roundId,omitempty"`
	GameID                         string       `json:"gameId,omitempty"`
	ReferenceExternalTransactionID string       `json:"referenceExternalTransactionId,omitempty"`
	ReferenceTransactionID         string       `json:"referenceTransactionId,omitempty"`
	FailureCode                    string       `json:"failureCode,omitempty"`
	Category                       string       `json:"category,omitempty"`
	Balance                        *money.Money `json:"balance,omitempty"`
	Attempts                       int          `json:"attempts"`
	NextAttemptAt                  string       `json:"nextAttemptAt,omitempty"`
	CreatedAt                      string       `json:"createdAt"`
	UpdatedAt                      string       `json:"updatedAt"`
	ProcessedAt                    string       `json:"processedAt,omitempty"`
}

func newTransactionView(t *wagering.WagerTransaction) transactionView {
	s := t.State()
	v := transactionView{
		TransactionID: s.ID.String(), Origin: string(s.Origin), Kind: string(s.Kind), Status: string(s.Status),
		WalletID: s.WalletID.String(), PlayerID: s.PlayerID.String(), Money: s.Money,
		ProviderID: s.ProviderID, ExternalTransactionID: s.ExternalTransactionID, RoundID: s.RoundID, GameID: s.GameID,
		ReferenceExternalTransactionID: s.ReferenceExternalTransactionID, Attempts: s.Attempts,
		CreatedAt: formatTime(s.CreatedAt), UpdatedAt: formatTime(s.UpdatedAt),
	}
	if s.ReferenceTransactionID != uuid.Nil {
		v.ReferenceTransactionID = s.ReferenceTransactionID.String()
	}
	if s.FailureCode != "" {
		v.FailureCode, v.Category = string(s.FailureCode), string(s.FailureCode.Category())
	}
	if s.Status == wagering.StatusProcessed {
		b := s.ResultBalance
		v.Balance = &b
	}
	if !s.NextAttemptAt.IsZero() {
		v.NextAttemptAt = formatTime(s.NextAttemptAt)
	}
	if !s.ProcessedAt.IsZero() {
		v.ProcessedAt = formatTime(s.ProcessedAt)
	}
	return v
}
```


`internal/adapters/httpapi/transaction_handlers.go`:

```go
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

type transactionHandlers struct {
	wagering *app.WageringService
	queries  *app.QueryService
}

// process applies an operation for the authenticated provider. The provider
// in the body must be the token's provider: otherwise 403 and no effect.
func (h transactionHandlers) process(c *gin.Context) {
	p := principal(c)
	var req transactionRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, err)
		return
	}
	if p.ProviderID == "" || (req.ProviderID != "" && req.ProviderID != p.ProviderID) {
		forbidden(c, "providerId does not match the authenticated provider")
		return
	}
	cmd, err := wagering.ParseCommand(wagering.RawCommand{
		ProviderID: req.ProviderID, ExternalTransactionID: req.ExternalTransactionID,
		IdempotencyKey: c.GetHeader("Idempotency-Key"), PlayerID: req.PlayerID, WalletID: req.WalletID,
		RoundID: req.RoundID, GameID: req.GameID, Kind: req.Kind,
		Amount: req.Money.Amount, Currency: req.Money.Currency,
		ReferenceExternalTransactionID: req.ReferenceExternalTransactionID,
	})
	if err != nil {
		writeError(c, err)
		return
	}
	res, err := h.wagering.Process(c.Request.Context(), cmd, meta(c), nil)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(statusCode(res.Transaction.Status()), newProcessResponse(res.Transaction, res.Replay))
}

// get reads by internal id: providers see only their own operations (others
// are 404); the internal client (wagering:read) sees everything.
func (h transactionHandlers) get(c *gin.Context) {
	id, err := parseUUIDParam(c, "transactionId")
	if err != nil {
		writeError(c, err)
		return
	}
	p := principal(c)
	viewer := app.Viewer{ProviderID: p.ProviderID, Internal: p.Has(auth.ScopeWageringRead)}
	t, err := h.queries.Transaction(c.Request.Context(), id, viewer)
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, newTransactionView(t))
}

// getByExternalID serves the provider route; the path provider must be the caller.
func (h transactionHandlers) getByExternalID(c *gin.Context) {
	p := principal(c)
	if c.Param("providerId") != p.ProviderID || p.ProviderID == "" {
		forbidden(c, "providerId does not match the authenticated provider")
		return
	}
	t, err := h.queries.TransactionByExternalID(c.Request.Context(), p.ProviderID, c.Param("externalTransactionId"))
	if err != nil {
		writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, newTransactionView(t))
}
```

Em `router.go`, acrescentar antes do `return r`:

```go
	th := transactionHandlers{wagering: d.Wagering, queries: d.Queries}
	r.POST("/wagering/transactions", authn, requireAnyScope(auth.ScopeWagering), th.process)
	r.GET("/wagering/transactions/:transactionId", authn, requireAnyScope(auth.ScopeWagering, auth.ScopeWageringRead), th.get)
	r.GET("/providers/:providerId/wagering/transactions/:externalTransactionId", authn, requireAnyScope(auth.ScopeWagering), th.getByExternalID)
```

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=unit ./... && go test -race -count=1 -tags=integration ./internal/adapters/httpapi/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/adapters/httpapi
git commit -m "feat(http): wager operation endpoints with provider-scoped authorization" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Servidor com ciclo de vida, composição Fx e `cmd/wallet`

**Files:**
- Create: `internal/adapters/httpapi/server.go`, `internal/adapters/httpapi/module.go`, `internal/platform/composition/composition.go`, `cmd/wallet/main.go`
- Test: `internal/platform/composition/composition_test.go` (integration)

**Interfaces:**
- Consumes: todos os módulos (`config`, `logging`, `metrics`, `postgres`, `app`, `auth`, `httpapi`).
- Produces:
  - `httpapi.NewServer(lc fx.Lifecycle, cfg config.Config, router *gin.Engine, readiness *health.Readiness,
    log *slog.Logger) *Server` e `(*Server).Addr() string`. O endereço real só existe depois do start, então
    `HTTP_ADDR=127.0.0.1:0` serve nos testes.
  - `httpapi.Module`, que fornece `*health.Readiness` (a partir do grupo `readiness`), `*gin.Engine` e `*Server`, e
    invoca o servidor.
  - `composition.Modules() fx.Option` e `cmd/wallet/main.go`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/platform/composition/composition_test.go`:

```go
//go:build !unit && !e2e

package composition_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/httpapi"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/composition"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func setEnv(t *testing.T) {
	t.Setenv("DATABASE_URL", pgtest.AppURL())
	t.Setenv("OIDC_ISSUER_URL", kctest.IssuerURL())
	t.Setenv("HTTP_ADDR", "127.0.0.1:0")
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "5s")
	t.Setenv("LOG_LEVEL", "error")
}

func TestCompositionGraphIsValid(t *testing.T) {
	setEnv(t)
	if err := fx.ValidateApp(fx.NopLogger, composition.Modules()); err != nil {
		t.Fatal(err)
	}
}

func TestServiceStartsServesAndDrains(t *testing.T) {
	pgtest.AppDB(t)
	setEnv(t)
	var (
		server    *httpapi.Server
		readiness *health.Readiness
	)
	app := fxtest.New(t, fx.NopLogger, composition.Modules(), fx.Populate(&server, &readiness))
	app.RequireStart()

	base := "http://" + server.Addr()
	for _, path := range []string{"/health/live", "/health/ready"} {
		resp, err := http.Get(base + path)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("GET %s: %v %v", path, resp, err)
		}
		resp.Body.Close()
	}
	resp, err := http.Post(base+"/wallets", "application/json", nil)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("business endpoints require a token: %v %v", resp, err)
	}
	resp.Body.Close()

	app.RequireStop()
	if ok, results := readiness.Check(context.Background()); ok || results["draining"] != "true" {
		t.Fatalf("readiness after stop = %v %v (must be draining)", ok, results)
	}
	client := http.Client{Timeout: time.Second}
	if _, err := client.Get(base + "/health/live"); err == nil {
		t.Fatal("server still accepting connections after stop")
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/platform/composition/...`
Expected: FAIL de compilação, `undefined: composition.Modules`.

- [ ] **Step 3: Implementar**

`internal/adapters/httpapi/server.go`:

```go
package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
)

// Server runs the HTTP API inside the Fx lifecycle.
type Server struct {
	srv *http.Server
	ln  net.Listener
}

// Addr is the bound address (valid after start; supports ":0").
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// NewServer binds on start (failing fast if the port is taken) and, on stop,
// marks readiness as draining, stops accepting connections and waits for
// in-flight requests up to HTTP_SHUTDOWN_TIMEOUT.
func NewServer(lc fx.Lifecycle, cfg config.Config, router *gin.Engine, readiness *health.Readiness, log *slog.Logger) *Server {
	s := &Server{srv: &http.Server{Handler: router, ReadHeaderTimeout: 5 * time.Second}}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ln, err := net.Listen("tcp", cfg.HTTP.Addr)
			if err != nil {
				return err
			}
			s.ln = ln
			go func() {
				if err := s.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Error("http server stopped", "error", err.Error())
				}
			}()
			log.Info("http server listening", "addr", s.Addr())
			return nil
		},
		OnStop: func(ctx context.Context) error {
			readiness.StartDraining()
			ctx, cancel := context.WithTimeout(ctx, cfg.HTTP.ShutdownTimeout)
			defer cancel()
			return s.srv.Shutdown(ctx)
		},
	})
	return s
}
```

`internal/adapters/httpapi/module.go`:

```go
package httpapi

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
)

type routerIn struct {
	fx.In
	Auth           auth.Authenticator
	Wallets        *app.WalletService
	Wagering       *app.WageringService
	Queries        *app.QueryService
	Reconciliation *app.ReconciliationService
	Metrics        *metrics.Metrics
	Readiness      *health.Readiness
	Log            *slog.Logger
}

type readinessIn struct {
	fx.In
	Checks []health.Check `group:"readiness"`
}

// Module provides the router and the server and starts it.
var Module = fx.Module("httpapi",
	fx.Provide(
		func(in readinessIn) *health.Readiness { return health.NewReadiness(in.Checks) },
		func(in routerIn) *gin.Engine {
			return NewRouter(RouterDeps{
				Auth: in.Auth, Wallets: in.Wallets, Wagering: in.Wagering, Queries: in.Queries,
				Reconciliation: in.Reconciliation, Metrics: in.Metrics, Readiness: in.Readiness, Log: in.Log,
			})
		},
		NewServer,
	),
	fx.Invoke(func(*Server) {}),
)
```

(acrescente o import `github.com/gin-gonic/gin`).

`internal/platform/composition/composition.go`:

```go
// Package composition is the composition root: every Fx module of the
// service, shared by cmd/wallet and the composition tests.
package composition

import (
	"log/slog"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/httpapi"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/postgres"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/logging"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
)

// Modules returns the full application graph. Fx starts modules in
// dependency order and stops them in reverse: the HTTP server drains before
// the database pool closes.
func Modules() fx.Option {
	return fx.Options(
		config.Module, logging.Module, metrics.Module, postgres.Module, app.Module, auth.Module, httpapi.Module,
	)
}

// Logger routes Fx's own events to the service logger.
func Logger() fx.Option {
	return fx.WithLogger(func(l *slog.Logger) fxevent.Logger { return &fxevent.SlogLogger{Logger: l} })
}
```

`cmd/wallet/main.go`:

```go
// Command wallet runs the wallet service: HTTP API (and, in later plans, the
// SQS consumer and background workers).
package main

import (
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/composition"
)

func main() {
	fx.New(composition.Modules(), composition.Logger()).Run()
}
```

- [ ] **Step 4: Rodar os testes e o binário**

Run:

```bash
go test -race -count=1 -tags=integration ./internal/platform/composition/...
go build -o /tmp/wallet ./cmd/wallet && echo build-ok
```

Expected: `ok` e `build-ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/adapters/httpapi internal/platform/composition cmd/wallet
git commit -m "feat(http): server lifecycle with draining, composition root and wallet binary" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Autorização ponta a ponta com tokens reais do Keycloak

**Files:**
- Test: `internal/adapters/httpapi/auth_e2e_test.go` (integration, package `httpapi_test`)

**Interfaces:**
- Consumes: `auth.NewOIDCAuthenticator`, `kctest.Token`/`IssuerURL`/`JWKSURL`, `newAPI`-like wiring (com o autenticador real), `txBody`, `decode`.
- Produces: só testes. Nenhuma mudança de produção é esperada. Se algo falhar, é bug real: reporte com evidência.

- [ ] **Step 1: Escrever os testes**

`internal/adapters/httpapi/auth_e2e_test.go`:

```go
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
	if rec := api.Do("POST", "/wallets", providerA, `{"playerId":"`+uuid.NewString()+`","initialBalance":{"amount":"1.00","currency":"BRL"}}`); rec.Code != 403 {
		t.Fatalf("providers cannot open wallets = %d", rec.Code)
	}
	if rec := api.Do("GET", "/wallets/"+w.id, internal, ""); decode(t, rec)["balance"].(map[string]any)["amount"] != "100.00" {
		t.Fatal("denied requests must not move money")
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
	if r := api.Do("GET", "/wagering/transactions/"+id, internal, ""); r.Code != 200 {
		t.Fatalf("internal reads any transaction = %d", r.Code)
	}
	if r := api.Do("GET", "/wallets/"+w.id, internal, ""); decode(t, r)["balance"].(map[string]any)["amount"] != "90.00" {
		t.Fatal("only provider-a's bet may have moved the wallet")
	}
}
```

- [ ] **Step 2: Rodar**

Run: `docker compose --profile test up -d --wait postgres_test keycloak_test && go test -race -count=1 -tags=integration ./internal/adapters/httpapi/ -run RealTokens`
Expected: `ok`, com cerca de 3s por causa do token expirado.

- [ ] **Step 3: Verificação final do plano**

Run:

```bash
gofmt -l . && go vet ./... && go test -race -tags=unit ./... && go test -race -count=1 ./...
go list -f '{{join .Imports "\n"}}' ./internal/app | sort -u
go list -f '{{join .Imports "\n"}}' ./internal/domain/... | sort -u
```

Expected:
- Tudo `ok`.
- `internal/app` sem gin, go-oidc, prometheus, gorm, aws ou adapters.
- O domínio só importa stdlib, uuid e `internal/domain`.

- [ ] **Step 4: Commit**

```bash
git add internal/adapters/httpapi
git commit -m "test(http): end-to-end authorization with real Keycloak tokens" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
