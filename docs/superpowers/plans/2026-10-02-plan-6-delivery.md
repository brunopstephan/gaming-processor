# Plano 6 — Entrega: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Deixar o serviço reproduzível de um checkout limpo. `docker compose up --build` sobe tudo:
- Postgres, Keycloak e MiniStack provisionados;
- o wallet com 3 réplicas atrás do nginx;
- Prometheus e Grafana com dashboard provisionado.

O plano também prova os cenários de concorrência e recuperação do enunciado (§13) com testes E2E que sobem 3 processos reais do binário, com falhas injetadas. Por fim, entrega README, ARCHITECTURE e `.env.example`.

**Architecture:**
- **Falhas injetadas:** um pacote `internal/platform/faultinject` é no-op no build normal. Com a tag `faultinject`, ele encerra o processo num ponto nomeado (variável `FAULT`). Os pontos ficam no `settle` do consumidor e no `publish` do relay.
- **Binário e compose:** o binário vai num Dockerfile multi-stage, e o compose ganha `wallet` (3 réplicas), `nginx` (balanceador), `prometheus` (descoberta por DNS) e `grafana`.
- **E2E:** os testes ficam em `e2e/` (tag de exclusão `!unit && !integration`). O `TestMain` compila o binário com `-tags faultinject`, e cada teste sobe seu próprio cluster de processos sobre banco migrado e filas próprios. Eles exercitam HTTP, SQS, outbox, worker, crash e restart.

**Tech Stack:** Go 1.25.11 (go.mod), imagens `golang:1.25.14-alpine`, `alpine:3.24.2`, `nginx:1.30.5-alpine`, `prom/prometheus:v3.15.0` e `grafana/grafana:13.2.3`. Mais o compose existente (`postgres:17-alpine`, `quay.io/keycloak/keycloak:26.6`, `ministackorg/ministack:1.5.20`, `amazon/aws-cli:2.37.8`).

**Spec:** `docs/superpowers/specs/2026-10-01-wallet-service-design.md`, seções 13 (shutdown), 14 (observabilidade), 15 (compose), 16 (testes, E2E) e 17 (documentação). Enunciado: `docs/CHALLENGE.md`, seções 3, 4, 12, 13 e 15. As pendências dos planos anteriores estão na memória do projeto e já foram incorporadas às tarefas.

## Roadmap

1. ~~Domínio~~ 2. ~~Persistência~~ 3. ~~Casos de uso~~ 4. ~~HTTP e auth~~ 5. ~~Mensageria~~ 6. **Entrega** (este, o último)

## Global Constraints

- Módulo `github.com/brunopstephan/backend-challenge-go`, diretiva `go 1.25.11`. `internal/domain/**` não muda.
  `internal/app` só pode importar, de fora da stdlib e do domínio, o `internal/platform/faultinject` (sem
  dependências).
- `faultinject` é interna: o build normal nunca encerra o processo, mesmo com `FAULT` definida. Só o binário do E2E
  usa a tag.
- **Compose:**
  - `docker compose up --build` sobe tudo, e o nginx atende em `${WALLET_PORT:-8000}`.
  - O wallet roda com `deploy.replicas: 3` e sem porta no host.
  - `stop_grace_period: 90s` cobre o `composition.StopTimeout` com todos os toggles (77s).
  - Cada réplica usa os profiles IAM `wallet-consumer` e `wallet-publisher` do arquivo de credenciais provisionado;
    sem profile, entraria como root.
  - `OIDC_ISSUER_URL` é o issuer público (`http://localhost:8080/realms/wallet`), e `OIDC_JWKS_URL` é o endereço
    interno (`http://keycloak:8080/...`).
- **Portas no host:** postgres 5432, keycloak 8080, ministack 4566, nginx 8000, prometheus 9090 e grafana 3000. No
  profile test: 5433, 8081 e 4567.
- **Métricas:** Prometheus coleta `/metrics` de cada réplica por `dns_sd_configs` do serviço `wallet`. As queries do
  dashboard usam só nomes de métricas que o código registra.
- **E2E:**
  - roda com `go test ./...` e `go test -tags=e2e ./...`;
  - precisa do profile `test`;
  - cada teste usa `pgtest.FreshDatabase` e `sqstest.NewQueues` próprios (relay e worker fazem claim global);
  - processos com ambiente explícito, sem herdar `AWS_*` do usuário;
  - todo processo iniciado é encerrado no cleanup;
  - em falha, o teste imprime o fim do log de cada processo.
- **Tags de build:**
  - unit: `//go:build !integration && !e2e`;
  - integration: `//go:build !unit && !e2e`;
  - e2e: `//go:build !unit && !integration`.
- **Documentação** em português, como o resto do projeto. O README atual termina com a seção
  `# Nota do desenvolvedor`, escrita à mão pelo dono do projeto. Ela é preservada byte a byte, no fim do novo README.
- Testes só com `testing`, nunca `t.Fatal` fora da goroutine do teste. `gofmt` e `go vet ./...` limpos.
- Toda mensagem de commit termina exatamente com `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Crash depois do commit e antes do delete** (enunciado §13.5): a reentrega em outra instância não duplica, e a
   prova é `inbox_duplicates_total` ≥ 1 e um único débito. Teste na Task 5.
2. **Crash entre publicar e marcar** (§13.6): outro publisher republica com o mesmo `eventId`. A linha da outbox tem
   `attempts ≥ 2` e a fila recebe cada `eventId` uma única vez. Teste na Task 5.
3. **Duas apostas de 80.00 sobre 100.00 em instâncias diferentes, com a carteira travada por fora:** as requisições
   esperam o lock entre processos. Uma é aceita e a outra recebe `INSUFFICIENT_FUNDS`, saldo 20.00, e os reenvios
   devolvem as respostas originais. Teste na Task 4.
4. **Restart com `kill -9` e `SIGTERM`:** replays devolvem o resultado original, pendências são retomadas por outra
   instância, a reconciliação fecha e o `SIGTERM` sai com status 0. Teste na Task 5.
5. **Um painel do dashboard apontando para métrica inexistente:** fica vazio sem erro, então um teste confere toda
   query do dashboard contra o registry. Task 3.

## File Structure

```
internal/platform/faultinject/{faultinject.go,hit_off.go,hit_on.go,faultinject_test.go}
internal/adapters/sqsconsumer/consumer.go      (modify) Hit no settle (delete)
internal/app/outbox_relay.go                   (modify) Hit entre Publish e MarkPublished
Dockerfile, .dockerignore
docker-compose.yml                             (modify) + wallet, nginx, prometheus, grafana
deploy/nginx/nginx.conf
deploy/prometheus/prometheus.yml
deploy/grafana/provisioning/datasources/prometheus.yml
deploy/grafana/provisioning/dashboards/dashboards.yml
deploy/grafana/dashboards/wallet.json
internal/platform/metrics/dashboard_test.go    consistência dashboard × registry
scripts/token.sh, scripts/sqs-send.sh, scripts/smoke.sh
e2e/harness_test.go                            TestMain, cluster de processos, helpers HTTP/SQS/DB
e2e/concurrency_test.go                        cenários 1–4 + cruzamento HTTP↔SQS + duplicatas SQS
e2e/recovery_test.go                           cenários 5–8
README.md, ARCHITECTURE.md, .env.example
```

---

### Task 1: Falhas injetadas

**Files:**
- Create: `internal/platform/faultinject/faultinject.go`, `internal/platform/faultinject/hit_off.go`, `internal/platform/faultinject/hit_on.go`
- Modify: `internal/adapters/sqsconsumer/consumer.go`, `internal/app/outbox_relay.go`
- Test: `internal/platform/faultinject/faultinject_test.go` (unit)

**Interfaces:**
- Produces:
  - Constantes `faultinject.CrashAfterCommitBeforeDelete = "crash_after_commit_before_delete"`,
    `faultinject.CrashAfterPublishBeforeMark = "crash_after_publish_before_mark"` e `faultinject.ExitCode = 86`.
  - Constante de build `faultinject.Enabled` (`false` sem a tag).
  - `faultinject.Hit(point string)`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/platform/faultinject/faultinject_test.go`:

```go
//go:build !integration && !e2e && !faultinject

package faultinject

import "testing"

func TestHitIsANoOpWithoutTheBuildTag(t *testing.T) {
	t.Setenv("FAULT", CrashAfterCommitBeforeDelete)
	if Enabled {
		t.Fatal("the default build must not enable fault injection")
	}
	Hit(CrashAfterCommitBeforeDelete) // must return: the process keeps running
	Hit(CrashAfterPublishBeforeMark)
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=unit ./internal/platform/faultinject/...`
Expected: FAIL de compilação, `undefined: CrashAfterCommitBeforeDelete`.

- [ ] **Step 3: Implementar**

`internal/platform/faultinject/faultinject.go`:

```go
// Package faultinject crashes the process at named points so end-to-end
// tests can prove recovery (spec §16). Only a binary built with the
// faultinject tag can crash; the normal build compiles Hit to nothing, so
// setting FAULT in production has no effect.
package faultinject

// Named crash points.
const (
	// CrashAfterCommitBeforeDelete: the SQS consumer committed the handling
	// of a message and is about to delete it.
	CrashAfterCommitBeforeDelete = "crash_after_commit_before_delete"
	// CrashAfterPublishBeforeMark: the outbox relay published an event and
	// is about to mark it published.
	CrashAfterPublishBeforeMark = "crash_after_publish_before_mark"
)

// ExitCode is the exit status of a process killed by an injected fault.
const ExitCode = 86
```

`internal/platform/faultinject/hit_off.go`:

```go
//go:build !faultinject

package faultinject

// Enabled reports whether this binary can inject faults.
const Enabled = false

// Hit does nothing in the normal build.
func Hit(string) {}
```

`internal/platform/faultinject/hit_on.go`:

```go
//go:build faultinject

package faultinject

import (
	"fmt"
	"os"
)

// Enabled reports whether this binary can inject faults.
const Enabled = true

// active is the point named by the FAULT environment variable.
var active = os.Getenv("FAULT")

// Hit exits the process at once (no deferred calls, no shutdown hooks) when
// point is the active fault, like a crash.
func Hit(point string) {
	if point == active {
		fmt.Fprintf(os.Stderr, "faultinject: crashing at %s\n", point)
		os.Exit(ExitCode)
	}
}
```

Em `internal/adapters/sqsconsumer/consumer.go`, no `settle`, no ramo `default` (delete), antes do `c.delete`:

```go
	default:
		faultinject.Hit(faultinject.CrashAfterCommitBeforeDelete)
		c.delete(sctx, msg, "the redelivery will be dropped by the inbox")
```

Em `internal/app/outbox_relay.go`, no `publish`, logo depois de `r.metrics.OutboxLag(...)` e antes do
`MarkPublished`:

```go
	faultinject.Hit(faultinject.CrashAfterPublishBeforeMark)
```

Nos dois arquivos, acrescente o import de
`github.com/brunopstephan/backend-challenge-go/internal/platform/faultinject`.

- [ ] **Step 4: Rodar os testes**

Run:

```bash
go test -race -tags=unit ./internal/platform/faultinject/...
go build ./... && go build -tags faultinject -o /tmp/wallet-fi ./cmd/wallet && echo build-ok
go test -race -count=1 ./...
```

Expected: `ok`, `build-ok` e a suíte inteira verde.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./... && go vet -tags faultinject ./...
git add internal/platform/faultinject internal/adapters/sqsconsumer/consumer.go internal/app/outbox_relay.go
git commit -m "feat(platform): build-tagged fault injection at the consumer delete and relay mark points" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Dockerfile, wallet com 3 réplicas e nginx

**Files:**
- Create: `Dockerfile`, `.dockerignore`, `deploy/nginx/nginx.conf`, `scripts/token.sh`, `scripts/sqs-send.sh`, `scripts/smoke.sh`
- Modify: `docker-compose.yml`

**Interfaces:**
- Produces:
  - Serviços `wallet` (build `.`, 3 réplicas) e `nginx` (`${WALLET_PORT:-8000}:80`).
  - Scripts:
    - `scripts/token.sh CLIENT` imprime um access token;
    - `scripts/sqs-send.sh PROFILE GROUP BODY` envia uma mensagem para `wager-transactions.fifo` com a credencial do
      profile;
    - `scripts/smoke.sh` faz o fluxo autenticado HTTP + SQS pelo nginx e confere a reconciliação.

- [ ] **Step 1: Escrever o Dockerfile**

`Dockerfile`:

```dockerfile
# syntax=docker/dockerfile:1
# Go version: go.mod declares go 1.25.11; the toolchain image is 1.25.x >= that.
FROM golang:1.25.14-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/wallet ./cmd/wallet

FROM alpine:3.24.2
RUN apk add --no-cache ca-certificates && adduser -D -H -u 10001 wallet
COPY --from=build /out/wallet /usr/local/bin/wallet
USER wallet
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/wallet"]
```

`.dockerignore`:

```
.git
.local
.superpowers
.env
bin
docs
deploy
migrations
scripts
e2e
*.md
```

- [ ] **Step 2: Escrever a configuração do nginx**

`deploy/nginx/nginx.conf`:

```nginx
worker_processes auto;
events { worker_connections 1024; }

http {
    access_log /dev/stdout;
    error_log /dev/stderr warn;

    # Docker's DNS returns one address per wallet replica. Resolving at
    # request time (variable proxy_pass) follows replicas that restart with a
    # new address; nginx round-robins across the returned addresses.
    resolver 127.0.0.11 valid=5s ipv6=off;

    server {
        listen 80;

        location / {
            set $wallet http://wallet:8080;
            proxy_pass $wallet;
            proxy_http_version 1.1;
            proxy_set_header Host $host;
            proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
            proxy_connect_timeout 2s;
            proxy_read_timeout 30s;
            # Only idempotent requests are retried on another replica; a POST
            # is retried by the client with its Idempotency-Key.
            proxy_next_upstream error timeout;
        }
    }
}
```

- [ ] **Step 3: Acrescentar os serviços ao compose**

Em `docker-compose.yml`, acrescentar:

```yaml
  wallet:
    build: .
    image: wallet:local
    deploy:
      replicas: 3
    environment:
      DATABASE_URL: postgres://wallet_app:${WALLET_APP_PASSWORD:-wallet_app}@postgres:5432/wallet?sslmode=disable
      HTTP_ADDR: ":8080"
      LOG_LEVEL: ${LOG_LEVEL:-info}
      OIDC_ISSUER_URL: http://localhost:${KEYCLOAK_PORT:-8080}/realms/wallet
      OIDC_JWKS_URL: http://keycloak:8080/realms/wallet/protocol/openid-connect/certs
      AWS_REGION: us-east-1
      AWS_SHARED_CREDENTIALS_FILE: /run/ministack/credentials
      SQS_ENDPOINT: http://ministack:4566
      SQS_CONSUMER_PROFILE: wallet-consumer
      SQS_PUBLISHER_PROFILE: wallet-publisher
    volumes:
      - ./.local/ministack:/run/ministack:ro
    depends_on:
      postgres:
        condition: service_healthy
      migrate:
        condition: service_completed_successfully
      keycloak:
        condition: service_healthy
      ministack_init:
        condition: service_completed_successfully
    healthcheck:
      test: ["CMD", "wget", "-q", "-O", "/dev/null", "http://127.0.0.1:8080/health/ready"]
      interval: 5s
      timeout: 3s
      retries: 30
      start_period: 10s
    # composition.StopTimeout with every toggle on is 77s.
    stop_grace_period: 90s

  nginx:
    image: nginx:1.30.5-alpine
    ports:
      - "${WALLET_PORT:-8000}:80"
    volumes:
      - ./deploy/nginx/nginx.conf:/etc/nginx/nginx.conf:ro
    depends_on:
      wallet:
        condition: service_healthy
```

Se a imagem `alpine` não tiver `wget` (BusyBox tem), troque o healthcheck por uma ferramenta que exista e registre a
troca.

- [ ] **Step 4: Escrever os scripts**

`scripts/token.sh`:

```bash
#!/usr/bin/env bash
# Prints an access token for a client of the wallet realm (client_credentials).
# Usage: scripts/token.sh provider-a|provider-b|wallet-internal
set -euo pipefail
client=${1:?client id}
kc=${KEYCLOAK_URL:-http://localhost:8080}
curl -sf "$kc/realms/wallet/protocol/openid-connect/token" \
  -d grant_type=client_credentials -d client_id="$client" -d client_secret="$client-secret" |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])'
```

`scripts/sqs-send.sh`:

```bash
#!/usr/bin/env bash
# Sends a message to wager-transactions.fifo as a provider, using the AWS CLI
# image of the compose project and the provisioned credentials file.
# Usage: scripts/sqs-send.sh PROFILE MESSAGE_GROUP_ID 'JSON BODY'
# The body's messageId is used as MessageDeduplicationId (producer contract).
set -euo pipefail
profile=${1:?profile} group=${2:?group} body=${3:?body}
dedup=$(python3 -c 'import json,sys; print(json.loads(sys.argv[1])["messageId"])' "$body")
docker compose run --rm --no-deps -T \
  -e AWS_SHARED_CREDENTIALS_FILE=/out/credentials -e AWS_PROFILE="$profile" -e AWS_DEFAULT_REGION=us-east-1 \
  --entrypoint aws ministack_init --endpoint-url http://ministack:4566 \
  sqs send-message --queue-url http://ministack:4566/000000000000/wager-transactions.fifo \
  --message-group-id "$group" --message-deduplication-id "$dedup" --message-body "$body" --output text --query MessageId
```

`scripts/smoke.sh`:

```bash
#!/usr/bin/env bash
# End-to-end smoke check of the running compose stack, through nginx:
# opens a wallet, bets over HTTP, bets over SQS, then reconciles.
set -euo pipefail
cd "$(dirname "$0")/.."
base=${WALLET_URL:-http://localhost:8000}
json() { python3 -c "import json,sys; d=json.load(sys.stdin); print($1)"; }
uuid() { python3 -c 'import uuid; print(uuid.uuid4())'; }

internal=$(scripts/token.sh wallet-internal)
provider=$(scripts/token.sh provider-a)
player=$(uuid)

wallet=$(curl -sf -X POST "$base/wallets" -H "Authorization: Bearer $internal" -H 'Content-Type: application/json' \
  -d "{\"playerId\":\"$player\",\"initialBalance\":{\"amount\":\"100.00\",\"currency\":\"BRL\"}}" | json 'd["id"]')
echo "wallet $wallet"

ext=$(uuid)
status=$(curl -s -o /dev/null -w '%{http_code}' -X POST "$base/wagering/transactions" \
  -H "Authorization: Bearer $provider" -H 'Content-Type: application/json' -H "Idempotency-Key: provider-a:$ext" \
  -d "{\"providerId\":\"provider-a\",\"externalTransactionId\":\"$ext\",\"playerId\":\"$player\",\"walletId\":\"$wallet\",\"roundId\":\"r1\",\"gameId\":\"g1\",\"kind\":\"BET\",\"money\":{\"amount\":\"25.00\",\"currency\":\"BRL\"}}")
[ "$status" = 200 ] || { echo "HTTP bet returned $status"; exit 1; }

ext2=$(uuid)
scripts/sqs-send.sh provider-a "$wallet" "{\"messageId\":\"msg-$ext2\",\"type\":\"WagerTransactionRequested\",\"occurredAt\":\"2026-09-08T12:00:00.000Z\",\"data\":{\"providerId\":\"provider-a\",\"externalTransactionId\":\"$ext2\",\"idempotencyKey\":\"provider-a:$ext2\",\"playerId\":\"$player\",\"walletId\":\"$wallet\",\"roundId\":\"r1\",\"gameId\":\"g1\",\"kind\":\"BET\",\"money\":{\"amount\":\"10.00\",\"currency\":\"BRL\"}}}" >/dev/null

for _ in $(seq 1 30); do
  balance=$(curl -sf "$base/wallets/$wallet" -H "Authorization: Bearer $internal" | json 'd["balance"]["amount"]')
  [ "$balance" = "65.00" ] && break
  sleep 1
done
[ "$balance" = "65.00" ] || { echo "balance $balance, want 65.00"; exit 1; }

consistent=$(curl -sf -X POST "$base/wallets/$wallet/reconciliation" -H "Authorization: Bearer $internal" | json 'd["consistent"]')
[ "$consistent" = "True" ] || { echo "reconciliation not consistent"; exit 1; }
echo "smoke ok: balance $balance, reconciliation consistent"
```

`chmod +x scripts/*.sh`.

- [ ] **Step 5: Subir e verificar**

Run:

```bash
docker compose config --quiet
docker compose up --build -d
for i in $(seq 1 60); do [ "$(docker compose ps wallet --format '{{.Health}}' | grep -c healthy)" = 3 ] && break; sleep 3; done
docker compose ps
scripts/smoke.sh
docker compose stop wallet && docker compose ps -a wallet --format '{{.Name}} {{.State}} {{.ExitCode}}'
```

Expected:
- 3 réplicas `healthy`, e o `smoke ok` impresso.
- Depois do `stop`, as réplicas saem com código 0, sem SIGKILL no meio da drenagem.

Se `docker compose up` falhar por um detalhe de ambiente (tag de imagem, ferramenta do healthcheck), faça o ajuste
mínimo e registre. Não mude o comportamento da aplicação. Ao final, deixe a stack de dev parada
(`docker compose stop`) e nunca rode `down -v`.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile .dockerignore docker-compose.yml deploy/nginx scripts
git commit -m "feat(deploy): wallet image, three replicas behind nginx and an authenticated smoke script" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Prometheus e Grafana provisionados

**Files:**
- Create: `deploy/prometheus/prometheus.yml`, `deploy/grafana/provisioning/datasources/prometheus.yml`, `deploy/grafana/provisioning/dashboards/dashboards.yml`, `deploy/grafana/dashboards/wallet.json`
- Modify: `docker-compose.yml`
- Test: `internal/platform/metrics/dashboard_test.go` (unit)

**Interfaces:**
- Consumes: `metrics.New()` e todos os seus métodos (Planos 4 e 5).
- Produces:
  - Serviços `prometheus` (`${PROMETHEUS_PORT:-9090}`) e `grafana` (`${GRAFANA_PORT:-3000}`).
  - Dashboard "Wallet Service" (uid `wallet-service`).

- [ ] **Step 1: Escrever o teste que falha**

`internal/platform/metrics/dashboard_test.go`:

```go
//go:build !integration && !e2e

package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// metricName matches the service's metric names inside PromQL expressions.
var metricName = regexp.MustCompile(`\b([a-z_]+_(?:total|seconds)(?:_bucket|_sum|_count)?)\b`)

func dashboardExprs(t *testing.T) []string {
	t.Helper()
	_, file, _, _ := runtime.Caller(0)
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "..", "deploy", "grafana", "dashboards", "wallet.json"))
	if err != nil {
		t.Fatal(err)
	}
	var dash struct {
		Panels []struct {
			Title   string `json:"title"`
			Targets []struct {
				Expr string `json:"expr"`
			} `json:"targets"`
		} `json:"panels"`
	}
	if err := json.Unmarshal(raw, &dash); err != nil {
		t.Fatalf("dashboard is not valid JSON: %v", err)
	}
	var exprs []string
	for _, p := range dash.Panels {
		if len(p.Targets) == 0 {
			t.Errorf("panel %q has no query", p.Title)
		}
		for _, target := range p.Targets {
			exprs = append(exprs, target.Expr)
		}
	}
	return exprs
}

func TestDashboardQueriesUseRegisteredMetrics(t *testing.T) {
	m := New()
	m.TransactionCompleted(wagering.KindBet, wagering.StatusProcessed, app.ChannelHTTP)
	m.IdempotentReplay(app.ChannelHTTP)
	m.ConcurrencyConflict(app.ConflictVersion)
	m.ProcessingDuration(app.ChannelHTTP, time.Millisecond)
	m.ReconciliationDivergence()
	m.InboxDuplicate()
	m.OutboxPublishAttempt(app.OutboxPublished)
	m.OutboxLag(time.Millisecond)
	m.ReferenceRetry()
	m.SQSRetry()
	m.SQSDeadLetter("INVALID_MONEY")
	m.HTTPRequests.WithLabelValues("GET", "/health/live", "200").Inc()
	m.HTTPDuration.WithLabelValues("GET", "/health/live").Observe(0.01)

	families, err := m.Registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	registered := map[string]bool{}
	for _, f := range families {
		registered[f.GetName()] = true
	}
	exprs := dashboardExprs(t)
	if len(exprs) < 10 {
		t.Fatalf("dashboard has %d queries, want at least 10", len(exprs))
	}
	used := map[string]bool{}
	for _, expr := range exprs {
		for _, match := range metricName.FindAllStringSubmatch(expr, -1) {
			name := match[1]
			for _, suffix := range []string{"_bucket", "_sum", "_count"} {
				if base := strings.TrimSuffix(name, suffix); base != name && registered[base] {
					name = base
				}
			}
			if !registered[name] {
				t.Errorf("query %q uses %s, which the service does not register", expr, name)
			}
			used[name] = true
		}
	}
	for _, want := range []string{
		"wager_transactions_total", "idempotent_replays_total", "inbox_duplicates_total", "sqs_retries_total",
		"sqs_dlq_total", "concurrency_conflicts_total", "outbox_lag_seconds", "outbox_publish_attempts_total",
		"processing_duration_seconds", "reconciliation_divergences_total", "reference_retries_total",
		"http_requests_total", "http_request_duration_seconds",
	} {
		if !used[want] {
			t.Errorf("no dashboard panel shows %s", want)
		}
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=unit ./internal/platform/metrics/ -run Dashboard`
Expected: FAIL, porque `wallet.json` não existe.

- [ ] **Step 3: Escrever o Prometheus e o provisionamento do Grafana**

`deploy/prometheus/prometheus.yml`:

```yaml
global:
  scrape_interval: 5s
  evaluation_interval: 5s

scrape_configs:
  # Every wallet replica, discovered through Docker's DNS (one A record per replica).
  - job_name: wallet
    metrics_path: /metrics
    dns_sd_configs:
      - names: [wallet]
        type: A
        port: 8080
        refresh_interval: 10s
```

`deploy/grafana/provisioning/datasources/prometheus.yml`:

```yaml
apiVersion: 1
datasources:
  - name: Prometheus
    uid: prometheus
    type: prometheus
    access: proxy
    url: http://prometheus:9090
    isDefault: true
    editable: false
```

`deploy/grafana/provisioning/dashboards/dashboards.yml`:

```yaml
apiVersion: 1
providers:
  - name: wallet
    folder: Wallet
    type: file
    disableDeletion: true
    allowUiUpdates: false
    options:
      path: /var/lib/grafana/dashboards
```

- [ ] **Step 4: Escrever o dashboard**

`deploy/grafana/dashboards/wallet.json`, com 14 painéis de série temporal sobre o datasource `prometheus`:

```json
{
  "uid": "wallet-service",
  "title": "Wallet Service",
  "tags": ["wallet"],
  "timezone": "browser",
  "schemaVersion": 39,
  "version": 1,
  "refresh": "5s",
  "time": { "from": "now-15m", "to": "now" },
  "panels": [
    { "id": 1, "type": "timeseries", "title": "Operações por status (req/s)", "gridPos": { "x": 0, "y": 0, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [ { "refId": "A", "expr": "sum by (status) (rate(wager_transactions_total[1m]))", "legendFormat": "{{status}}" } ] },
    { "id": 2, "type": "timeseries", "title": "Operações por tipo e canal (req/s)", "gridPos": { "x": 12, "y": 0, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [ { "refId": "A", "expr": "sum by (kind, channel) (rate(wager_transactions_total[1m]))", "legendFormat": "{{kind}} {{channel}}" } ] },
    { "id": 3, "type": "timeseries", "title": "Latência de processamento p50/p95/p99", "gridPos": { "x": 0, "y": 8, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [
        { "refId": "A", "expr": "histogram_quantile(0.5, sum by (le, channel) (rate(processing_duration_seconds_bucket[1m])))", "legendFormat": "p50 {{channel}}" },
        { "refId": "B", "expr": "histogram_quantile(0.95, sum by (le, channel) (rate(processing_duration_seconds_bucket[1m])))", "legendFormat": "p95 {{channel}}" },
        { "refId": "C", "expr": "histogram_quantile(0.99, sum by (le, channel) (rate(processing_duration_seconds_bucket[1m])))", "legendFormat": "p99 {{channel}}" } ] },
    { "id": 4, "type": "timeseries", "title": "Replays idempotentes (req/s)", "gridPos": { "x": 12, "y": 8, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [ { "refId": "A", "expr": "sum by (channel) (rate(idempotent_replays_total[1m]))", "legendFormat": "{{channel}}" } ] },
    { "id": 5, "type": "timeseries", "title": "Conflitos de concorrência (por tipo)", "gridPos": { "x": 0, "y": 16, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [ { "refId": "A", "expr": "sum by (type) (rate(concurrency_conflicts_total[1m]))", "legendFormat": "{{type}}" } ] },
    { "id": 6, "type": "timeseries", "title": "Duplicatas da inbox", "gridPos": { "x": 12, "y": 16, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [ { "refId": "A", "expr": "sum(rate(inbox_duplicates_total[1m]))", "legendFormat": "duplicates" } ] },
    { "id": 7, "type": "timeseries", "title": "Retries do SQS", "gridPos": { "x": 0, "y": 24, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [ { "refId": "A", "expr": "sum(rate(sqs_retries_total[1m]))", "legendFormat": "retries" } ] },
    { "id": 8, "type": "timeseries", "title": "DLQ por motivo", "gridPos": { "x": 12, "y": 24, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [ { "refId": "A", "expr": "sum by (reason) (increase(sqs_dlq_total[5m]))", "legendFormat": "{{reason}}" } ] },
    { "id": 9, "type": "timeseries", "title": "Atraso da outbox p50/p95", "gridPos": { "x": 0, "y": 32, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [
        { "refId": "A", "expr": "histogram_quantile(0.5, sum by (le) (rate(outbox_lag_seconds_bucket[1m])))", "legendFormat": "p50" },
        { "refId": "B", "expr": "histogram_quantile(0.95, sum by (le) (rate(outbox_lag_seconds_bucket[1m])))", "legendFormat": "p95" } ] },
    { "id": 10, "type": "timeseries", "title": "Publicações da outbox por resultado", "gridPos": { "x": 12, "y": 32, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [ { "refId": "A", "expr": "sum by (result) (rate(outbox_publish_attempts_total[1m]))", "legendFormat": "{{result}}" } ] },
    { "id": 11, "type": "timeseries", "title": "Retentativas de referência", "gridPos": { "x": 0, "y": 40, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [ { "refId": "A", "expr": "sum(rate(reference_retries_total[1m]))", "legendFormat": "retries" } ] },
    { "id": 12, "type": "timeseries", "title": "Divergências de reconciliação", "gridPos": { "x": 12, "y": 40, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [ { "refId": "A", "expr": "sum(increase(reconciliation_divergences_total[5m]))", "legendFormat": "divergences" } ] },
    { "id": 13, "type": "timeseries", "title": "HTTP por rota e status (req/s)", "gridPos": { "x": 0, "y": 48, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [ { "refId": "A", "expr": "sum by (route, status) (rate(http_requests_total[1m]))", "legendFormat": "{{route}} {{status}}" } ] },
    { "id": 14, "type": "timeseries", "title": "Latência HTTP p95 por rota", "gridPos": { "x": 12, "y": 48, "w": 12, "h": 8 },
      "datasource": { "type": "prometheus", "uid": "prometheus" },
      "targets": [ { "refId": "A", "expr": "histogram_quantile(0.95, sum by (le, route) (rate(http_request_duration_seconds_bucket[1m])))", "legendFormat": "{{route}}" } ] }
  ]
}
```

- [ ] **Step 5: Acrescentar os serviços ao compose**

```yaml
  prometheus:
    image: prom/prometheus:v3.15.0
    ports:
      - "${PROMETHEUS_PORT:-9090}:9090"
    volumes:
      - ./deploy/prometheus/prometheus.yml:/etc/prometheus/prometheus.yml:ro
    depends_on:
      wallet:
        condition: service_healthy

  grafana:
    image: grafana/grafana:13.2.3
    ports:
      - "${GRAFANA_PORT:-3000}:3000"
    environment:
      GF_SECURITY_ADMIN_PASSWORD: ${GRAFANA_ADMIN_PASSWORD:-admin}
      GF_AUTH_ANONYMOUS_ENABLED: "true"
      GF_AUTH_ANONYMOUS_ORG_ROLE: Viewer
      GF_DASHBOARDS_DEFAULT_HOME_DASHBOARD_PATH: /var/lib/grafana/dashboards/wallet.json
    volumes:
      - ./deploy/grafana/provisioning:/etc/grafana/provisioning:ro
      - ./deploy/grafana/dashboards:/var/lib/grafana/dashboards:ro
    depends_on:
      - prometheus
```

- [ ] **Step 6: Rodar os testes e verificar a stack**

Run:

```bash
go test -race -tags=unit ./internal/platform/metrics/...
docker compose run --rm --no-deps --entrypoint promtool prometheus check config /etc/prometheus/prometheus.yml
docker compose up --build -d
sleep 20
curl -sf 'http://localhost:9090/api/v1/query' --data-urlencode 'query=count(up{job="wallet"} == 1)'
scripts/smoke.sh
curl -sf 'http://localhost:9090/api/v1/query' --data-urlencode 'query=sum(wager_transactions_total)'
curl -sf 'http://localhost:3000/api/search?query=Wallet%20Service'
docker compose stop
```

Expected:
- `ok` e `SUCCESS` do `promtool`.
- O Prometheus devolve `"3"` para os alvos do job wallet.
- A soma de `wager_transactions_total` é maior que 0 depois do smoke.
- A busca do Grafana devolve o dashboard `wallet-service`.

- [ ] **Step 7: Commit**

```bash
gofmt -l . && go vet ./...
git add deploy/prometheus deploy/grafana docker-compose.yml internal/platform/metrics/dashboard_test.go
git commit -m "feat(observability): provisioned Prometheus scraping every replica and Grafana dashboard" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: E2E — harness multi-processo e cenários de concorrência

**Files:**
- Create: `e2e/harness_test.go`, `e2e/concurrency_test.go`

**Interfaces:**
- Consumes:
  - `faultinject` (Task 1);
  - `pgtest.FreshDatabase/Open`, `sqstest.*` e `kctest.Token/IssuerURL`;
  - o contrato HTTP (Plano 4) e o contrato SQS (Plano 5).
- Produces (pacote `e2e`, só para testes):
  - Cluster:
    - `newCluster(t, opts clusterOptions) *cluster`;
    - `(*cluster).start(t, name string, env map[string]string) *proc`.
  - Processo:
    - `(*proc).url() string`, `stop(t) int` (SIGTERM; devolve o exit code), `kill(t)` (SIGKILL);
    - `(*proc).waitExit(t, timeout) int`;
    - `(*proc).metric(t, name string) float64`.
  - Operações:
    - `openWallet(t, p, amount) wallet`;
    - `postTx(t, p, token string, body txRequest) (int, map[string]any)`;
    - `sendSQS(t, provider string, w wallet, kind, amount, ext, ref, msgID string)`.
  - Consultas:
    - `balance(t, p, w) string` e `reconcile(t, p, w)`, que falha se não for consistente;
    - `getByExternal(t, p, token, provider, ext) map[string]any`;
    - `eventually(t, timeout, what string, cond func() bool)`.

- [ ] **Step 1: Escrever o harness**

`e2e/harness_test.go`:

```go
//go:build !unit && !integration

// Package e2e runs the real wallet binary as several OS processes against
// the test infrastructure (postgres_test, keycloak_test, ministack_test).
// TestMain builds the binary with the faultinject tag so recovery scenarios
// can crash a process at a named point (FAULT=...).
package e2e

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "wallet-e2e-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	binary = filepath.Join(dir, "wallet")
	_, file, _, _ := runtime.Caller(0)
	build := exec.Command("go", "build", "-race", "-tags", "faultinject", "-o", binary, "./cmd/wallet")
	build.Dir = filepath.Join(filepath.Dir(file), "..")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "build wallet binary:", err)
		os.Exit(1)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

type clusterOptions struct {
	// VisibilityTimeout of the per-test input queue (seconds; 0 = 30).
	VisibilityTimeout int
	// Env applies to every process of the cluster.
	Env map[string]string
}

type cluster struct {
	dbURL  string
	db     *gorm.DB
	queues sqstest.Queues
	env    map[string]string
	procs  []*proc
}

type proc struct {
	name   string
	addr   string
	cmd    *exec.Cmd
	log    *bytes.Buffer
	logMu  *sync.Mutex
	exited chan struct{}
	code   int
}

// newCluster creates a fresh database and queues; processes are started with start.
func newCluster(t *testing.T, opts clusterOptions) *cluster {
	t.Helper()
	c := &cluster{dbURL: pgtest.FreshDatabase(t), queues: sqstest.NewQueues(t, sqstest.Options{VisibilityTimeout: opts.VisibilityTimeout})}
	c.db = pgtest.Open(t, c.dbURL)
	c.env = map[string]string{
		"PATH": os.Getenv("PATH"), "HOME": os.Getenv("HOME"),
		"DATABASE_URL": c.dbURL, "OIDC_ISSUER_URL": kctest.IssuerURL(), "LOG_LEVEL": "info",
		"AWS_ACCESS_KEY_ID": "test", "AWS_SECRET_ACCESS_KEY": "test", "AWS_REGION": sqstest.Region,
		"SQS_ENDPOINT": sqstest.Endpoint(), "SQS_INPUT_QUEUE": c.queues.Input.Name,
		"SQS_INPUT_DLQ": c.queues.InputDLQ.Name, "SQS_EVENTS_QUEUE": c.queues.Events.Name,
		"SQS_WAIT_TIME": "1s", "SQS_RETRY_BASE_DELAY": "1s", "SQS_RETRY_MAX_DELAY": "2s",
		"SQS_SHUTDOWN_TIMEOUT": "5s", "HTTP_SHUTDOWN_TIMEOUT": "5s",
		"OUTBOX_POLL_INTERVAL": "100ms", "REFWORKER_POLL_INTERVAL": "100ms",
		"OUTBOX_SHUTDOWN_TIMEOUT": "5s", "REFWORKER_SHUTDOWN_TIMEOUT": "5s",
	}
	for k, v := range opts.Env {
		c.env[k] = v
	}
	t.Cleanup(func() {
		for _, p := range c.procs {
			p.kill(t)
		}
		if t.Failed() {
			for _, p := range c.procs {
				t.Logf("---- %s log (tail) ----\n%s", p.name, p.tail(60))
			}
		}
	})
	return c
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// start launches a process and waits until it is ready (or, with FAULT set,
// until it is ready or has crashed).
func (c *cluster) start(t *testing.T, name string, env map[string]string) *proc {
	t.Helper()
	p := &proc{name: name, addr: freePort(t), log: &bytes.Buffer{}, logMu: &sync.Mutex{}, exited: make(chan struct{})}
	all := map[string]string{"HTTP_ADDR": p.addr}
	for k, v := range c.env {
		all[k] = v
	}
	for k, v := range env {
		all[k] = v
	}
	p.cmd = exec.Command(binary)
	for k, v := range all {
		p.cmd.Env = append(p.cmd.Env, k+"="+v)
	}
	w := &lockedWriter{buf: p.log, mu: p.logMu}
	p.cmd.Stdout, p.cmd.Stderr = w, w
	if err := p.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		err := p.cmd.Wait()
		p.code = exitCode(err)
		close(p.exited)
	}()
	c.procs = append(c.procs, p)
	eventually(t, 30*time.Second, name+" ready", func() bool {
		select {
		case <-p.exited:
			t.Fatalf("%s exited with %d before becoming ready:\n%s", name, p.code, p.tail(40))
		default:
		}
		resp, err := http.Get(p.url() + "/health/ready")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	})
	return p
}

type lockedWriter struct {
	buf *bytes.Buffer
	mu  *sync.Mutex
}

func (w *lockedWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.Write(b)
}

func exitCode(err error) int {
	if err == nil {
		return 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return 128 + int(ws.Signal())
		}
		return ee.ExitCode()
	}
	return -1
}

func (p *proc) url() string { return "http://" + p.addr }

func (p *proc) tail(n int) string {
	p.logMu.Lock()
	defer p.logMu.Unlock()
	lines := strings.Split(strings.TrimRight(p.log.String(), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// stop sends SIGTERM and returns the exit code.
func (p *proc) stop(t *testing.T) int {
	t.Helper()
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	return p.waitExit(t, 60*time.Second)
}

// kill sends SIGKILL (no cleanup) unless the process already exited.
func (p *proc) kill(t *testing.T) {
	select {
	case <-p.exited:
		return
	default:
	}
	_ = p.cmd.Process.Kill()
	<-p.exited
}

func (p *proc) waitExit(t *testing.T, timeout time.Duration) int {
	t.Helper()
	select {
	case <-p.exited:
		return p.code
	case <-time.After(timeout):
		t.Fatalf("%s did not exit within %s", p.name, timeout)
		return -1
	}
}

// metric returns the sum of a counter (all label sets) from /metrics.
func (p *proc) metric(t *testing.T, name string) float64 {
	t.Helper()
	resp, err := http.Get(p.url() + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sum := 0.0
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, name) || strings.HasPrefix(line, "#") {
			continue
		}
		rest := strings.TrimPrefix(line, name)
		if rest != "" && rest[0] != ' ' && rest[0] != '{' {
			continue
		}
		fields := strings.Fields(line)
		v, err := strconv.ParseFloat(fields[len(fields)-1], 64)
		if err == nil {
			sum += v
		}
	}
	return sum
}

func eventually(t *testing.T, timeout time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out after %s waiting for %s", timeout, what)
}

// ---- HTTP and SQS helpers ----

type wallet struct{ id, player string }

var httpClient = &http.Client{Timeout: 30 * time.Second}

func call(t *testing.T, method, url, token, body string, headers ...string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatalf("%s %s: body %q: %v", method, url, raw, err)
		}
	}
	return resp.StatusCode, out
}

func openWallet(t *testing.T, p *proc, amount string) wallet {
	t.Helper()
	player := uuid.NewString()
	code, body := call(t, "POST", p.url()+"/wallets", kctest.Token(t, "wallet-internal"),
		fmt.Sprintf(`{"playerId":%q,"initialBalance":{"amount":%q,"currency":"BRL"}}`, player, amount))
	if code != http.StatusCreated {
		t.Fatalf("open wallet = %d %v", code, body)
	}
	return wallet{id: body["id"].(string), player: player}
}

type txRequest struct {
	Provider, Ext, Kind, Amount, Ref string
	W                                wallet
}

func (r txRequest) json() string {
	ref := ""
	if r.Ref != "" {
		ref = fmt.Sprintf(`,"referenceExternalTransactionId":%q`, r.Ref)
	}
	return fmt.Sprintf(`{"providerId":%q,"externalTransactionId":%q,"playerId":%q,"walletId":%q,"roundId":"round-1",`+
		`"gameId":"game-1","kind":%q,"money":{"amount":%q,"currency":"BRL"}%s}`,
		r.Provider, r.Ext, r.W.player, r.W.id, r.Kind, r.Amount, ref)
}

func postTx(t *testing.T, p *proc, token string, r txRequest) (int, map[string]any) {
	t.Helper()
	return call(t, "POST", p.url()+"/wagering/transactions", token, r.json(), "Idempotency-Key", r.Provider+":"+r.Ext)
}

// sqsBody is the input message for r (idempotency key provider:ext).
func sqsBody(msgID string, r txRequest) string {
	return fmt.Sprintf(`{"messageId":%q,"type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z",`+
		`"data":{"providerId":%q,"externalTransactionId":%q,"idempotencyKey":%q,"playerId":%q,"walletId":%q,`+
		`"roundId":"round-1","gameId":"game-1","kind":%q,"money":{"amount":%q,"currency":"BRL"}%s}}`,
		msgID, r.Provider, r.Ext, r.Provider+":"+r.Ext, r.W.player, r.W.id, r.Kind, r.Amount, refField(r.Ref))
}

func refField(ref string) string {
	if ref == "" {
		return ""
	}
	return fmt.Sprintf(`,"referenceExternalTransactionId":%q`, ref)
}

// sendSQS sends body as provider-a's account with a fresh deduplication id
// (so a resend is a new SQS message carrying the same messageId).
func (c *cluster) sendSQS(t *testing.T, body, group string) {
	t.Helper()
	sqstest.Send(t, sqstest.Client(t, sqstest.ProviderAAccount), c.queues.Input.URL, body, group)
}

func balance(t *testing.T, p *proc, w wallet) string {
	t.Helper()
	code, body := call(t, "GET", p.url()+"/wallets/"+w.id, kctest.Token(t, "wallet-internal"), "")
	if code != http.StatusOK {
		t.Fatalf("get wallet = %d %v", code, body)
	}
	return body["balance"].(map[string]any)["amount"].(string)
}

// reconcile requires stored balance == credits − debits of the ledger.
func reconcile(t *testing.T, p *proc, w wallet) {
	t.Helper()
	code, body := call(t, "POST", p.url()+"/wallets/"+w.id+"/reconciliation", kctest.Token(t, "wallet-internal"), "")
	if code != http.StatusOK || body["consistent"] != true {
		t.Fatalf("reconciliation of %s = %d %v", w.id, code, body)
	}
}

func getByExternal(t *testing.T, p *proc, provider, ext string) map[string]any {
	t.Helper()
	code, body := call(t, "GET", p.url()+"/providers/"+provider+"/wagering/transactions/"+ext, kctest.Token(t, provider), "")
	if code != http.StatusOK {
		return nil
	}
	return body
}

// queueEmpty reports whether the queue holds no visible or in-flight messages.
func (c *cluster) queueEmpty(t *testing.T, url string) bool {
	t.Helper()
	out, err := sqstest.Owner(t).GetQueueAttributes(context.Background(), &sqs.GetQueueAttributesInput{
		QueueUrl:       aws.String(url),
		AttributeNames: []sqstypes.QueueAttributeName{"ApproximateNumberOfMessages", "ApproximateNumberOfMessagesNotVisible"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return out.Attributes["ApproximateNumberOfMessages"] == "0" && out.Attributes["ApproximateNumberOfMessagesNotVisible"] == "0"
}
```

Acrescente o import `sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"`. Se a assinatura de algum helper
de `pgtest` ou `sqstest` divergir, adapte o mínimo e registre.

- [ ] **Step 2: Escrever os cenários de concorrência**

`e2e/concurrency_test.go`:

```go
//go:build !unit && !integration

package e2e

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
)

func threeInstances(t *testing.T, opts clusterOptions) (*cluster, []*proc) {
	t.Helper()
	c := newCluster(t, opts)
	return c, []*proc{c.start(t, "a", nil), c.start(t, "b", nil), c.start(t, "c", nil)}
}

// Scenario 1: the same bet 50 times in parallel across 3 instances → one debit.
func TestSameBetFiftyTimesAcrossInstances(t *testing.T) {
	_, ps := threeInstances(t, clusterOptions{})
	w := openWallet(t, ps[0], "100.00")
	token := kctest.Token(t, "provider-a")
	req := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "25.00", W: w}

	type result struct {
		code int
		body map[string]any
	}
	results := make([]result, 50)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			code, body := postTxNoFatal(ps[i%3], token, req)
			results[i] = result{code, body}
		}()
	}
	close(start)
	wg.Wait()

	fresh, id := 0, ""
	for i, r := range results {
		if r.code != http.StatusOK || r.body["status"] != "PROCESSED" || r.body["balance"].(map[string]any)["amount"] != "75.00" {
			t.Fatalf("request %d = %d %v", i, r.code, r.body)
		}
		if r.body["idempotentReplay"] == false {
			fresh++
		}
		if id == "" {
			id = r.body["transactionId"].(string)
		} else if r.body["transactionId"] != id {
			t.Fatalf("request %d answered another transaction %v", i, r.body["transactionId"])
		}
	}
	if fresh != 1 {
		t.Fatalf("%d requests applied the bet, want exactly 1 (49 replays)", fresh)
	}
	if b := balance(t, ps[1], w); b != "75.00" {
		t.Fatalf("balance = %s, want one debit", b)
	}
	reconcile(t, ps[2], w)
}

// postTxNoFatal is postTx for goroutines: it never calls t.Fatal.
func postTxNoFatal(p *proc, token string, r txRequest) (int, map[string]any) {
	req, _ := http.NewRequest("POST", p.url()+"/wagering/transactions", stringsReader(r.json()))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Idempotency-Key", r.Provider+":"+r.Ext)
	resp, err := httpClient.Do(req)
	if err != nil {
		return -1, map[string]any{"error": err.Error()}
	}
	defer resp.Body.Close()
	return resp.StatusCode, decodeBody(resp)
}

// Scenario 2: 80 + 80 on 100 from two instances while the wallet row is held
// by an outside transaction: both wait on the database lock across
// processes; one is processed, the other rejected; resends are unchanged.
func TestTwoBetsOfEightyOnOneHundred(t *testing.T) {
	c, ps := threeInstances(t, clusterOptions{Env: map[string]string{"DB_LOCK_TIMEOUT": "15s"}})
	token := kctest.Token(t, "provider-a")
	for round := range 3 {
		w := openWallet(t, ps[0], "100.00")
		first := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "80.00", W: w}
		second := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "80.00", W: w}

		holder := c.db.Begin()
		if err := holder.Exec(`SELECT id FROM wallets WHERE id = ? FOR UPDATE`, w.id).Error; err != nil {
			t.Fatal(err)
		}
		codes := make([]int, 2)
		bodies := make([]map[string]any, 2)
		var wg sync.WaitGroup
		for i, r := range []txRequest{first, second} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes[i], bodies[i] = postTxNoFatal(ps[i], token, r)
			}()
		}
		time.Sleep(time.Second) // both requests are now blocked on the wallet lock
		holder.Rollback()
		wg.Wait()

		processed, rejected := -1, -1
		for i := range codes {
			switch {
			case codes[i] == http.StatusOK && bodies[i]["status"] == "PROCESSED":
				processed = i
			case codes[i] == http.StatusUnprocessableEntity && bodies[i]["failureCode"] == "INSUFFICIENT_FUNDS":
				rejected = i
			}
		}
		if processed < 0 || rejected < 0 {
			t.Fatalf("round %d: responses %d %v / %d %v, want one PROCESSED and one INSUFFICIENT_FUNDS", round, codes[0], bodies[0], codes[1], bodies[1])
		}
		if b := balance(t, ps[2], w); b != "20.00" {
			t.Fatalf("round %d: balance = %s, want 20.00", round, b)
		}
		for i, r := range []txRequest{first, second} {
			code, body := postTx(t, ps[2], token, r)
			if code != codes[i] || body["transactionId"] != bodies[i]["transactionId"] || body["idempotentReplay"] != true {
				t.Fatalf("round %d: resend %d = %d %v, want the original %d %v", round, i, code, body, codes[i], bodies[i])
			}
		}
		reconcile(t, ps[1], w)
	}
}

// Scenario 3: distinct wallets processed simultaneously across instances.
func TestDistinctWalletsInParallel(t *testing.T) {
	_, ps := threeInstances(t, clusterOptions{})
	token := kctest.Token(t, "provider-a")
	wallets := make([]wallet, 10)
	for i := range wallets {
		wallets[i] = openWallet(t, ps[i%3], "50.00")
	}
	var wg sync.WaitGroup
	failures := make(chan string, 100)
	for i, w := range wallets {
		for j := range 10 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				r := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "1.00", W: w}
				if code, body := postTxNoFatal(ps[(i+j)%3], token, r); code != http.StatusOK {
					failures <- fmt.Sprintf("wallet %d bet %d = %d %v", i, j, code, body)
				}
			}()
		}
	}
	wg.Wait()
	close(failures)
	for f := range failures {
		t.Error(f)
	}
	for _, w := range wallets {
		if b := balance(t, ps[0], w); b != "40.00" {
			t.Fatalf("wallet %s balance = %s, want 40.00", w.id, b)
		}
		reconcile(t, ps[1], w)
	}
}

// Cross-channel: the same operation by HTTP and by SQS (any instance) → one debit.
func TestSameOperationOverHTTPAndSQS(t *testing.T) {
	c, ps := threeInstances(t, clusterOptions{})
	w := openWallet(t, ps[0], "100.00")
	r := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "30.00", W: w}
	if code, body := postTx(t, ps[0], kctest.Token(t, "provider-a"), r); code != http.StatusOK {
		t.Fatalf("http = %d %v", code, body)
	}
	c.sendSQS(t, sqsBody("msg-"+uuid.NewString(), r), w.id)
	eventually(t, 20*time.Second, "SQS message consumed", func() bool { return c.queueEmpty(t, c.queues.Input.URL) })
	replays := 0.0
	for _, p := range ps {
		replays += p.metric(t, "idempotent_replays_total")
	}
	if replays < 1 {
		t.Fatal("the SQS delivery must be answered as a replay")
	}
	if b := balance(t, ps[1], w); b != "70.00" {
		t.Fatalf("balance = %s, want one debit", b)
	}
	reconcile(t, ps[2], w)
}

// Repeated SQS receptions of one message (new deduplication id each time)
// are dropped by the application's inbox, not by SQS.
func TestRepeatedSQSReceptionsAreDeduplicated(t *testing.T) {
	c, ps := threeInstances(t, clusterOptions{})
	w := openWallet(t, ps[0], "100.00")
	body := sqsBody("msg-"+uuid.NewString(), txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "10.00", W: w})
	for range 5 {
		c.sendSQS(t, body, w.id)
	}
	eventually(t, 30*time.Second, "all receptions handled", func() bool { return c.queueEmpty(t, c.queues.Input.URL) })
	duplicates := 0.0
	for _, p := range ps {
		duplicates += p.metric(t, "inbox_duplicates_total")
	}
	if duplicates != 4 {
		t.Fatalf("inbox duplicates = %v, want 4 (5 receptions, 1 applied)", duplicates)
	}
	if b := balance(t, ps[1], w); b != "90.00" {
		t.Fatalf("balance = %s, want one debit", b)
	}
	reconcile(t, ps[2], w)
}
```

Acrescente ao `harness_test.go` os helpers `stringsReader(s string) io.Reader` (`strings.NewReader`) e
`decodeBody(resp *http.Response) map[string]any`, que lê o JSON e devolve um mapa vazio se falhar.

- [ ] **Step 3: Rodar**

Run:

```bash
docker compose --profile test up -d --wait postgres_test keycloak_test ministack_test && docker compose --profile test run --rm migrate_test && docker compose --profile test run --rm ministack_test_init
go test -race -count=1 -tags=e2e ./e2e/ -run 'Fifty|Eighty|Distinct|HTTPAndSQS|Repeated' -v 2>&1 | tail -40
```

Expected: os 5 testes em `PASS`. Rode duas vezes para checar estabilidade (`-count=2`).

- [ ] **Step 4: Commit**

```bash
gofmt -l . && go vet -tags=e2e ./e2e/...
git add e2e
git commit -m "test(e2e): multi-process harness and concurrency scenarios across three instances" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: E2E — falhas, recuperação e restart

**Files:**
- Create: `e2e/recovery_test.go`

**Interfaces:**
- Consumes: o harness da Task 4 e `faultinject.*` da Task 1.

- [ ] **Step 1: Escrever os cenários**

`e2e/recovery_test.go`:

```go
//go:build !unit && !integration

package e2e

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/faultinject"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

// Scenario 5: a consumer crashes after the commit and before deleting the
// message; another instance receives it again and drops it via the inbox.
func TestCrashAfterCommitBeforeDelete(t *testing.T) {
	c := newCluster(t, clusterOptions{VisibilityTimeout: 3})
	crashing := c.start(t, "crashing", map[string]string{"FAULT": faultinject.CrashAfterCommitBeforeDelete})
	w := openWallet(t, crashing, "100.00")
	c.sendSQS(t, sqsBody("msg-"+uuid.NewString(), txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "40.00", W: w}), w.id)
	if code := crashing.waitExit(t, 30*time.Second); code != faultinject.ExitCode {
		t.Fatalf("exit code = %d, want the injected crash %d", code, faultinject.ExitCode)
	}

	survivor := c.start(t, "survivor", nil)
	if b := balance(t, survivor, w); b != "60.00" {
		t.Fatalf("balance after crash = %s, want the committed debit", b)
	}
	eventually(t, 30*time.Second, "redelivery handled", func() bool { return c.queueEmpty(t, c.queues.Input.URL) })
	if d := survivor.metric(t, "inbox_duplicates_total"); d < 1 {
		t.Fatalf("inbox duplicates = %v, the redelivery must be dropped by the inbox", d)
	}
	if b := balance(t, survivor, w); b != "60.00" {
		t.Fatalf("balance after redelivery = %s, want one debit", b)
	}
	reconcile(t, survivor, w)
}

// Scenario 6: two publishers share the outbox; one crashes after publishing
// and before marking; the other republishes with the same eventId after the
// lease expires. SQS FIFO deduplication keeps a single copy in the queue.
func TestCrashAfterPublishBeforeMark(t *testing.T) {
	c := newCluster(t, clusterOptions{Env: map[string]string{"OUTBOX_LEASE": "2s", "CONSUMER_ENABLED": "false", "REFWORKER_ENABLED": "false"}})
	// A slower poll on the crashing publisher leaves time for the HTTP response
	// of the wallet opening before its relay claims the OPENING events and crashes.
	crashing := c.start(t, "crashing", map[string]string{"FAULT": faultinject.CrashAfterPublishBeforeMark, "OUTBOX_POLL_INTERVAL": "1s"})
	w := openWallet(t, crashing, "100.00")
	if code := crashing.waitExit(t, 30*time.Second); code != faultinject.ExitCode {
		t.Fatalf("exit code = %d, want the injected crash %d", code, faultinject.ExitCode)
	}

	b1 := c.start(t, "publisher-b", nil)
	b2 := c.start(t, "publisher-c", nil)
	if code, body := postTx(t, b1, kctest.Token(t, "provider-a"), txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "5.00", W: w}); code != 200 {
		t.Fatalf("bet = %d %v", code, body)
	}
	_ = b2

	var rows []struct {
		ID       string
		Attempts int
	}
	eventually(t, 30*time.Second, "every event published", func() bool {
		var pending int64
		if err := c.db.Raw(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&pending).Error; err != nil {
			t.Fatal(err)
		}
		return pending == 0
	})
	if err := c.db.Raw(`SELECT id::text AS id, attempts FROM outbox_events`).Scan(&rows).Error; err != nil {
		t.Fatal(err)
	}
	republished := 0
	for _, r := range rows {
		if r.Attempts >= 2 {
			republished++
		}
	}
	if republished < 1 {
		t.Fatalf("no event was republished after the crash: %+v", rows)
	}

	seen := map[string]int{}
	owner := sqstest.Owner(t)
	for deadline := time.Now().Add(20 * time.Second); len(seen) < len(rows) && time.Now().Before(deadline); {
		for _, m := range sqstest.Receive(t, owner, c.queues.Events.URL, 2*time.Second) {
			var env struct {
				EventID string `json:"eventId"`
			}
			if err := json.Unmarshal([]byte(aws.ToString(m.Body)), &env); err != nil {
				t.Fatal(err)
			}
			seen[env.EventID]++
			sqstest.Delete(t, owner, c.queues.Events.URL, m)
		}
	}
	for _, r := range rows {
		if seen[r.ID] != 1 {
			t.Fatalf("event %s delivered %d times, want exactly once (republished with the same eventId)", r.ID, seen[r.ID])
		}
	}
}

// Scenario 7: reversals delivered before their reference are resolved by the
// worker (on any instance) or expire with REFERENCE_NOT_FOUND.
func TestReversalBeforeReference(t *testing.T) {
	c := newCluster(t, clusterOptions{Env: map[string]string{
		"REFERENCE_RETRY_BASE_DELAY": "200ms", "REFERENCE_RETRY_MAX_DELAY": "200ms", "REFERENCE_RETRY_MAX_ATTEMPTS": "20",
	}})
	a, b := c.start(t, "a", nil), c.start(t, "b", nil)
	token := kctest.Token(t, "provider-a")
	w := openWallet(t, a, "100.00")

	bet := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "30.00", W: w}
	rollback := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "ROLLBACK", Amount: "30.00", Ref: bet.Ext, W: w}
	c.sendSQS(t, sqsBody("msg-"+uuid.NewString(), rollback), w.id)
	eventually(t, 20*time.Second, "rollback pending", func() bool {
		v := getByExternal(t, a, "provider-a", rollback.Ext)
		return v != nil && v["status"] == "PENDING_REFERENCE"
	})
	if code, body := postTx(t, b, token, bet); code != 200 {
		t.Fatalf("bet = %d %v", code, body)
	}
	eventually(t, 20*time.Second, "rollback resolved", func() bool {
		v := getByExternal(t, b, "provider-a", rollback.Ext)
		return v != nil && v["status"] == "PROCESSED"
	})
	if bal := balance(t, a, w); bal != "100.00" {
		t.Fatalf("balance = %s, want the bet rolled back", bal)
	}

	orphan := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "REFUND", Amount: "10.00", Ref: "never-" + uuid.NewString(), W: w}
	if code, body := postTx(t, a, token, orphan); code != 202 {
		t.Fatalf("orphan refund = %d %v, want 202 PENDING_REFERENCE", code, body)
	}
	eventually(t, 20*time.Second, "orphan expired", func() bool {
		v := getByExternal(t, b, "provider-a", orphan.Ext)
		return v != nil && v["status"] == "REJECTED" && v["failureCode"] == "REFERENCE_NOT_FOUND"
	})
	reconcile(t, b, w)
}

// Scenario 8: kill -9 and SIGTERM; a new instance preserves idempotency,
// resumes pending operations and keeps the books consistent.
func TestRestartPreservesIdempotencyAndPendings(t *testing.T) {
	c := newCluster(t, clusterOptions{Env: map[string]string{
		"REFERENCE_RETRY_BASE_DELAY": "1s", "REFERENCE_RETRY_MAX_DELAY": "1s", "REFERENCE_RETRY_MAX_ATTEMPTS": "60",
	}})
	a, b := c.start(t, "a", nil), c.start(t, "b", nil)
	token := kctest.Token(t, "provider-a")
	w := openWallet(t, a, "100.00")
	bet := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "20.00", W: w}
	_, original := postTx(t, a, token, bet)
	laterBet := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "BET", Amount: "15.00", W: w}
	refund := txRequest{Provider: "provider-a", Ext: uuid.NewString(), Kind: "REFUND", Amount: "15.00", Ref: laterBet.Ext, W: w}
	if code, body := postTx(t, b, token, refund); code != 202 {
		t.Fatalf("refund = %d %v, want pending", code, body)
	}

	a.kill(t)
	if code := b.stop(t); code != 0 {
		t.Fatalf("graceful stop exit code = %d, want 0", code)
	}

	n := c.start(t, "restarted", nil)
	code, replay := postTx(t, n, token, bet)
	if code != 200 || replay["idempotentReplay"] != true || replay["transactionId"] != original["transactionId"] ||
		replay["balance"].(map[string]any)["amount"] != "80.00" {
		t.Fatalf("replay after restart = %d %v, want the original result %v", code, replay, original)
	}
	if code, body := postTx(t, n, token, laterBet); code != 200 {
		t.Fatalf("later bet = %d %v", code, body)
	}
	eventually(t, 20*time.Second, "pending refund resumed", func() bool {
		v := getByExternal(t, n, "provider-a", refund.Ext)
		return v != nil && v["status"] == "PROCESSED"
	})
	if bal := balance(t, n, w); bal != "80.00" {
		t.Fatalf("balance = %s, want 100 − 20 − 15 + 15", bal)
	}
	reconcile(t, n, w)
}
```

- [ ] **Step 2: Rodar**

Run: `go test -race -count=1 -tags=e2e ./e2e/ -run 'Crash|Reversal|Restart' -v 2>&1 | tail -40`
Expected: os 4 testes em `PASS`. Depois rode o pacote inteiro duas vezes:
`go test -race -count=2 -tags=e2e ./e2e/...`.

Se um cenário falhar por um bug real do serviço, não enfraqueça a asserção. Reporte DONE_WITH_CONCERNS com o log.

- [ ] **Step 3: Verificação completa**

```bash
gofmt -l . && go vet ./... && go vet -tags=e2e ./... && go vet -tags faultinject ./...
go test -race -count=1 ./...
go test -race -count=1 -tags=unit ./... && go test -race -count=1 -tags=integration ./... && go test -race -count=1 -tags=e2e ./...
```

Expected: tudo `ok`. Cada tag roda só a sua camada, e sem tag roda tudo.

- [ ] **Step 4: Commit**

```bash
git add e2e
git commit -m "test(e2e): crash, republication, pending-reference and restart recovery scenarios" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Documentação — README, ARCHITECTURE e `.env.example`

**Files:**
- Modify: `README.md`
- Create: `ARCHITECTURE.md`, `.env.example`

**Interfaces:**
- Consumes: o código, o compose, os scripts e a spec. Todo valor citado (porta, variável, padrão, código de
  falha, nome de fila e de métrica) deve ser conferido no código ou na config antes de ser escrito.

Esta tarefa é documentação: não há teste automatizado. Os critérios de aceite são a lista abaixo, e o revisor confere
cada item contra o código.

- [ ] **Step 1: Escrever o `.env.example`**

Acrescente todas as variáveis lidas por `internal/platform/config` e pelo `docker-compose.yml`, com os valores locais
padrão e um comentário curto por grupo. Os grupos são:
- **Banco:** `DATABASE_URL`, `DB_*`, `POSTGRES_*` e `WALLET_OWNER_PASSWORD`/`WALLET_APP_PASSWORD`.
- **HTTP:** `HTTP_*`.
- **Log:** `LOG_LEVEL`.
- **OIDC:** `OIDC_*`.
- **SQS e AWS:** `AWS_REGION`, `AWS_SHARED_CREDENTIALS_FILE`, `SQS_*` e `SQS_SENDER_PROVIDER_MAP`.
- **Toggles:** `*_ENABLED`.
- **Outbox:** `OUTBOX_*`.
- **Worker de referências:** `REFWORKER_*` e `REFERENCE_RETRY_*`.
- **Portas do compose:** `*_PORT`.
- **Senhas locais:** `KEYCLOAK_ADMIN_PASSWORD` e `GRAFANA_ADMIN_PASSWORD`.

Os segredos são só valores locais de exemplo. Diga explicitamente que deixar `SQS_CONSUMER_PROFILE` e
`SQS_PUBLISHER_PROFILE` vazios faz o SDK usar a credencial padrão; no MiniStack isso é o root da conta dona, o que
anula o menor privilégio.

- [ ] **Step 2: Escrever o README**

Substitua o conteúdo de `README.md` pelas seções abaixo, nesta ordem. No fim, copie byte a byte a seção
`# Nota do desenvolvedor` atual: confira com `git show HEAD:README.md` e com `diff` da seção antes do commit.

1. **Visão geral:** o que o serviço faz, links para `docs/CHALLENGE.md`, `ARCHITECTURE.md` e a spec.
2. **Pré-requisitos:** Docker com Compose v2, Go 1.25.11+ e `curl` e `python3` (só para os scripts).
3. **Subir tudo:**
   - `docker compose up --build`, com o que sobe e as portas (nginx 8000, Keycloak 8080, MiniStack 4566, Prometheus
     9090, Grafana 3000 com acesso anônimo de leitura, admin/admin);
   - como ver as 3 réplicas;
   - `scripts/smoke.sh`;
   - `docker compose stop` (e o aviso de que `down -v` apaga dados).
4. **Variáveis de ambiente:** remete ao `.env.example` e lista as principais.
5. **Filas (MiniStack):**
   - o que `ministack_init`/`deploy/ministack/provision.sh` cria;
   - filas e DLQs, `maxReceiveCount=5`, visibility 30s;
   - a queue policy das contas `111111111111` e `222222222222`;
   - os usuários IAM e o arquivo `.local/ministack/credentials`;
   - que o MiniStack guarda tudo em memória e que reprovisionar rotaciona as chaves IAM, então os wallets devem ser
     reiniciados;
   - que o script exige bash ≥ 4.4 (roda no container).
6. **Migrations:**
   - `docker compose run --rm migrate`, que aplica com `up`;
   - para reverter, sobrescreva o comando:
     `docker compose run --rm migrate -path=/migrations -database='postgres://wallet_owner:wallet_owner@postgres:5432/wallet?sslmode=disable' down 1`;
   - o init de roles roda só com volume vazio.
7. **Autenticação e exemplos:**
   - clientes do realm, scopes e `provider_id`;
   - `scripts/token.sh`;
   - um `curl` por endpoint pelo nginx: abrir carteira, consultar, ledger, reconciliação, enviar operação com
     `Idempotency-Key`, consultar por id e pela rota do provedor;
   - envio por SQS com `scripts/sqs-send.sh` e o envelope do enunciado;
   - o contrato `MessageGroupId = walletId` e `MessageDeduplicationId = messageId`.
8. **Testes:**
   - os comandos `go test ./...`, `go test -race ./...`, `-tags=unit`, `-tags=integration`, `-tags=e2e` e
     `go vet ./...`;
   - a infra de teste: `docker compose --profile test up -d --wait postgres_test keycloak_test ministack_test`,
     `run --rm migrate_test` e `run --rm ministack_test_init`;
   - o que cada camada cobre;
   - os cenários E2E 1–8 e onde estão;
   - que `faultinject` é interna: o E2E compila o binário com ela e os valores de `FAULT` são esses.
9. **Multi-instância e falhas:**
   - 3 réplicas atrás do nginx, e os toggles para processos dedicados;
   - o `stop_grace_period` de 90s e o motivo;
   - como simular falhas manualmente (E2E ou binário com `-tags faultinject` e `FAULT=...`).
10. **Estrutura do repositório**, em resumo.
11. `# Nota do desenvolvedor`: a seção atual, intacta.

- [ ] **Step 3: Escrever o ARCHITECTURE.md**

Seções obrigatórias (enunciado §4 e §15; spec §17), cada uma com a decisão e o porquê, em prosa curta e tabelas:

1. **Visão e pacotes:** domínio puro, `internal/app` com portas, adapters e platform, e as regras de dependência.
2. **Dinheiro:**
   - `int64` em unidades menores e parsing canônico `^(0|[1-9]\d*)\.\d{2}$`;
   - tabela ISO 4217 com expoente 2 e checagem de overflow;
   - nunca float;
   - o mapeamento de `Money` no banco (`*_minor bigint` + `currency char(3)`).
3. **Persistência e transações:**
   - GORM com SQL explícito nos caminhos críticos;
   - `TxManager` com a transação no contexto, e nested que entra na externa;
   - `lock_timeout` e `statement_timeout` locais;
   - snapshot REPEATABLE READ READ ONLY na reconciliação;
   - os papéis `wallet_owner`/`wallet_app`, o ledger append-only com triggers e as constraints que garantem as
     invariantes.
4. **Concorrência e locks:** o lock pessimista da carteira com a guarda de versão e a ordem de locks (linha da
   transação → carteira → leitura de referências). Erros transitórios mapeados para 503 ou retry.
5. **Idempotência:**
   - o hash canônico e seus campos, sem normalização;
   - a chave escopada por provedor;
   - replay com o saldo original;
   - os dois 409;
   - a corrida resolvida por `ON CONFLICT`;
   - o cruzamento HTTP↔SQS.
6. **Operações, referências pendentes e reversões:**
   - a tabela dos 5 tipos e os índices de reversão única;
   - o worker (`FOR NO KEY UPDATE SKIP LOCKED`, uma pendência por transação, backoff 2s ×2 com teto de 5min e 10
     tentativas, sendo a primeira tentativa síncrona a número 1);
   - expiração em `REFERENCE_NOT_FOUND` ou `REFERENCE_NOT_PROCESSED`;
   - o bloqueio na cabeça da fila quando a pendência mais antiga só tem erro transitório.
7. **Inbox e consumidor SQS:**
   - a inbox na mesma transação via `alongside`;
   - a inbox escopada por provedor (`consumer_name = wager-transactions/<provider>`) — interpretação adotada;
   - a tabela de disposições;
   - a DLQ com `failureReason` e `senderId`, e dedup id = id SQS;
   - backoff 2s→5min por `ApproximateReceiveCount` e os ~60s de tolerância a queda do Postgres antes do redrive;
   - a ordem por carteira dentro do lote;
   - o shutdown (20s, abort e liberação de visibilidade).
8. **Outbox e eventos:**
   - claim com lease, relógio do banco, fencing por `locked_by` e republicação com o mesmo `eventId`;
   - os contratos dos 4 eventos;
   - a ordem por carteira não garantida com vários publishers (o consumidor usa `walletVersion`);
   - a dedup do SQS só vale por 5 minutos, então o consumidor deve deduplicar por `eventId`;
   - `outbox_lag_seconds` só conta publicações bem-sucedidas.
9. **Autenticação e autorização:**
   - o Keycloak e o go-oidc (RS256, `iss`, `aud=wallet-api`, `exp`, JWKS com timeout);
   - a matriz de scopes;
   - outro provedor recebe 404;
   - a identidade SQS por `SenderId` e o mapa;
   - uma queda do IdP aparece como 401.
10. **Fx e ciclo de vida:**
    - os módulos;
    - os toggles;
    - a ordem de parada (HTTP primeiro, depois consumidor e workers, depois o pool);
    - `StopTimeout` = 10s + soma dos prazos (77s com tudo ligado) e o `stop_grace_period` de 90s;
    - o readiness drenando.
11. **Observabilidade:** os campos dos logs, as métricas (nome, labels, significado), o dashboard e o
    readiness/liveness.
12. **Failure codes:** a tabela completa (código, categoria, quando ocorre, status HTTP ou disposição SQS), tirada de
    `internal/domain/wagering/failure.go`.
13. **Máquina de estados** da `WagerTransaction` e os contratos de eventos.
14. **Interpretações adotadas:**
    - os itens da memória do projeto (primeira tentativa conta como 1; WIN com referência ausente é REJECTED; WIN
      sobre BET revertida é aceito; `MaxFieldLength` em bytes; códigos extras `INVALID_FIELD` e
      `REFERENCE_NOT_ALLOWED`);
    - os conflitos pelo SQS vão para a DLQ;
    - a inbox por provedor;
    - o aceite síncrono, por isso o item 8b do enunciado não se aplica.
15. **Limitações e trabalho não concluído:**
    - MiniStack: não verifica assinatura; `SenderId` é a conta; IAM cross-account só com root; estado em memória;
    - a ordem da outbox;
    - um cancelamento no meio do receive pode deixar uma mensagem invisível por 30s;
    - os itens adiados das revisões que sobrarem;
    - sem testes de carga nem tracing.

- [ ] **Step 4: Conferir**

Run:

```bash
diff <(git show HEAD:README.md | sed -n '/^# Nota do desenvolvedor/,$p') <(sed -n '/^# Nota do desenvolvedor/,$p' README.md) && echo nota-intacta
grep -c . ARCHITECTURE.md README.md .env.example
for v in $(grep -oE 'getenv\("[A-Z_]+"|envOr\(getenv, "[A-Z_]+"|"[A-Z_]+_(ENABLED|TIMEOUT|DELAY|ATTEMPTS|INTERVAL|SIZE|LEASE|QUEUE|DLQ|PROFILE|MAP|WORKERS|TIME|MESSAGES)"' internal/platform/config/config.go | grep -oE '[A-Z_]{3,}' | sort -u); do grep -q "$v" .env.example || echo "missing in .env.example: $v"; done
```

Expected: `nota-intacta` e nenhuma linha `missing in .env.example`.

- [ ] **Step 5: Commit**

```bash
git add README.md ARCHITECTURE.md .env.example
git commit -m "docs: README, ARCHITECTURE and .env.example for a reproducible checkout" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
