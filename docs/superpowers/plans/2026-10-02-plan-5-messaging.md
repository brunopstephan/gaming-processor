# Plano 5 — Mensageria: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Receber operações pelo SQS (`wager-transactions.fifo`) com o mesmo caso de uso e a mesma idempotência do
HTTP, mais a inbox. Publicar os eventos da outbox em `wallet-events.fifo` só depois do commit. Retomar operações
`PENDING_REFERENCE` com um worker. Tudo roda em várias instâncias ao mesmo tempo, sem lock global. O plano também
traz o MiniStack provisionado no compose, as DLQs, as métricas de mensageria, os toggles por componente e o prazo de
desligamento do Fx dimensionado para todos eles.

**Architecture:**
- **`internal/app`:** ganha três casos de uso, sem dependência de AWS:
  - `IntakeService`: a inbox mais `WageringService.Process`, com o registro da inbox no `alongside`, na mesma
    transação SQL.
  - `OutboxRelay`: claim com lease, publicação por uma porta `EventPublisher` e marcação.
  - `ReferenceService`: claim de uma pendência vencida por transação, reaproveitando o `apply` do Process.
- **Adapters:**
  - `awssqs`: clientes, resolução das filas, readiness e publisher.
  - `sqsconsumer`: parsing estrito do envelope, decisão delete/DLQ/retry e workers com shutdown.
  - `workers`: módulos Fx do relay e do worker de referências.
  - `jsonstrict`: decodificação estrita compartilhada com o HTTP.
- **Plataforma:** `internal/platform/background` (loop periódico com parada controlada).
- **Composição:** monta os módulos conforme os toggles.
- **Isolamento dos testes:** filas próprias por teste. Os testes de claim global (outbox e worker) usam um banco
  migrado próprio por teste.

**Tech Stack:** Go 1.25.11, AWS SDK Go v2 (`aws-sdk-go-v2` v1.47.1, `config` v1.33.6, `service/sqs` v1.52.1),
MiniStack `ministackorg/ministack:1.5.20`, `amazon/aws-cli:2.37.8` (só para o provisionamento no compose), GORM,
Uber Fx, Prometheus.

**Spec:** `docs/superpowers/specs/2026-10-01-wallet-service-design.md`, seções 5 (referências e worker), 7, 8, 9 (SQS:
identidade do broker), 11 (consumidor e inbox), 12 (outbox), 13 (Fx e toggles), 14 (métricas) e 15 (compose).
Enunciado: `docs/CHALLENGE.md`, seções 6.5, 10 e 11.

## Roadmap

1. ~~Domínio~~ 2. ~~Persistência~~ 3. ~~Casos de uso~~ 4. ~~HTTP e auth~~ 5. **Mensageria** (este)
6. **Entrega:** Dockerfile, app no compose (nginx e 3 réplicas), Prometheus/Grafana, E2E multi-processo com
   `faultinject`, README, ARCHITECTURE e `.env.example`.

## Global Constraints

- Módulo `github.com/brunopstephan/backend-challenge-go`, diretiva `go 1.25.11`. `internal/domain/**` não muda.
  `internal/app` não importa AWS, Gin, go-oidc, Prometheus, GORM nem adapters.
- **Ordem do commit no SQS:** `DeleteMessage` só depois do commit da transação que gravou inbox, domínio, ledger e
  outbox. O envio para a DLQ acontece antes do delete. Se o envio falhar, a mensagem não é apagada.
- **Ordem do commit na outbox:** nenhum evento é publicado antes do commit que o gravou. O relay lê só linhas
  commitadas e nunca descarta um evento.
- **Contrato das filas:**
  - Entrada: `MessageGroupId = walletId`, `MessageDeduplicationId = messageId`.
  - Eventos: `MessageGroupId = aggregateId` (walletId), `MessageDeduplicationId = eventId`.
  - DLQ de entrada: mantém o `MessageGroupId` original. O `MessageDeduplicationId` é o id SQS da mensagem. O motivo vai
    no atributo `failureReason`.
- **Disposição por resultado no consumidor:**

  | Resultado | Ação |
  | --- | --- |
  | PROCESSED, REJECTED ou PENDING_REFERENCE commitado, inclusive replay | delete |
  | Inbox já tem o messageId com o mesmo hash | delete + `inbox_duplicates_total` |
  | Entrada inválida (código do `InputError`), `PROVIDER_IDENTITY_MISMATCH`, `MESSAGE_ID_REUSED`, `IDEMPOTENCY_KEY_CONFLICT`, `EXTERNAL_TRANSACTION_CONFLICT` | DLQ + delete + `sqs_dlq_total{reason}` |
  | FAILED (falha permanente de infra, já gravada) | DLQ `INFRASTRUCTURE_FAILURE` + delete |
  | Transitório ou erro não classificado | sem delete; `ChangeMessageVisibility` = base×2^(n−1) (teto configurável) por `ApproximateReceiveCount`; `sqs_retries_total`. Esgotado → redrive (`maxReceiveCount=5`) |
- **Identidade:** o `providerId` vem do atributo `SenderId`, pelo mapa `SQS_SENDER_PROVIDER_MAP`. Remetente fora do
  mapa ou `data.providerId` diferente → DLQ `PROVIDER_IDENTITY_MISMATCH`, sem efeito financeiro.
- **Shutdown do consumidor:** para o polling e espera o trabalho em andamento até `SQS_SHUTDOWN_TIMEOUT` (20s). Se
  estourar, cancela o contexto (rollback) e libera a visibilidade (`ChangeMessageVisibility(0)`). Mensagens já
  recebidas e ainda não iniciadas também são liberadas.
- **FAILED** só para erro permanente. Erro transitório nunca vira FAILED.
- **Métricas** (spec §14): `inbox_duplicates_total`, `sqs_retries_total`, `sqs_dlq_total{reason}`,
  `outbox_lag_seconds`, `outbox_publish_attempts_total{result}` (`published` ou `failed`) e
  `reference_retries_total`, além das já existentes.
- **Toggles:** `HTTP_ENABLED`, `CONSUMER_ENABLED`, `OUTBOX_ENABLED` e `REFWORKER_ENABLED`, todos com padrão `true`.
- **Prazo de desligamento:** `fx.StopTimeout` = 10s + soma dos prazos de drenagem dos componentes ligados.
- **Tags de build:** unit `//go:build !integration && !e2e`; integration `//go:build !unit && !e2e`.
- **Infra de teste:**

  ```bash
  docker compose --profile test up -d --wait postgres_test keycloak_test ministack_test \
    && docker compose --profile test run --rm migrate_test \
    && docker compose --profile test run --rm ministack_test_init
  ```

  Os testes falham com a instrução acima quando a infra não responde.
- Testes só com `testing`, nunca `t.Fatal` fora da goroutine do teste. `gofmt` e `go vet ./...` limpos.
- Toda mensagem de commit termina exatamente com `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. **Queda entre o commit e o `DeleteMessage`:** a reentrega vira duplicata, é apagada e não movimenta saldo de novo.
   Teste na Task 5 (`TestRedeliveryAfterLostDeleteIsDuplicate`).
2. **Mesmo `messageId` com outro payload:** vai para a DLQ `MESSAGE_ID_REUSED`, sem efeito. Testes nas Tasks 4 e 5.
3. **Provedor que se passa por outro** (`data.providerId` diferente do remetente): DLQ, sem linha nem movimento. Teste
   na Task 5.
4. **Relay que cai entre publicar e marcar:** quando o lease expira, outra instância publica de novo com o mesmo
   `eventId`. A marca da instância antiga não sobrescreve. Teste na Task 7.
5. **Shutdown com mensagem em processamento:** rollback e visibilidade liberada na hora, sem commit parcial. Teste na
   Task 6.

## File Structure

```
docker-compose.yml                                   (modify) + ministack, ministack_init, ministack_test, ministack_test_init
deploy/ministack/provision.sh                        filas, DLQs, redrive, queue policy, usuários IAM, arquivo de credenciais
deploy/ministack/*.json                              atributos das filas e políticas
.gitignore                                           (modify) + .local/
internal/testsupport/sqstest/sqstest.go              clientes, filas por teste, send/receive
internal/testsupport/pgtest/pgtest.go                (modify) + FreshDatabase
internal/testsupport/apptest/apptest.go              (modify) + Inbox, NewIsolated, métricas novas
internal/platform/config/config.go                   (modify) + SQS, Toggles, Outbox, RefWorker
internal/platform/metrics/metrics.go                 (modify) + métricas de mensageria
internal/platform/background/background.go           loop periódico + registro no Fx
internal/app/metrics.go, ports.go                    (modify) + métricas, inbox, relay, publisher, claim
internal/app/intake_service.go                       inbox + Process
internal/app/outbox_relay.go                         claim/publish/mark
internal/app/reference_service.go                    worker de referências
internal/app/wagering_service.go                     (modify) transientFailure extraído
internal/adapters/postgres/inbox_repository.go       inbox
internal/adapters/postgres/outbox_repository.go      (modify) Claim, MarkPublished, Reschedule
internal/adapters/postgres/transaction_repository.go (modify) ClaimDuePending, GetForUpdate
internal/adapters/jsonstrict/jsonstrict.go           decodificação estrita (HTTP e SQS)
internal/adapters/awssqs/{awssqs.go,publisher.go}    clientes, filas, readiness, publisher
internal/adapters/sqsconsumer/{message.go,consumer.go,runner.go,module.go}
internal/adapters/workers/workers.go                 OutboxModule, ReferenceModule
internal/platform/composition/composition.go         (modify) Modules(cfg), StopTimeout(cfg)
cmd/wallet/main.go                                   (modify)
```

---

### Task 1: MiniStack no compose, provisionamento e suporte de teste

**Files:**
- Modify: `docker-compose.yml`, `.gitignore`, `go.mod`, `go.sum`
- Create: `deploy/ministack/provision.sh`, `deploy/ministack/dlq.json`, `deploy/ministack/wager-transactions.json`, `deploy/ministack/wallet-events.json`, `deploy/ministack/wager-transactions-policy.json`, `deploy/ministack/wallet-consumer-policy.json`, `deploy/ministack/wallet-publisher-policy.json`, `internal/testsupport/sqstest/sqstest.go`
- Test: `internal/testsupport/sqstest/provision_test.go` (integration)

**Interfaces:**
- Produces:
  - Serviços `ministack` (`${MINISTACK_PORT:-4566}`), `ministack_init`, `ministack_test` (profile `test`,
    `${MINISTACK_TEST_PORT:-4567}`) e `ministack_test_init`.
  - Arquivos de credenciais `./.local/ministack/credentials` e `./.local/ministack-test/credentials`. Eles têm os
    profiles `wallet-consumer`, `wallet-publisher`, `provider-a` e `provider-b`.
  - Helpers de teste:
    - `sqstest.Endpoint()`, `Region`, `OwnerAccount`, `ProviderAAccount`, `ProviderBAccount`;
    - `Client(t, accessKey)`, `Owner(t)`, `ProfileClient(t, profile)` e `CredentialsFile()`;
    - `NewQueues(t, Options) Queues`, com `Queues{Input, InputDLQ, Events, EventsDLQ Queue}` e
      `Queue{Name, URL string}`;
    - `Send(t, c, url, body, group) string`, `Receive(t, c, url, wait) []types.Message` e `Delete(t, c, url, msg)`;
    - `FailureReason(msg) string`.

- [ ] **Step 1: Adicionar o SDK**

```bash
go get github.com/aws/aws-sdk-go-v2@v1.47.1 github.com/aws/aws-sdk-go-v2/config@v1.33.6 github.com/aws/aws-sdk-go-v2/service/sqs@v1.52.1 github.com/aws/aws-sdk-go-v2/credentials
```

- [ ] **Step 2: Escrever os atributos e as políticas**

`deploy/ministack/dlq.json`:

```json
{ "FifoQueue": "true", "ContentBasedDeduplication": "false", "MessageRetentionPeriod": "1209600" }
```

`deploy/ministack/wager-transactions.json`:

```json
{
  "FifoQueue": "true",
  "ContentBasedDeduplication": "false",
  "VisibilityTimeout": "30",
  "RedrivePolicy": "{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:000000000000:wager-transactions-dlq.fifo\",\"maxReceiveCount\":\"5\"}"
}
```

`deploy/ministack/wallet-events.json`:

```json
{
  "FifoQueue": "true",
  "ContentBasedDeduplication": "false",
  "VisibilityTimeout": "30",
  "RedrivePolicy": "{\"deadLetterTargetArn\":\"arn:aws:sqs:us-east-1:000000000000:wallet-events-dlq.fifo\",\"maxReceiveCount\":\"5\"}"
}
```

`deploy/ministack/wager-transactions-policy.json` (só as contas dos provedores podem enviar):

```json
{
  "Policy": "{\"Version\":\"2012-10-17\",\"Statement\":[{\"Sid\":\"ProvidersSend\",\"Effect\":\"Allow\",\"Principal\":{\"AWS\":[\"arn:aws:iam::111111111111:root\",\"arn:aws:iam::222222222222:root\"]},\"Action\":\"sqs:SendMessage\",\"Resource\":\"arn:aws:sqs:us-east-1:000000000000:wager-transactions.fifo\"}]}"
}
```

`deploy/ministack/wallet-consumer-policy.json`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    { "Effect": "Allow",
      "Action": ["sqs:ReceiveMessage", "sqs:DeleteMessage", "sqs:ChangeMessageVisibility", "sqs:GetQueueAttributes", "sqs:GetQueueUrl"],
      "Resource": "arn:aws:sqs:us-east-1:000000000000:wager-transactions.fifo" },
    { "Effect": "Allow",
      "Action": ["sqs:SendMessage", "sqs:GetQueueAttributes", "sqs:GetQueueUrl"],
      "Resource": "arn:aws:sqs:us-east-1:000000000000:wager-transactions-dlq.fifo" }
  ]
}
```

`deploy/ministack/wallet-publisher-policy.json`:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    { "Effect": "Allow",
      "Action": ["sqs:SendMessage", "sqs:GetQueueAttributes", "sqs:GetQueueUrl"],
      "Resource": "arn:aws:sqs:us-east-1:000000000000:wallet-events.fifo" }
  ]
}
```

- [ ] **Step 3: Escrever o script de provisionamento**

`deploy/ministack/provision.sh`:

```bash
#!/usr/bin/env bash
# Provisions MiniStack for the wallet service: FIFO queues with redrive to
# their DLQs, the input queue policy (only the provider accounts may send),
# and least-privilege IAM users for the consumer and the publisher. IAM access
# keys are generated by MiniStack, so they are written to CREDENTIALS_OUT as
# an AWS shared credentials file (profiles wallet-consumer, wallet-publisher,
# provider-a and provider-b). Safe to re-run.
set -euo pipefail
: "${MINISTACK_ENDPOINT:?}" "${CREDENTIALS_OUT:?}"
export AWS_DEFAULT_REGION=us-east-1 AWS_PAGER="" AWS_ACCESS_KEY_ID=test AWS_SECRET_ACCESS_KEY=test
awsm() { aws --endpoint-url "$MINISTACK_ENDPOINT" "$@"; }
cd "$(dirname "$0")"

awsm sqs create-queue --queue-name wager-transactions-dlq.fifo --attributes file://dlq.json >/dev/null
awsm sqs create-queue --queue-name wallet-events-dlq.fifo --attributes file://dlq.json >/dev/null
awsm sqs create-queue --queue-name wager-transactions.fifo --attributes file://wager-transactions.json >/dev/null
awsm sqs create-queue --queue-name wallet-events.fifo --attributes file://wallet-events.json >/dev/null
input_url=$(awsm sqs get-queue-url --queue-name wager-transactions.fifo --query QueueUrl --output text)
awsm sqs set-queue-attributes --queue-url "$input_url" --attributes file://wager-transactions-policy.json

# ensure_user NAME: creates the user if needed, (re)applies its policy, rotates
# its access key and prints "KEY SECRET".
ensure_user() {
  local name=$1
  awsm iam get-user --user-name "$name" >/dev/null 2>&1 || awsm iam create-user --user-name "$name" >/dev/null
  awsm iam put-user-policy --user-name "$name" --policy-name "$name" --policy-document "file://$name-policy.json"
  for key in $(awsm iam list-access-keys --user-name "$name" --query 'AccessKeyMetadata[].AccessKeyId' --output text); do
    awsm iam delete-access-key --user-name "$name" --access-key-id "$key"
  done
  awsm iam create-access-key --user-name "$name" --query 'AccessKey.[AccessKeyId,SecretAccessKey]' --output text
}

read -r consumer_key consumer_secret <<<"$(ensure_user wallet-consumer)"
read -r publisher_key publisher_secret <<<"$(ensure_user wallet-publisher)"

mkdir -p "$(dirname "$CREDENTIALS_OUT")"
tmp="$CREDENTIALS_OUT.tmp"
cat >"$tmp" <<EOF
[wallet-consumer]
aws_access_key_id = $consumer_key
aws_secret_access_key = $consumer_secret

[wallet-publisher]
aws_access_key_id = $publisher_key
aws_secret_access_key = $publisher_secret

# Providers use the root credential of their own account (MiniStack denies
# cross-account sends by IAM users; see ARCHITECTURE.md).
[provider-a]
aws_access_key_id = 111111111111
aws_secret_access_key = provider-a-secret

[provider-b]
aws_access_key_id = 222222222222
aws_secret_access_key = provider-b-secret
EOF
chmod 644 "$tmp"
mv "$tmp" "$CREDENTIALS_OUT"
echo "ministack provisioned; credentials at $CREDENTIALS_OUT"
```

Torne executável: `chmod +x deploy/ministack/provision.sh`.

- [ ] **Step 4: Acrescentar ao compose e ao `.gitignore`**

Em `.gitignore`, acrescentar a linha `.local/`. Em `docker-compose.yml`, acrescentar os serviços abaixo:

```yaml
  ministack:
    image: ministackorg/ministack:1.5.20
    environment:
      AUTH: "true"
    ports:
      - "${MINISTACK_PORT:-4566}:4566"
    healthcheck: &ministack-healthcheck
      test: ["CMD-SHELL", "python3 -c \"import urllib.request; urllib.request.urlopen('http://127.0.0.1:4566/_ministack/health', timeout=3)\""]
      interval: 3s
      timeout: 5s
      retries: 40

  ministack_init:
    image: amazon/aws-cli:2.37.8
    depends_on:
      ministack:
        condition: service_healthy
    entrypoint: ["bash", "/provision/provision.sh"]
    environment:
      MINISTACK_ENDPOINT: http://ministack:4566
      CREDENTIALS_OUT: /out/credentials
    volumes:
      - ./deploy/ministack:/provision:ro
      - ./.local/ministack:/out

  ministack_test:
    image: ministackorg/ministack:1.5.20
    profiles: ["test"]
    environment:
      AUTH: "true"
    ports:
      - "${MINISTACK_TEST_PORT:-4567}:4566"
    healthcheck: *ministack-healthcheck

  ministack_test_init:
    image: amazon/aws-cli:2.37.8
    profiles: ["test"]
    depends_on:
      ministack_test:
        condition: service_healthy
    entrypoint: ["bash", "/provision/provision.sh"]
    environment:
      MINISTACK_ENDPOINT: http://ministack_test:4566
      CREDENTIALS_OUT: /out/credentials
    volumes:
      - ./deploy/ministack:/provision:ro
      - ./.local/ministack-test:/out
```

Se a imagem do MiniStack não tiver `python3`, troque o healthcheck por uma ferramenta que exista nela (`curl` ou
`wget`) e registre a troca no relatório. Se o MiniStack não aceitar o formato do `Principal` da queue policy, ajuste o
JSON até `provider-a` poder enviar e a conta `333333333333` levar `AccessDenied`, e registre a troca.

- [ ] **Step 5: Criar o suporte de teste**

`internal/testsupport/sqstest/sqstest.go`:

```go
// Package sqstest talks to the ministack_test container
// (`docker compose --profile test up -d --wait ministack_test`): per-test
// FIFO queues with redrive, clients for the owner and provider accounts, and
// send/receive helpers.
package sqstest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
)

// Region and the accounts provisioned in MiniStack. A 12-digit access key is
// the root credential of that account; "test" is the owner account's root.
const (
	Region           = "us-east-1"
	OwnerAccount     = "000000000000"
	ProviderAAccount = "111111111111"
	ProviderBAccount = "222222222222"
)

const infraHint = "Suba a infra: `docker compose --profile test up -d --wait ministack_test && " +
	"docker compose --profile test run --rm ministack_test_init`"

// Endpoint is the test MiniStack (env MINISTACK_TEST_URL).
func Endpoint() string {
	if v := os.Getenv("MINISTACK_TEST_URL"); v != "" {
		return v
	}
	return "http://localhost:4567"
}

// Client returns an SQS client authenticated as accessKey.
func Client(t testing.TB, accessKey string) *sqs.Client {
	t.Helper()
	return sqs.New(sqs.Options{
		Region:       Region,
		BaseEndpoint: aws.String(Endpoint()),
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, accessKey+"-secret", ""),
	})
}

// Owner returns a client with the queue owner's root credential.
func Owner(t testing.TB) *sqs.Client { return Client(t, "test") }

// CredentialsFile is the shared credentials file written by
// ministack_test_init (env MINISTACK_TEST_CREDENTIALS).
func CredentialsFile() string {
	if v := os.Getenv("MINISTACK_TEST_CREDENTIALS"); v != "" {
		return v
	}
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", ".local", "ministack-test", "credentials")
}

// ProfileClient returns a client for a profile of CredentialsFile.
func ProfileClient(t testing.TB, profile string) *sqs.Client {
	t.Helper()
	if _, err := os.Stat(CredentialsFile()); err != nil {
		t.Fatalf("credenciais do ministack_test ausentes em %s (%v). %s", CredentialsFile(), err, infraHint)
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(Region),
		awsconfig.WithSharedCredentialsFiles([]string{CredentialsFile()}),
		awsconfig.WithSharedConfigProfile(profile),
	)
	if err != nil {
		t.Fatalf("profile %s: %v", profile, err)
	}
	return sqs.NewFromConfig(cfg, func(o *sqs.Options) { o.BaseEndpoint = aws.String(Endpoint()) })
}

// Queue is a queue created for one test.
type Queue struct {
	Name string
	URL  string
}

// Queues mirrors the provisioned layout: input and events queues with their DLQs.
type Queues struct {
	Input, InputDLQ, Events, EventsDLQ Queue
}

// Options tune the per-test queues; zero values mean 30s and 5.
type Options struct {
	VisibilityTimeout int
	MaxReceiveCount   int
}

// NewQueues creates uniquely named FIFO queues with redrive and the provider
// send policy on the input queue, deleted at cleanup.
func NewQueues(t testing.TB, opts Options) Queues {
	t.Helper()
	if opts.VisibilityTimeout == 0 {
		opts.VisibilityTimeout = 30
	}
	if opts.MaxReceiveCount == 0 {
		opts.MaxReceiveCount = 5
	}
	c := Owner(t)
	prefix := "t" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	inDLQ := create(t, c, prefix+"-in-dlq.fifo", nil)
	evDLQ := create(t, c, prefix+"-ev-dlq.fifo", nil)
	in := create(t, c, prefix+"-in.fifo", map[string]string{
		"VisibilityTimeout": fmt.Sprint(opts.VisibilityTimeout),
		"RedrivePolicy":     redrive(t, c, inDLQ, opts.MaxReceiveCount),
	})
	ev := create(t, c, prefix+"-ev.fifo", map[string]string{
		"VisibilityTimeout": fmt.Sprint(opts.VisibilityTimeout),
		"RedrivePolicy":     redrive(t, c, evDLQ, opts.MaxReceiveCount),
	})
	policy, _ := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect": "Allow",
			"Principal": map[string]any{"AWS": []string{
				"arn:aws:iam::" + ProviderAAccount + ":root", "arn:aws:iam::" + ProviderBAccount + ":root",
			}},
			"Action":   "sqs:SendMessage",
			"Resource": "arn:aws:sqs:" + Region + ":" + OwnerAccount + ":" + in.Name,
		}},
	})
	if _, err := c.SetQueueAttributes(context.Background(), &sqs.SetQueueAttributesInput{
		QueueUrl: aws.String(in.URL), Attributes: map[string]string{"Policy": string(policy)},
	}); err != nil {
		t.Fatalf("queue policy: %v", err)
	}
	return Queues{Input: in, InputDLQ: inDLQ, Events: ev, EventsDLQ: evDLQ}
}

func create(t testing.TB, c *sqs.Client, name string, attrs map[string]string) Queue {
	t.Helper()
	all := map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "false"}
	for k, v := range attrs {
		all[k] = v
	}
	out, err := c.CreateQueue(context.Background(), &sqs.CreateQueueInput{QueueName: aws.String(name), Attributes: all})
	if err != nil {
		t.Fatalf("ministack de teste indisponível (%v). %s", err, infraHint)
	}
	url := aws.ToString(out.QueueUrl)
	t.Cleanup(func() { _, _ = c.DeleteQueue(context.Background(), &sqs.DeleteQueueInput{QueueUrl: aws.String(url)}) })
	return Queue{Name: name, URL: url}
}

func redrive(t testing.TB, c *sqs.Client, dlq Queue, maxReceive int) string {
	t.Helper()
	out, err := c.GetQueueAttributes(context.Background(), &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(dlq.URL), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("dlq arn: %v", err)
	}
	policy, _ := json.Marshal(map[string]string{
		"deadLetterTargetArn": out.Attributes[string(types.QueueAttributeNameQueueArn)],
		"maxReceiveCount":     fmt.Sprint(maxReceive),
	})
	return string(policy)
}

// Send sends body to a FIFO queue in group and returns the deduplication id used.
func Send(t testing.TB, c *sqs.Client, queueURL, body, group string) string {
	t.Helper()
	dedup := uuid.NewString()
	if _, err := c.SendMessage(context.Background(), &sqs.SendMessageInput{
		QueueUrl: aws.String(queueURL), MessageBody: aws.String(body),
		MessageGroupId: aws.String(group), MessageDeduplicationId: aws.String(dedup),
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	return dedup
}

// Receive returns the first non-empty batch (up to 10 messages, with every
// system and message attribute) received within wait, or nil.
func Receive(t testing.TB, c *sqs.Client, queueURL string, wait time.Duration) []types.Message {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		out, err := c.ReceiveMessage(context.Background(), &sqs.ReceiveMessageInput{
			QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 10, WaitTimeSeconds: 1,
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameAll},
			MessageAttributeNames:       []string{"All"},
		})
		if err != nil {
			t.Fatalf("receive: %v", err)
		}
		if len(out.Messages) > 0 || time.Now().After(deadline) {
			return out.Messages
		}
	}
}

// Delete removes msg from the queue.
func Delete(t testing.TB, c *sqs.Client, queueURL string, msg types.Message) {
	t.Helper()
	if _, err := c.DeleteMessage(context.Background(), &sqs.DeleteMessageInput{
		QueueUrl: aws.String(queueURL), ReceiptHandle: msg.ReceiptHandle,
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// FailureReason returns the failureReason message attribute ("" if absent).
func FailureReason(msg types.Message) string {
	if v, ok := msg.MessageAttributes["failureReason"]; ok {
		return aws.ToString(v.StringValue)
	}
	return ""
}
```

- [ ] **Step 6: Escrever o teste do provisionamento**

`internal/testsupport/sqstest/provision_test.go`:

```go
//go:build !unit && !e2e

package sqstest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
)

func queueURL(t *testing.T, c *sqs.Client, name string) string {
	t.Helper()
	out, err := c.GetQueueUrl(context.Background(), &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		t.Fatalf("queue %s not provisioned (%v). %s", name, err, infraHint)
	}
	return aws.ToString(out.QueueUrl)
}

func requireDenied(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("%s: error = %v, want AccessDenied", what, err)
	}
}

func TestProvisionedQueues(t *testing.T) {
	owner := Owner(t)
	for name, dlq := range map[string]string{
		"wager-transactions.fifo": "wager-transactions-dlq.fifo",
		"wallet-events.fifo":      "wallet-events-dlq.fifo",
	} {
		out, err := owner.GetQueueAttributes(context.Background(), &sqs.GetQueueAttributesInput{
			QueueUrl: aws.String(queueURL(t, owner, name)), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameAll},
		})
		if err != nil {
			t.Fatal(err)
		}
		var redrive map[string]any
		if err := json.Unmarshal([]byte(out.Attributes["RedrivePolicy"]), &redrive); err != nil {
			t.Fatalf("%s redrive %q: %v", name, out.Attributes["RedrivePolicy"], err)
		}
		if !strings.HasSuffix(fmt.Sprint(redrive["deadLetterTargetArn"]), ":"+dlq) || fmt.Sprint(redrive["maxReceiveCount"]) != "5" ||
			out.Attributes["VisibilityTimeout"] != "30" || out.Attributes["FifoQueue"] != "true" {
			t.Fatalf("%s attributes = %v", name, out.Attributes)
		}
	}
}

func TestProvisionedIdentities(t *testing.T) {
	ctx := context.Background()
	owner := Owner(t)
	input, inputDLQ, events := queueURL(t, owner, "wager-transactions.fifo"),
		queueURL(t, owner, "wager-transactions-dlq.fifo"), queueURL(t, owner, "wallet-events.fifo")
	send := func(c *sqs.Client, url, body string) error {
		_, err := c.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(url), MessageBody: aws.String(body),
			MessageGroupId: aws.String("probe"), MessageDeduplicationId: aws.String(uuid.NewString())})
		return err
	}

	probe := `{"probe":"` + uuid.NewString() + `"}`
	if err := send(ProfileClient(t, "provider-a"), input, probe); err != nil {
		t.Fatalf("provider-a must be allowed to send: %v", err)
	}
	requireDenied(t, send(Client(t, "333333333333"), input, probe), "unknown account send")

	consumer := ProfileClient(t, "wallet-consumer")
	found := false
	for deadline := time.Now().Add(10 * time.Second); !found && time.Now().Before(deadline); {
		for _, m := range Receive(t, consumer, input, 2*time.Second) {
			if aws.ToString(m.Body) == probe {
				found = true
				if m.Attributes["SenderId"] != ProviderAAccount {
					t.Fatalf("SenderId = %q, want %s", m.Attributes["SenderId"], ProviderAAccount)
				}
			}
			Delete(t, consumer, input, m)
		}
	}
	if !found {
		t.Fatal("wallet-consumer did not receive the provider message")
	}
	if err := send(consumer, inputDLQ, probe); err != nil {
		t.Fatalf("wallet-consumer must be allowed to dead-letter: %v", err)
	}
	requireDenied(t, send(consumer, events, probe), "consumer send to events")

	publisher := ProfileClient(t, "wallet-publisher")
	if err := send(publisher, events, probe); err != nil {
		t.Fatalf("wallet-publisher must be allowed to publish: %v", err)
	}
	_, err := publisher.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: aws.String(input), WaitTimeSeconds: 0})
	requireDenied(t, err, "publisher receive from input")

	for _, url := range []string{inputDLQ, events} { // keep the shared test queues small
		for _, m := range Receive(t, owner, url, time.Second) {
			Delete(t, owner, url, m)
		}
	}
}
```

- [ ] **Step 7: Subir e rodar**

Run:

```bash
docker compose config --quiet
docker compose --profile test up -d --wait ministack_test && docker compose --profile test run --rm ministack_test_init
go test -race -count=1 -tags=integration ./internal/testsupport/sqstest/...
```

Expected: `ok`, e o arquivo `.local/ministack-test/credentials` existe. Confira também o dev:

```bash
docker compose up -d --wait ministack && docker compose run --rm ministack_init
docker compose stop ministack
```

- [ ] **Step 8: Commit**

```bash
go mod tidy && gofmt -l . && go vet ./...
git add docker-compose.yml .gitignore deploy/ministack internal/testsupport/sqstest go.mod go.sum
git commit -m "chore(sqs): ministack dev/test with provisioned queues, policies and IAM identities" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Configuração de SQS, toggles e workers; adapter `awssqs`

**Files:**
- Modify: `internal/platform/config/config.go`, `internal/platform/config/config_test.go`
- Create: `internal/adapters/awssqs/awssqs.go`
- Test: `internal/adapters/awssqs/awssqs_test.go` (integration)

**Interfaces:**
- Consumes: `sqstest.*` (Task 1) e `health.Check`.
- Produces:
  - Tipos de configuração:
    - `config.SQS{Endpoint, Region, ConsumerProfile, PublisherProfile, InputQueue, InputDLQ, EventsQueue string;
      Workers int; WaitTime time.Duration; MaxMessages int; RetryBaseDelay, RetryMaxDelay, ShutdownTimeout
      time.Duration; SenderProviders map[string]string}`;
    - `config.Toggles{HTTP, Consumer, Outbox, RefWorker bool}`;
    - `config.Outbox{PollInterval time.Duration; BatchSize int; Lease, RetryBaseDelay, RetryMaxDelay,
      ShutdownTimeout time.Duration}`;
    - `config.RefWorker{PollInterval, ShutdownTimeout time.Duration}`.
  - Adapter `awssqs`:
    - `awssqs.Clients{Consumer, Publisher *sqs.Client}` e `NewClients(cfg config.Config) (*Clients, error)`;
    - `awssqs.Queues{Input, InputDLQ, Events string}` e `NewQueues(lc, cfg, *Clients) *Queues`, que resolve as URLs no
      `OnStart` (só as filas dos componentes ligados) e falha o start se o SQS não responder;
    - `awssqs.Module`, que fornece `*Clients`, `*Queues` e um `health.Check` "sqs" no grupo `readiness`.

**Variáveis** (padrão entre parênteses):

| Grupo | Variável | Padrão |
| --- | --- | --- |
| SQS | `SQS_ENDPOINT` | vazio = AWS |
| SQS | `AWS_REGION` | `us-east-1` |
| SQS | `SQS_CONSUMER_PROFILE` | vazio = cadeia padrão de credenciais |
| SQS | `SQS_PUBLISHER_PROFILE` | vazio = cadeia padrão de credenciais |
| SQS | `SQS_INPUT_QUEUE` | `wager-transactions.fifo` |
| SQS | `SQS_INPUT_DLQ` | `wager-transactions-dlq.fifo` |
| SQS | `SQS_EVENTS_QUEUE` | `wallet-events.fifo` |
| SQS | `SQS_CONSUMER_WORKERS` | 4 |
| SQS | `SQS_WAIT_TIME` | 20s, de 1s a 20s |
| SQS | `SQS_MAX_MESSAGES` | 10, de 1 a 10 |
| SQS | `SQS_RETRY_BASE_DELAY` | 2s |
| SQS | `SQS_RETRY_MAX_DELAY` | 5m, ≥ base, ≤ 12h |
| SQS | `SQS_SHUTDOWN_TIMEOUT` | 20s |
| SQS | `SQS_SENDER_PROVIDER_MAP` | `111111111111=provider-a,222222222222=provider-b` |
| Toggles | `HTTP_ENABLED`, `CONSUMER_ENABLED`, `OUTBOX_ENABLED`, `REFWORKER_ENABLED` | `true`; aceita `true/false/1/0` |
| Outbox | `OUTBOX_POLL_INTERVAL` | 500ms |
| Outbox | `OUTBOX_BATCH_SIZE` | 50 |
| Outbox | `OUTBOX_LEASE` | 30s |
| Outbox | `OUTBOX_RETRY_BASE_DELAY` | 1s |
| Outbox | `OUTBOX_RETRY_MAX_DELAY` | 5m, ≥ base |
| Outbox | `OUTBOX_SHUTDOWN_TIMEOUT` | 10s |
| Worker | `REFWORKER_POLL_INTERVAL` | 1s |
| Worker | `REFWORKER_SHUTDOWN_TIMEOUT` | 10s |

- [ ] **Step 1: Escrever os testes de configuração que falham**

Acrescentar a `internal/platform/config/config_test.go` (acrescente `reflect` aos imports):

```go
func TestLoadMessagingDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	wantSQS := SQS{
		Region: "us-east-1", InputQueue: "wager-transactions.fifo", InputDLQ: "wager-transactions-dlq.fifo",
		EventsQueue: "wallet-events.fifo", Workers: 4, WaitTime: 20 * time.Second, MaxMessages: 10,
		RetryBaseDelay: 2 * time.Second, RetryMaxDelay: 5 * time.Minute, ShutdownTimeout: 20 * time.Second,
		SenderProviders: map[string]string{"111111111111": "provider-a", "222222222222": "provider-b"},
	}
	if !reflect.DeepEqual(cfg.SQS, wantSQS) {
		t.Fatalf("SQS = %+v\nwant %+v", cfg.SQS, wantSQS)
	}
	if cfg.Toggles != (Toggles{HTTP: true, Consumer: true, Outbox: true, RefWorker: true}) {
		t.Fatalf("Toggles = %+v", cfg.Toggles)
	}
	wantOutbox := Outbox{PollInterval: 500 * time.Millisecond, BatchSize: 50, Lease: 30 * time.Second,
		RetryBaseDelay: time.Second, RetryMaxDelay: 5 * time.Minute, ShutdownTimeout: 10 * time.Second}
	if cfg.Outbox != wantOutbox || cfg.RefWorker != (RefWorker{PollInterval: time.Second, ShutdownTimeout: 10 * time.Second}) {
		t.Fatalf("Outbox %+v RefWorker %+v", cfg.Outbox, cfg.RefWorker)
	}
}

func TestLoadMessagingOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "SQS_ENDPOINT": "http://localhost:4566", "SQS_CONSUMER_PROFILE": "wallet-consumer",
		"SQS_SENDER_PROVIDER_MAP": " 333333333333 = provider-c ", "HTTP_ENABLED": "false", "CONSUMER_ENABLED": "0",
		"SQS_WAIT_TIME": "1s", "SQS_MAX_MESSAGES": "3",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SQS.Endpoint != "http://localhost:4566" || cfg.SQS.ConsumerProfile != "wallet-consumer" ||
		!reflect.DeepEqual(cfg.SQS.SenderProviders, map[string]string{"333333333333": "provider-c"}) ||
		cfg.Toggles.HTTP || cfg.Toggles.Consumer || !cfg.Toggles.Outbox || cfg.SQS.WaitTime != time.Second || cfg.SQS.MaxMessages != 3 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadMessagingInvalid(t *testing.T) {
	_, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "SQS_SENDER_PROVIDER_MAP": "broken", "OUTBOX_ENABLED": "maybe",
		"SQS_WAIT_TIME": "30s", "SQS_MAX_MESSAGES": "11", "SQS_RETRY_MAX_DELAY": "1s",
		"OUTBOX_RETRY_MAX_DELAY": "1ms",
	}))
	for _, key := range []string{"SQS_SENDER_PROVIDER_MAP", "OUTBOX_ENABLED", "SQS_WAIT_TIME", "SQS_MAX_MESSAGES", "SQS_RETRY_MAX_DELAY", "OUTBOX_RETRY_MAX_DELAY"} {
		if err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("error %v does not mention %s", err, key)
		}
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test -tags=unit ./internal/platform/config/...`
Expected: FAIL de compilação, `cfg.SQS undefined`.

- [ ] **Step 3: Implementar a configuração**

Em `config.go`, acrescentar ao `Config` os campos `SQS SQS`, `Toggles Toggles`, `Outbox Outbox` e
`RefWorker RefWorker`. Em `Load`, antes do `if len(errs) > 0`:

```go
	sqsCfg := SQS{
		Endpoint:         getenv("SQS_ENDPOINT"),
		Region:           envOr(getenv, "AWS_REGION", "us-east-1"),
		ConsumerProfile:  getenv("SQS_CONSUMER_PROFILE"),
		PublisherProfile: getenv("SQS_PUBLISHER_PROFILE"),
		InputQueue:       envOr(getenv, "SQS_INPUT_QUEUE", "wager-transactions.fifo"),
		InputDLQ:         envOr(getenv, "SQS_INPUT_DLQ", "wager-transactions-dlq.fifo"),
		EventsQueue:      envOr(getenv, "SQS_EVENTS_QUEUE", "wallet-events.fifo"),
		Workers:          positiveInt(getenv, "SQS_CONSUMER_WORKERS", 4, &errs),
		WaitTime:         positiveDuration(getenv, "SQS_WAIT_TIME", 20*time.Second, &errs),
		MaxMessages:      positiveInt(getenv, "SQS_MAX_MESSAGES", 10, &errs),
		RetryBaseDelay:   positiveDuration(getenv, "SQS_RETRY_BASE_DELAY", 2*time.Second, &errs),
		RetryMaxDelay:    positiveDuration(getenv, "SQS_RETRY_MAX_DELAY", 5*time.Minute, &errs),
		ShutdownTimeout:  positiveDuration(getenv, "SQS_SHUTDOWN_TIMEOUT", 20*time.Second, &errs),
		SenderProviders:  senderProviders(getenv, &errs),
	}
	if sqsCfg.WaitTime < time.Second || sqsCfg.WaitTime > 20*time.Second {
		errs = append(errs, fmt.Errorf("SQS_WAIT_TIME must be between 1s and 20s, got %s", sqsCfg.WaitTime))
	}
	if sqsCfg.MaxMessages > 10 {
		errs = append(errs, fmt.Errorf("SQS_MAX_MESSAGES must be between 1 and 10, got %d", sqsCfg.MaxMessages))
	}
	if sqsCfg.RetryMaxDelay < sqsCfg.RetryBaseDelay || sqsCfg.RetryMaxDelay > 12*time.Hour {
		errs = append(errs, fmt.Errorf("SQS_RETRY_MAX_DELAY must be >= SQS_RETRY_BASE_DELAY and <= 12h, got %s", sqsCfg.RetryMaxDelay))
	}
	toggles := Toggles{
		HTTP:      boolEnv(getenv, "HTTP_ENABLED", &errs),
		Consumer:  boolEnv(getenv, "CONSUMER_ENABLED", &errs),
		Outbox:    boolEnv(getenv, "OUTBOX_ENABLED", &errs),
		RefWorker: boolEnv(getenv, "REFWORKER_ENABLED", &errs),
	}
	outbox := Outbox{
		PollInterval:    positiveDuration(getenv, "OUTBOX_POLL_INTERVAL", 500*time.Millisecond, &errs),
		BatchSize:       positiveInt(getenv, "OUTBOX_BATCH_SIZE", 50, &errs),
		Lease:           positiveDuration(getenv, "OUTBOX_LEASE", 30*time.Second, &errs),
		RetryBaseDelay:  positiveDuration(getenv, "OUTBOX_RETRY_BASE_DELAY", time.Second, &errs),
		RetryMaxDelay:   positiveDuration(getenv, "OUTBOX_RETRY_MAX_DELAY", 5*time.Minute, &errs),
		ShutdownTimeout: positiveDuration(getenv, "OUTBOX_SHUTDOWN_TIMEOUT", 10*time.Second, &errs),
	}
	if outbox.RetryMaxDelay < outbox.RetryBaseDelay {
		errs = append(errs, fmt.Errorf("OUTBOX_RETRY_MAX_DELAY (%s) must be >= OUTBOX_RETRY_BASE_DELAY (%s)", outbox.RetryMaxDelay, outbox.RetryBaseDelay))
	}
	refWorker := RefWorker{
		PollInterval:    positiveDuration(getenv, "REFWORKER_POLL_INTERVAL", time.Second, &errs),
		ShutdownTimeout: positiveDuration(getenv, "REFWORKER_SHUTDOWN_TIMEOUT", 10*time.Second, &errs),
	}
```

e acrescentar `SQS: sqsCfg, Toggles: toggles, Outbox: outbox, RefWorker: refWorker` ao `Config` retornado. Tipos e
helpers:

```go
// SQS configures the SQS clients and the input consumer. Profiles name
// entries of the AWS shared credentials file (AWS_SHARED_CREDENTIALS_FILE);
// empty means the SDK's default credential chain.
type SQS struct {
	Endpoint         string
	Region           string
	ConsumerProfile  string
	PublisherProfile string
	InputQueue       string
	InputDLQ         string
	EventsQueue      string
	Workers          int
	WaitTime         time.Duration
	MaxMessages      int
	RetryBaseDelay   time.Duration
	RetryMaxDelay    time.Duration
	ShutdownTimeout  time.Duration
	// SenderProviders maps the SQS SenderId (the sending account) to the providerId it represents.
	SenderProviders map[string]string
}

// Toggles turn process components on or off (tests and dedicated workers).
type Toggles struct {
	HTTP      bool
	Consumer  bool
	Outbox    bool
	RefWorker bool
}

// Outbox configures the outbox relay.
type Outbox struct {
	PollInterval    time.Duration
	BatchSize       int
	Lease           time.Duration
	RetryBaseDelay  time.Duration
	RetryMaxDelay   time.Duration
	ShutdownTimeout time.Duration
}

// RefWorker configures the PENDING_REFERENCE worker.
type RefWorker struct {
	PollInterval    time.Duration
	ShutdownTimeout time.Duration
}

func boolEnv(getenv func(string) string, key string, errs *[]error) bool {
	switch strings.ToLower(strings.TrimSpace(getenv(key))) {
	case "", "true", "1":
		return true
	case "false", "0":
		return false
	default:
		*errs = append(*errs, fmt.Errorf("%s must be true, false, 1 or 0, got %q", key, getenv(key)))
		return true
	}
}

// senderProviders parses "account=provider,account=provider".
func senderProviders(getenv func(string) string, errs *[]error) map[string]string {
	raw := envOr(getenv, "SQS_SENDER_PROVIDER_MAP", "111111111111=provider-a,222222222222=provider-b")
	out := map[string]string{}
	for _, pair := range strings.Split(raw, ",") {
		sender, provider, ok := strings.Cut(pair, "=")
		sender, provider = strings.TrimSpace(sender), strings.TrimSpace(provider)
		if !ok || sender == "" || provider == "" {
			*errs = append(*errs, fmt.Errorf("SQS_SENDER_PROVIDER_MAP entry %q must be sender=provider", pair))
			continue
		}
		out[sender] = provider
	}
	return out
}
```

- [ ] **Step 4: Escrever o teste do adapter**

`internal/adapters/awssqs/awssqs_test.go`:

```go
//go:build !unit && !e2e

package awssqs

import (
	"context"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

func testConfig(t *testing.T, q sqstest.Queues) config.Config {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	return config.Config{
		Toggles: config.Toggles{Consumer: true, Outbox: true},
		SQS: config.SQS{Endpoint: sqstest.Endpoint(), Region: sqstest.Region,
			InputQueue: q.Input.Name, InputDLQ: q.InputDLQ.Name, EventsQueue: q.Events.Name},
	}
}

type readinessIn struct {
	fx.In
	Checks []health.Check `group:"readiness"`
}

func TestModuleResolvesQueuesAndReportsReadiness(t *testing.T) {
	q := sqstest.NewQueues(t, sqstest.Options{})
	var (
		queues *Queues
		in     readinessIn
	)
	app := fxtest.New(t, fx.NopLogger, Module, fx.Supply(testConfig(t, q)), fx.Populate(&queues),
		fx.Invoke(func(got readinessIn) { in = got }))
	app.RequireStart()
	defer app.RequireStop()

	if queues.Input != q.Input.URL || queues.InputDLQ != q.InputDLQ.URL || queues.Events != q.Events.URL {
		t.Fatalf("queues = %+v, want %+v", queues, q)
	}
	if len(in.Checks) != 1 || in.Checks[0].Name != "sqs" {
		t.Fatalf("checks = %+v", in.Checks)
	}
	if err := in.Checks[0].Probe(context.Background()); err != nil {
		t.Fatalf("sqs probe: %v", err)
	}
}

func TestStartFailsWhenQueueIsMissing(t *testing.T) {
	q := sqstest.NewQueues(t, sqstest.Options{})
	cfg := testConfig(t, q)
	cfg.SQS.InputQueue = "missing-" + q.Input.Name
	app := fx.New(fx.NopLogger, Module, fx.Supply(cfg), fx.Invoke(func(*Queues) {}))
	if err := app.Start(context.Background()); err == nil {
		_ = app.Stop(context.Background())
		t.Fatal("start must fail when a queue does not exist")
	}
}

func TestOnlyEnabledComponentsResolveTheirQueues(t *testing.T) {
	q := sqstest.NewQueues(t, sqstest.Options{})
	cfg := testConfig(t, q)
	cfg.Toggles = config.Toggles{Outbox: true}
	cfg.SQS.InputQueue = "not-used-" + q.Input.Name
	var queues *Queues
	app := fxtest.New(t, fx.NopLogger, Module, fx.Supply(cfg), fx.Populate(&queues))
	app.RequireStart()
	defer app.RequireStop()
	if queues.Input != "" || queues.Events != q.Events.URL {
		t.Fatalf("queues = %+v", queues)
	}
}
```

- [ ] **Step 5: Implementar o adapter**

`internal/adapters/awssqs/awssqs.go`:

```go
// Package awssqs builds the SQS clients (one identity for the consumer, one
// for the publisher), resolves the queues the enabled components use and
// reports SQS readiness.
package awssqs

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
)

// Clients are the SQS identities of the process.
type Clients struct {
	Consumer  *sqs.Client
	Publisher *sqs.Client
}

// NewClients loads both identities. No request is sent here.
func NewClients(cfg config.Config) (*Clients, error) {
	consumer, err := newClient(cfg.SQS, cfg.SQS.ConsumerProfile)
	if err != nil {
		return nil, fmt.Errorf("awssqs: consumer credentials: %w", err)
	}
	publisher, err := newClient(cfg.SQS, cfg.SQS.PublisherProfile)
	if err != nil {
		return nil, fmt.Errorf("awssqs: publisher credentials: %w", err)
	}
	return &Clients{Consumer: consumer, Publisher: publisher}, nil
}

func newClient(cfg config.SQS, profile string) (*sqs.Client, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, err
	}
	return sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	}), nil
}

// Queues are the queue URLs, resolved when the process starts.
type Queues struct {
	Input    string
	InputDLQ string
	Events   string
}

// NewQueues resolves, on start, the queues of the enabled components. Start
// fails if SQS is unreachable or a queue does not exist.
func NewQueues(lc fx.Lifecycle, cfg config.Config, c *Clients) *Queues {
	q := &Queues{}
	lc.Append(fx.Hook{OnStart: func(ctx context.Context) error { return q.resolve(ctx, cfg, c) }})
	return q
}

func (q *Queues) resolve(ctx context.Context, cfg config.Config, c *Clients) error {
	var err error
	if cfg.Toggles.Consumer {
		if q.Input, err = queueURL(ctx, c.Consumer, cfg.SQS.InputQueue); err != nil {
			return err
		}
		if q.InputDLQ, err = queueURL(ctx, c.Consumer, cfg.SQS.InputDLQ); err != nil {
			return err
		}
	}
	if cfg.Toggles.Outbox {
		if q.Events, err = queueURL(ctx, c.Publisher, cfg.SQS.EventsQueue); err != nil {
			return err
		}
	}
	return nil
}

func queueURL(ctx context.Context, c *sqs.Client, name string) (string, error) {
	out, err := c.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		return "", fmt.Errorf("awssqs: queue %s: %w", name, err)
	}
	return aws.ToString(out.QueueUrl), nil
}

// newHealthCheck probes the input queue (or the events queue when the
// consumer is off) with the identity that uses it.
func newHealthCheck(cfg config.Config, c *Clients, q *Queues) health.Check {
	return health.Check{Name: "sqs", Probe: func(ctx context.Context) error {
		client, url := c.Consumer, q.Input
		if !cfg.Toggles.Consumer {
			client, url = c.Publisher, q.Events
		}
		if url == "" {
			return errors.New("awssqs: queues not resolved")
		}
		_, err := client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
			QueueUrl:       aws.String(url),
			AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameApproximateNumberOfMessages},
		})
		return err
	}}
}

// Module provides the clients, the resolved queues and the readiness check.
var Module = fx.Module("sqs",
	fx.Provide(
		NewClients,
		NewQueues,
		fx.Annotate(newHealthCheck, fx.ResultTags(`group:"readiness"`)),
	),
)
```

- [ ] **Step 6: Rodar os testes**

Run: `go test -race -tags=unit ./internal/platform/config/... && go test -race -count=1 -tags=integration ./internal/adapters/awssqs/...`
Expected: `ok`.

- [ ] **Step 7: Commit**

```bash
go mod tidy && gofmt -l . && go vet ./... && go test -race -count=1 ./...
git add internal/platform/config internal/adapters/awssqs go.mod go.sum
git commit -m "feat(sqs): messaging configuration, component toggles and SQS clients with readiness" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Métricas de mensageria, inbox e banco isolado de teste

**Files:**
- Modify: `internal/app/metrics.go`, `internal/app/ports.go`, `internal/platform/metrics/metrics.go`, `internal/platform/metrics/metrics_test.go`, `internal/adapters/postgres/models.go`, `internal/adapters/postgres/module.go`, `internal/testsupport/apptest/apptest.go`, `internal/testsupport/pgtest/pgtest.go`
- Create: `internal/adapters/postgres/inbox_repository.go`
- Test: `internal/adapters/postgres/inbox_repository_test.go` (integration), `internal/testsupport/pgtest/pgtest_test.go` (integration)

**Interfaces:**
- Produces:
  - `app.Metrics` ganha `InboxDuplicate()`, `OutboxPublishAttempt(result string)`, `OutboxLag(d time.Duration)` e
    `ReferenceRetry()`. Constantes `app.OutboxPublished = "published"` e `app.OutboxFailed = "failed"`.
  - `*metrics.Metrics` implementa os quatro e ainda `SQSRetry()` e `SQSDeadLetter(reason string)`.
  - Inbox:
    - `app.InboxMessage{Consumer, MessageID, PayloadHash string; ReceivedAt, ProcessedAt time.Time}`;
    - `app.InboxRepository{Get(ctx, consumer, messageID) (InboxMessage, error); Insert(ctx, InboxMessage) (bool,
      error)}`;
    - `postgres.NewInboxRepository(db) *InboxRepository`, fornecido pelo `postgres.Module` como `app.InboxRepository`.
  - Suporte de teste:
    - `pgtest.FreshDatabase(t) string`: banco novo, migrado, removido no cleanup; devolve a URL do `wallet_app`;
    - `apptest.Harness.Inbox` e `apptest.NewIsolated(t)`, um harness sobre um `FreshDatabase`;
    - `RecordingMetrics` com as chaves `inbox_duplicate`, `outbox:<result>`, `outbox_lag` e `reference_retry`.

- [ ] **Step 1: Escrever os testes que falham**

Acrescentar a `internal/platform/metrics/metrics_test.go`, dentro de `TestMetricsImplementAppMetrics`, antes do
`Gather`:

```go
	m.InboxDuplicate()
	m.OutboxPublishAttempt(app.OutboxPublished)
	m.OutboxLag(250 * time.Millisecond)
	m.ReferenceRetry()
	m.SQSRetry()
	m.SQSDeadLetter("INVALID_MONEY")
	if testutil.ToFloat64(m.sqsDLQ.WithLabelValues("INVALID_MONEY")) != 1 ||
		testutil.ToFloat64(m.outboxAttempts.WithLabelValues("published")) != 1 {
		t.Fatal("messaging counters not recorded")
	}
```

e acrescentar à lista `want` os nomes `"inbox_duplicates_total"`, `"sqs_retries_total"`, `"sqs_dlq_total"`,
`"outbox_lag_seconds"`, `"outbox_publish_attempts_total"` e `"reference_retries_total"`.

`internal/testsupport/pgtest/pgtest_test.go`:

```go
//go:build !unit && !e2e

package pgtest

import "testing"

func TestFreshDatabaseIsMigratedAndIsolated(t *testing.T) {
	url := FreshDatabase(t)
	db := Open(t, url)
	var count int64
	if err := db.Raw(`SELECT count(*) FROM outbox_events`).Scan(&count).Error; err != nil || count != 0 {
		t.Fatalf("fresh outbox count = %d err %v", count, err)
	}
	if err := db.Exec(`INSERT INTO inbox_messages VALUES ('c', 'm', repeat('a', 64), now(), now())`).Error; err != nil {
		t.Fatalf("wallet_app must write the fresh schema: %v", err)
	}
}
```

`internal/adapters/postgres/inbox_repository_test.go`:

```go
//go:build !unit && !e2e

package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func TestInboxRepository(t *testing.T) {
	db := pgtest.AppDB(t)
	repo := NewInboxRepository(db)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Microsecond)
	msg := app.InboxMessage{Consumer: "wager-transactions", MessageID: "msg-" + uuid.NewString(),
		PayloadHash: strings.Repeat("a", 64), ReceivedAt: now, ProcessedAt: now.Add(time.Millisecond)}

	if inserted, err := repo.Insert(ctx, msg); err != nil || !inserted {
		t.Fatalf("insert = %v %v", inserted, err)
	}
	got, err := repo.Get(ctx, msg.Consumer, msg.MessageID)
	if err != nil || got != msg {
		t.Fatalf("get = %+v %v, want %+v", got, err, msg)
	}
	if inserted, err := repo.Insert(ctx, msg); err != nil || inserted {
		t.Fatalf("duplicate insert = %v %v, want false nil", inserted, err)
	}
	other := msg
	other.Consumer = "another-consumer"
	if inserted, err := repo.Insert(ctx, other); err != nil || !inserted {
		t.Fatalf("same messageId for another consumer = %v %v", inserted, err)
	}
	if _, err := repo.Get(ctx, msg.Consumer, "missing-"+uuid.NewString()); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing = %v, want ErrNotFound", err)
	}
}

func TestInboxInsertRollsBackWithItsTransaction(t *testing.T) {
	db := pgtest.AppDB(t)
	repo := NewInboxRepository(db)
	tm := NewTxManager(db, config.Config{Database: config.Database{LockTimeout: time.Second, StatementTimeout: 5 * time.Second}})
	ctx := context.Background()
	id := "msg-" + uuid.NewString()
	boom := errors.New("boom")
	err := tm.WithinTx(ctx, func(ctx context.Context) error {
		now := time.Now().UTC()
		if _, err := repo.Insert(ctx, app.InboxMessage{Consumer: "c", MessageID: id,
			PayloadHash: strings.Repeat("0", 64), ReceivedAt: now, ProcessedAt: now}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := repo.Get(ctx, "c", id); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("rolled-back inbox entry is visible: %v", err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test -tags=integration ./internal/adapters/postgres/ -run Inbox ; go test -tags=unit ./internal/platform/metrics/...`
Expected: FAIL de compilação, `undefined: NewInboxRepository` e `m.InboxDuplicate undefined`.

- [ ] **Step 3: Implementar as métricas**

`internal/app/metrics.go`: acrescentar as constantes e os métodos ao `Metrics` e ao `NopMetrics`.

```go
// Outbox publish results reported to Metrics.
const (
	OutboxPublished = "published"
	OutboxFailed    = "failed"
)
```

Métodos novos da interface `Metrics`, cada um com seu comentário:

```go
	// InboxDuplicate counts a message already handled with the same payload.
	InboxDuplicate()
	// OutboxPublishAttempt counts one publish attempt of an outbox event by result.
	OutboxPublishAttempt(result string)
	// OutboxLag observes the delay between an event's occurrence and its publication.
	OutboxLag(d time.Duration)
	// ReferenceRetry counts a worker attempt that left an operation waiting for its reference.
	ReferenceRetry()
```

Os mesmos quatro no `NopMetrics`, com corpo vazio. `internal/platform/metrics/metrics.go`: novos campos registrados
em `New` e os métodos:

```go
	inboxDuplicates  prometheus.Counter
	sqsRetries       prometheus.Counter
	sqsDLQ           *prometheus.CounterVec
	outboxLag        prometheus.Histogram
	outboxAttempts   *prometheus.CounterVec
	referenceRetries prometheus.Counter
```

```go
		inboxDuplicates: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "inbox_duplicates_total", Help: "SQS messages dropped because the inbox already handled them.",
		}),
		sqsRetries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "sqs_retries_total", Help: "SQS messages left for redelivery after a transient failure.",
		}),
		sqsDLQ: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "sqs_dlq_total", Help: "SQS messages sent to the dead-letter queue by reason.",
		}, []string{"reason"}),
		outboxLag: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name: "outbox_lag_seconds", Help: "Delay between an event's occurrence and its publication.",
			Buckets: []float64{.05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300},
		}),
		outboxAttempts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "outbox_publish_attempts_total", Help: "Outbox publish attempts by result.",
		}, []string{"result"}),
		referenceRetries: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "reference_retries_total", Help: "Worker attempts that left an operation waiting for its reference.",
		}),
```

(acrescente os seis ao `MustRegister`)

```go
// InboxDuplicate implements app.Metrics.
func (m *Metrics) InboxDuplicate() { m.inboxDuplicates.Inc() }

// OutboxPublishAttempt implements app.Metrics.
func (m *Metrics) OutboxPublishAttempt(result string) { m.outboxAttempts.WithLabelValues(result).Inc() }

// OutboxLag implements app.Metrics.
func (m *Metrics) OutboxLag(d time.Duration) { m.outboxLag.Observe(d.Seconds()) }

// ReferenceRetry implements app.Metrics.
func (m *Metrics) ReferenceRetry() { m.referenceRetries.Inc() }

// SQSRetry counts a message left for redelivery.
func (m *Metrics) SQSRetry() { m.sqsRetries.Inc() }

// SQSDeadLetter counts a message sent to the DLQ; reason is a failure code.
func (m *Metrics) SQSDeadLetter(reason string) { m.sqsDLQ.WithLabelValues(reason).Inc() }
```

Em `apptest.RecordingMetrics`:

```go
// InboxDuplicate implements app.Metrics.
func (m *RecordingMetrics) InboxDuplicate() { m.inc("inbox_duplicate") }

// OutboxPublishAttempt implements app.Metrics.
func (m *RecordingMetrics) OutboxPublishAttempt(result string) { m.inc("outbox:" + result) }

// OutboxLag implements app.Metrics.
func (m *RecordingMetrics) OutboxLag(time.Duration) { m.inc("outbox_lag") }

// ReferenceRetry implements app.Metrics.
func (m *RecordingMetrics) ReferenceRetry() { m.inc("reference_retry") }
```

- [ ] **Step 4: Implementar a inbox**

Em `internal/app/ports.go` (acrescente `time` aos imports):

```go
// InboxMessage records that a consumer handled a message. It is written in
// the transaction of the message's effects and identifies redeliveries.
type InboxMessage struct {
	Consumer    string
	MessageID   string
	PayloadHash string
	ReceivedAt  time.Time
	ProcessedAt time.Time
}

// InboxRepository stores handled messages.
type InboxRepository interface {
	// Get returns the handled message; ErrNotFound if unknown.
	Get(ctx context.Context, consumer, messageID string) (InboxMessage, error)
	// Insert records m unless (consumer, messageId) exists; inserted=false reports that.
	Insert(ctx context.Context, m InboxMessage) (inserted bool, err error)
}
```

Em `models.go`:

```go
type inboxMessageModel struct {
	ConsumerName string    `gorm:"column:consumer_name;primaryKey"`
	MessageID    string    `gorm:"column:message_id;primaryKey"`
	PayloadHash  string    `gorm:"column:payload_hash"`
	ReceivedAt   time.Time `gorm:"column:received_at"`
	ProcessedAt  time.Time `gorm:"column:processed_at"`
}

func (inboxMessageModel) TableName() string { return "inbox_messages" }
```

`internal/adapters/postgres/inbox_repository.go`:

```go
package postgres

import (
	"context"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
)

var _ app.InboxRepository = (*InboxRepository)(nil)

// InboxRepository records handled messages. Insert uses ON CONFLICT DO
// NOTHING on (consumer_name, message_id), so a concurrent duplicate waits for
// the winner's commit and then inserts nothing.
type InboxRepository struct {
	db *gorm.DB
}

// NewInboxRepository builds an InboxRepository.
func NewInboxRepository(db *gorm.DB) *InboxRepository { return &InboxRepository{db: db} }

// Get returns the handled message.
func (r *InboxRepository) Get(ctx context.Context, consumer, messageID string) (app.InboxMessage, error) {
	var m inboxMessageModel
	err := conn(ctx, r.db).Where("consumer_name = ? AND message_id = ?", consumer, messageID).Take(&m).Error
	if err != nil {
		return app.InboxMessage{}, mapError(err)
	}
	return app.InboxMessage{Consumer: m.ConsumerName, MessageID: m.MessageID, PayloadHash: m.PayloadHash,
		ReceivedAt: m.ReceivedAt.UTC(), ProcessedAt: m.ProcessedAt.UTC()}, nil
}

// Insert records msg unless it already exists.
func (r *InboxRepository) Insert(ctx context.Context, msg app.InboxMessage) (bool, error) {
	m := inboxMessageModel{ConsumerName: msg.Consumer, MessageID: msg.MessageID, PayloadHash: msg.PayloadHash,
		ReceivedAt: msg.ReceivedAt, ProcessedAt: msg.ProcessedAt}
	res := conn(ctx, r.db).Clauses(clause.OnConflict{DoNothing: true}).Create(&m)
	if res.Error != nil {
		return false, mapError(res.Error)
	}
	return res.RowsAffected == 1, nil
}
```

Em `postgres.Module`, acrescentar `fx.Annotate(NewInboxRepository, fx.As(new(app.InboxRepository)))`.

- [ ] **Step 5: Implementar o banco isolado e o harness**

Em `internal/testsupport/pgtest/pgtest.go` (imports `fmt`, `path/filepath`, `runtime`, `strings`, `time`,
`github.com/golang-migrate/migrate/v4`, `_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"` e
`_ "github.com/golang-migrate/migrate/v4/source/file"`):

```go
// FreshDatabase creates a new database migrated to the latest version,
// dropped at cleanup, and returns its wallet_app URL. Tests whose code claims
// rows globally (outbox relay, reference worker) use it so leftovers of other
// tests are never claimed.
func FreshDatabase(t testing.TB) string {
	t.Helper()
	owner := OwnerDB(t)
	name := fmt.Sprintf("wallet_t_%d", time.Now().UnixNano())
	if err := owner.Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)") })
	_, file, _, _ := runtime.Caller(0)
	migrations := filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations")
	ownerURL := strings.Replace(WithDatabase(OwnerURL(), name), "postgres://", "pgx5://", 1)
	m, err := migrate.New("file://"+migrations, ownerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Up(); err != nil {
		t.Fatalf("migrate fresh database: %v", err)
	}
	return WithDatabase(AppURL(), name)
}
```

O `t.Cleanup` do `owner` (registrado antes pelo `OwnerDB`) roda depois do `DROP`, pela ordem LIFO, então a conexão do
owner ainda está aberta no `DROP`.

Em `apptest`:
- `Harness` ganha `Inbox *postgres.InboxRepository`, preenchido em `New` com `postgres.NewInboxRepository(db)`.
- `New(t)` passa a delegar para uma função interna `newHarness(t, url string)`.
- Acrescentar:

```go
// NewIsolated is New over a fresh database of its own (pgtest.FreshDatabase).
func NewIsolated(t testing.TB) *Harness {
	t.Helper()
	return newHarness(t, pgtest.FreshDatabase(t))
}
```

`newHarness` faz o que `New` faz hoje, usando `url` no lugar de `pgtest.AppURL()`. O `New` chama
`pgtest.AppDB(t)` (falha rápida) e depois `newHarness(t, pgtest.AppURL())`.

- [ ] **Step 6: Rodar os testes**

Run: `go test -race -count=1 ./internal/app/... ./internal/adapters/postgres/... ./internal/platform/metrics/... ./internal/testsupport/...`
Expected: `ok`. Todas as implementações de `app.Metrics` compilam.

- [ ] **Step 7: Commit**

```bash
gofmt -l . && go vet ./... && go test -race -count=1 ./...
git add internal/app internal/platform/metrics internal/adapters/postgres internal/testsupport
git commit -m "feat(messaging): messaging metrics, inbox repository and isolated test databases" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Caso de uso de entrada por mensagem (`IntakeService`)

**Files:**
- Create: `internal/app/intake_service.go`
- Modify: `internal/app/module.go`
- Test: `internal/app/intake_service_test.go` (integration, package `app_test`)

**Interfaces:**
- Consumes: `WageringService.Process(ctx, cmd, meta, alongside)`, `InboxRepository` e `Metrics.InboxDuplicate` (Task 3);
  `apptest.New`, `Command`, `OpenWallet` e `Harness.Inbox`.
- Produces:
  - Constantes: `app.ConsumerWagerTransactions = "wager-transactions"` e `app.IntakeOutcome`, com os valores
    `IntakeHandled`, `IntakeDuplicate` e `IntakeMessageReused`.
  - Resultado: `app.IntakeResult{Outcome IntakeOutcome; Transaction *wagering.WagerTransaction; Replay bool}`.
  - `app.InboxHash(cmd wagering.Command) string`.
  - Serviço: `app.NewIntakeService(d Deps, inbox InboxRepository, wagering *WageringService) *IntakeService` e
    `(*IntakeService).Handle(ctx, messageID string, cmd wagering.Command, meta Meta) (IntakeResult, error)`.
  - `app.Module` fornece `*IntakeService`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/app/intake_service_test.go`:

```go
//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

func newIntake(t *testing.T, h *apptest.Harness) (*app.IntakeService, *app.WageringService) {
	t.Helper()
	ws, err := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return app.NewIntakeService(h.Deps, h.Inbox, ws), ws
}

func balance(t *testing.T, h *apptest.Harness, id uuid.UUID) string {
	t.Helper()
	w, err := h.Wallets.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return w.Balance().Amount()
}

func TestIntakeHandlesOnceAndRecordsInbox(t *testing.T) {
	h := apptest.New(t)
	intake, _ := newIntake(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "25.00", "")
	msgID := "msg-" + uuid.NewString()
	ctx := context.Background()

	res, err := intake.Handle(ctx, msgID, cmd, app.Meta{CorrelationID: "corr-1"})
	if err != nil || res.Outcome != app.IntakeHandled || res.Replay || res.Transaction.Status() != wagering.StatusProcessed {
		t.Fatalf("first = %+v %v", res, err)
	}
	inbox, err := h.Inbox.Get(ctx, app.ConsumerWagerTransactions, msgID)
	if err != nil || inbox.PayloadHash != app.InboxHash(cmd) {
		t.Fatalf("inbox = %+v %v", inbox, err)
	}
	rows := h.OutboxRows(t, w.ID())
	last := rows[len(rows)-1]
	if last.CausationID != msgID || last.CorrelationID != "corr-1" {
		t.Fatalf("event causation/correlation = %q/%q", last.CausationID, last.CorrelationID)
	}
	if h.Metrics.Count("completed:BET:PROCESSED:sqs") != 1 {
		t.Fatal("completion not counted on the sqs channel")
	}

	again, err := intake.Handle(ctx, msgID, cmd, app.Meta{})
	if err != nil || again.Outcome != app.IntakeDuplicate || h.Metrics.Count("inbox_duplicate") != 1 {
		t.Fatalf("redelivery = %+v %v", again, err)
	}
	if got := balance(t, h, w.ID()); got != "75.00" {
		t.Fatalf("balance = %s, want one debit (75.00)", got)
	}
}

func TestIntakeRejectsReusedMessageID(t *testing.T) {
	h := apptest.New(t)
	intake, _ := newIntake(t, h)
	w := h.OpenWallet(t, "100.00")
	msgID := "msg-" + uuid.NewString()
	ctx := context.Background()
	if _, err := intake.Handle(ctx, msgID, apptest.Command(t, w, "provider-a", "BET", "10.00", ""), app.Meta{}); err != nil {
		t.Fatal(err)
	}
	other := apptest.Command(t, w, "provider-a", "BET", "20.00", "")
	res, err := intake.Handle(ctx, msgID, other, app.Meta{})
	if err != nil || res.Outcome != app.IntakeMessageReused {
		t.Fatalf("reused = %+v %v", res, err)
	}
	if _, err := h.Transactions.FindByIdempotencyKey(ctx, "provider-a", other.IdempotencyKey); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("a reused messageId must not process its payload: %v", err)
	}
	if got := balance(t, h, w.ID()); got != "90.00" {
		t.Fatalf("balance = %s", got)
	}
}

func TestIntakeAfterHTTPIsReplayAndRecordsInbox(t *testing.T) {
	h := apptest.New(t)
	intake, ws := newIntake(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "30.00", "")
	ctx := context.Background()
	if _, err := ws.Process(ctx, cmd, app.Meta{Channel: app.ChannelHTTP}, nil); err != nil {
		t.Fatal(err)
	}
	msgID := "msg-" + uuid.NewString()
	res, err := intake.Handle(ctx, msgID, cmd, app.Meta{})
	if err != nil || res.Outcome != app.IntakeHandled || !res.Replay {
		t.Fatalf("cross-channel = %+v %v", res, err)
	}
	if _, err := h.Inbox.Get(ctx, app.ConsumerWagerTransactions, msgID); err != nil {
		t.Fatalf("a replayed message must be recorded in the inbox: %v", err)
	}
	if got := balance(t, h, w.ID()); got != "70.00" {
		t.Fatalf("balance = %s, want one debit", got)
	}
}

func TestIntakeOutcomesAreRecorded(t *testing.T) {
	h := apptest.New(t)
	intake, _ := newIntake(t, h)
	w := h.OpenWallet(t, "10.00")
	ctx := context.Background()
	rejected, err := intake.Handle(ctx, "msg-"+uuid.NewString(), apptest.Command(t, w, "provider-a", "BET", "50.00", ""), app.Meta{})
	if err != nil || rejected.Transaction.Status() != wagering.StatusRejected {
		t.Fatalf("rejected = %+v %v", rejected, err)
	}
	pending, err := intake.Handle(ctx, "msg-"+uuid.NewString(), apptest.Command(t, w, "provider-a", "REFUND", "5.00", "missing-"+uuid.NewString()), app.Meta{})
	if err != nil || pending.Transaction.Status() != wagering.StatusPendingReference {
		t.Fatalf("pending = %+v %v", pending, err)
	}
}

func TestIntakeConcurrentDeliveriesApplyOnce(t *testing.T) {
	h := apptest.New(t)
	intake, _ := newIntake(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "40.00", "")
	msgID := "msg-" + uuid.NewString()

	const n = 10
	results := make([]app.IntakeResult, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i], errs[i] = intake.Handle(context.Background(), msgID, cmd, app.Meta{})
		}()
	}
	wg.Wait()
	handled := 0
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("delivery %d: %v", i, errs[i])
		}
		switch results[i].Outcome {
		case app.IntakeHandled:
			if results[i].Replay {
				t.Fatalf("delivery %d replayed instead of being dropped by the inbox", i)
			}
			handled++
		case app.IntakeDuplicate:
		default:
			t.Fatalf("delivery %d outcome %s", i, results[i].Outcome)
		}
	}
	if handled != 1 {
		t.Fatalf("handled = %d, want exactly 1", handled)
	}
	if got := balance(t, h, w.ID()); got != "60.00" {
		t.Fatalf("balance = %s, want one debit", got)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/app/ -run Intake`
Expected: FAIL de compilação, `undefined: app.NewIntakeService`.

- [ ] **Step 3: Implementar**

`internal/app/intake_service.go`:

```go
package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// ConsumerWagerTransactions is the inbox consumer name of the input queue.
const ConsumerWagerTransactions = "wager-transactions"

// IntakeOutcome says how a message was handled.
type IntakeOutcome string

// Intake outcomes.
const (
	// IntakeHandled: the operation was processed (or replayed) and the inbox
	// entry committed with it.
	IntakeHandled IntakeOutcome = "HANDLED"
	// IntakeDuplicate: the message was already handled with the same payload.
	IntakeDuplicate IntakeOutcome = "DUPLICATE"
	// IntakeMessageReused: the messageId was already handled with another payload.
	IntakeMessageReused IntakeOutcome = "MESSAGE_ID_REUSED"
)

// IntakeResult is the outcome of Handle. Transaction and Replay are set when
// Outcome is IntakeHandled.
type IntakeResult struct {
	Outcome     IntakeOutcome
	Transaction *wagering.WagerTransaction
	Replay      bool
}

// IntakeService handles operations that arrive as messages: the inbox drops
// redeliveries by messageId, and the operation itself goes through
// WageringService.Process, so HTTP and SQS share idempotency.
type IntakeService struct {
	d        Deps
	inbox    InboxRepository
	wagering *WageringService
}

// NewIntakeService builds an IntakeService.
func NewIntakeService(d Deps, inbox InboxRepository, wagering *WageringService) *IntakeService {
	return &IntakeService{d: d, inbox: inbox, wagering: wagering}
}

// InboxHash identifies a message's content: the business payload hash plus
// the idempotency key (both carried by the message's data).
func InboxHash(cmd wagering.Command) string {
	sum := sha256.Sum256([]byte(cmd.PayloadHash() + "\n" + cmd.IdempotencyKey))
	return hex.EncodeToString(sum[:])
}

// errInboxRace aborts the processing transaction when a concurrent delivery
// of the same message recorded the inbox entry first.
var errInboxRace = errors.New("app: message recorded concurrently")

// Handle processes cmd, carried by messageID, at most once per message:
//
//   - a messageId already in the inbox with the same content is
//     IntakeDuplicate; with other content, IntakeMessageReused (nothing runs);
//   - otherwise Process runs with the inbox insert in its transaction, so the
//     entry commits with the outcome (PROCESSED, REJECTED, PENDING_REFERENCE,
//     a replay of an operation received by HTTP, or FAILED).
//
// Errors are those of Process (ErrTransient for retries; the conflict errors
// for a reused key or external id).
func (s *IntakeService) Handle(ctx context.Context, messageID string, cmd wagering.Command, meta Meta) (IntakeResult, error) {
	if messageID == "" {
		return IntakeResult{}, errors.New("app: messageId is required")
	}
	hash := InboxHash(cmd)
	if res, seen, err := s.seen(ctx, messageID, hash); seen || err != nil {
		return res, err
	}
	meta.Channel = ChannelSQS
	meta.CausationID = messageID
	receivedAt := s.d.Clock()
	res, err := s.wagering.Process(ctx, cmd, meta, func(ctx context.Context) error {
		inserted, err := s.inbox.Insert(ctx, InboxMessage{
			Consumer: ConsumerWagerTransactions, MessageID: messageID, PayloadHash: hash,
			ReceivedAt: receivedAt, ProcessedAt: s.d.Clock(),
		})
		if err != nil {
			return err
		}
		if !inserted {
			return errInboxRace
		}
		return nil
	})
	if errors.Is(err, errInboxRace) {
		res, seen, err := s.seen(ctx, messageID, hash)
		if err == nil && !seen {
			return IntakeResult{}, fmt.Errorf("%w: inbox entry %q not visible", ErrTransient, messageID)
		}
		return res, err
	}
	if err != nil {
		return IntakeResult{}, err
	}
	return IntakeResult{Outcome: IntakeHandled, Transaction: res.Transaction, Replay: res.Replay}, nil
}

// seen answers from the inbox; seen=false means the message is new.
func (s *IntakeService) seen(ctx context.Context, messageID, hash string) (IntakeResult, bool, error) {
	m, err := s.inbox.Get(ctx, ConsumerWagerTransactions, messageID)
	switch {
	case errors.Is(err, ErrNotFound):
		return IntakeResult{}, false, nil
	case err != nil:
		return IntakeResult{}, false, err
	case m.PayloadHash != hash:
		return IntakeResult{Outcome: IntakeMessageReused}, true, nil
	}
	s.d.Metrics.InboxDuplicate()
	return IntakeResult{Outcome: IntakeDuplicate}, true, nil
}
```

Em `app.Module`, acrescentar ao `fx.Provide`: `NewIntakeService`.

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -count=1 -tags=integration ./internal/app/...`
Expected: `ok`, com o módulo de app ainda válido.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/app
git commit -m "feat(app): message intake with inbox deduplication sharing the wagering use case" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Consumidor SQS: decodificação estrita e decisão por mensagem

**Files:**
- Create: `internal/adapters/jsonstrict/jsonstrict.go`, `internal/adapters/sqsconsumer/message.go`, `internal/adapters/sqsconsumer/consumer.go`
- Modify: `internal/adapters/httpapi/decode.go` (passa a usar `jsonstrict`)
- Test: `internal/adapters/jsonstrict/jsonstrict_test.go` (unit), `internal/adapters/sqsconsumer/message_test.go` (unit), `internal/adapters/sqsconsumer/consumer_test.go` (integration, package `sqsconsumer`)

**Interfaces:**
- Consumes:
  - `app.IntakeService.Handle` e `app.IntakeResult` (Task 4);
  - `sqstest.*` (Task 1);
  - `apptest.New` e `Harness.Inbox` (Task 3).
- Produces:
  - Decodificação: `jsonstrict.Decode(r io.Reader, v any) error`, que devolve um `*wagering.InputError`.
  - Contrato da mensagem: `sqsconsumer.MessageTypeWagerRequested`.
  - Portas do consumidor:
    - `sqsconsumer.API` (o subconjunto do cliente SQS usado);
    - `sqsconsumer.Intake{Handle(...)}`;
    - `sqsconsumer.Metrics{SQSRetry(); SQSDeadLetter(reason string)}`.
  - `sqsconsumer.Settings{InputURL, DLQURL string; Workers int; WaitTime time.Duration; MaxMessages int;
    RetryBaseDelay, RetryMaxDelay, ShutdownTimeout time.Duration; SenderProviders map[string]string}`.
  - Consumidor: `sqsconsumer.New(api, intake, metrics, log, settings) *Consumer` e
    `(*Consumer).HandleMessage(ctx, msg types.Message)`.

- [ ] **Step 1: Mover a decodificação estrita**

`internal/adapters/jsonstrict/jsonstrict.go`:

```go
// Package jsonstrict decodes request payloads strictly, for HTTP bodies and
// SQS messages alike: unknown fields, trailing data and wrong JSON types are
// correctable input errors, and a numeric money amount is INVALID_MONEY
// (amounts are decimal strings).
package jsonstrict

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// Decode decodes exactly one JSON object from r into v.
func Decode(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var typeErr *json.UnmarshalTypeError
		if errors.As(err, &typeErr) {
			if strings.HasSuffix(typeErr.Field, "amount") {
				return violation(wagering.FailureInvalidMoney, typeErr.Field, "amount must be a decimal string such as \"25.00\"")
			}
			return violation(wagering.FailureInvalidField, typeErr.Field, "must be a "+typeErr.Type.String())
		}
		return violation(wagering.FailureMalformedPayload, "body", fmt.Sprintf("invalid JSON: %v", err))
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return violation(wagering.FailureMalformedPayload, "body", "unexpected data after the JSON object")
	}
	return nil
}

func violation(code wagering.FailureCode, field, reason string) *wagering.InputError {
	return &wagering.InputError{Violations: []wagering.Violation{{Code: code, Field: field, Reason: reason}}}
}
```

`internal/adapters/jsonstrict/jsonstrict_test.go`:

```go
//go:build !integration && !e2e

package jsonstrict

import (
	"errors"
	"strings"
	"testing"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

func TestDecode(t *testing.T) {
	type money struct {
		Amount string `json:"amount"`
	}
	type body struct {
		Name  string `json:"name"`
		Money money  `json:"money"`
	}
	cases := map[string]struct {
		in   string
		code wagering.FailureCode
	}{
		"ok":             {`{"name":"x","money":{"amount":"1.00"}}`, ""},
		"numeric amount": {`{"money":{"amount":1.0}}`, wagering.FailureInvalidMoney},
		"wrong type":     {`{"name":1}`, wagering.FailureInvalidField},
		"unknown field":  {`{"extra":1}`, wagering.FailureMalformedPayload},
		"trailing data":  {`{"name":"x"} {}`, wagering.FailureMalformedPayload},
		"not json":       {`{`, wagering.FailureMalformedPayload},
		"empty":          {``, wagering.FailureMalformedPayload},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var v body
			err := Decode(strings.NewReader(tc.in), &v)
			if tc.code == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			var in *wagering.InputError
			if !errors.As(err, &in) || in.Code() != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
}
```

Em `internal/adapters/httpapi/decode.go`, troque o corpo de `decodeJSON` por:

```go
func decodeJSON(c *gin.Context, v any) error {
	return jsonstrict.Decode(http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes), v)
}
```

Remova os imports que sobrarem. `inputError` continua no `httpapi`, porque o `parseUUIDParam` usa. Os testes de
decodificação do HTTP (Tasks 5–7 do Plano 4) precisam continuar passando sem mudança.

- [ ] **Step 2: Escrever o teste do parsing**

`internal/adapters/sqsconsumer/message_test.go`:

```go
//go:build !integration && !e2e

package sqsconsumer

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

const validBody = `{"messageId":"msg-123","type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z",
"data":{"providerId":"provider-a","externalTransactionId":"transaction-123","idempotencyKey":"provider-a:transaction-123",
"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37",
"roundId":"round-987","gameId":"fortune-chimp","kind":"BET","money":{"amount":"25.00","currency":"BRL"}}}`

func TestParseRequestValid(t *testing.T) {
	req, err := parseRequest(validBody)
	if err != nil {
		t.Fatal(err)
	}
	want := wagering.RawCommand{ProviderID: "provider-a", ExternalTransactionID: "transaction-123",
		IdempotencyKey: "provider-a:transaction-123", PlayerID: "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
		WalletID: "0192f291-27dd-7d3f-8071-5f8685deef37", RoundID: "round-987", GameID: "fortune-chimp",
		Kind: "BET", Amount: "25.00", Currency: "BRL"}
	if req.MessageID != "msg-123" || req.CorrelationID != "" || req.Raw != want {
		t.Fatalf("req = %+v", req)
	}
	if _, err := wagering.ParseCommand(req.Raw); err != nil {
		t.Fatalf("the challenge's example must parse: %v", err)
	}
}

func TestParseRequestInvalid(t *testing.T) {
	cases := map[string]struct {
		body string
		code wagering.FailureCode
	}{
		"not json":         {`{`, wagering.FailureMalformedPayload},
		"unknown field":    {strings.Replace(validBody, `"type"`, `"extra":1,"type"`, 1), wagering.FailureMalformedPayload},
		"numeric amount":   {strings.Replace(validBody, `"amount":"25.00"`, `"amount":25.00`, 1), wagering.FailureInvalidMoney},
		"missing id":       {strings.Replace(validBody, `"messageId":"msg-123",`, ``, 1), wagering.FailureMissingField},
		"long id":          {strings.Replace(validBody, `msg-123`, strings.Repeat("m", 256), 1), wagering.FailureInvalidField},
		"wrong type":       {strings.Replace(validBody, `WagerTransactionRequested`, `Other`, 1), wagering.FailureInvalidField},
		"missing type":     {strings.Replace(validBody, `"type":"WagerTransactionRequested",`, ``, 1), wagering.FailureMissingField},
		"bad occurredAt":   {strings.Replace(validBody, `2026-09-08T12:00:00.000Z`, `yesterday`, 1), wagering.FailureInvalidField},
		"missing occurred": {strings.Replace(validBody, `"occurredAt":"2026-09-08T12:00:00.000Z",`, ``, 1), wagering.FailureMissingField},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseRequest(tc.body)
			var in *wagering.InputError
			if !errors.As(err, &in) || in.Code() != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestRetryDelay(t *testing.T) {
	base, maxDelay := 2*time.Second, 10*time.Second
	for n, want := range map[int]time.Duration{1: 2 * time.Second, 2: 4 * time.Second, 3: 8 * time.Second, 4: 10 * time.Second, 9: 10 * time.Second} {
		if got := retryDelay(n, base, maxDelay); got != want {
			t.Errorf("retryDelay(%d) = %s, want %s", n, got, want)
		}
	}
}
```

- [ ] **Step 3: Implementar o parsing**

`internal/adapters/sqsconsumer/message.go`:

```go
package sqsconsumer

import (
	"fmt"
	"strings"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/jsonstrict"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// MessageTypeWagerRequested is the only message type of the input queue.
const MessageTypeWagerRequested = "WagerTransactionRequested"

type moneyData struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type requestData struct {
	ProviderID                     string    `json:"providerId"`
	ExternalTransactionID          string    `json:"externalTransactionId"`
	IdempotencyKey                 string    `json:"idempotencyKey"`
	PlayerID                       string    `json:"playerId"`
	WalletID                       string    `json:"walletId"`
	RoundID                        string    `json:"roundId"`
	GameID                         string    `json:"gameId"`
	Kind                           string    `json:"kind"`
	Money                          moneyData `json:"money"`
	ReferenceExternalTransactionID string    `json:"referenceExternalTransactionId"`
}

// envelope is the message body. correlationId is optional; without it the
// messageId correlates the logs and events.
type envelope struct {
	MessageID     string      `json:"messageId"`
	Type          string      `json:"type"`
	OccurredAt    string      `json:"occurredAt"`
	CorrelationID string      `json:"correlationId"`
	Data          requestData `json:"data"`
}

// request is a decoded message whose envelope is valid.
type request struct {
	MessageID     string
	CorrelationID string
	Raw           wagering.RawCommand
}

// parseRequest decodes body strictly and validates the envelope. Every error
// is a *wagering.InputError (CORRECTABLE); the data itself is validated later
// by wagering.ParseCommand.
func parseRequest(body string) (request, error) {
	var env envelope
	if err := jsonstrict.Decode(strings.NewReader(body), &env); err != nil {
		return request{}, err
	}
	var v []wagering.Violation
	add := func(code wagering.FailureCode, field, reason string) {
		v = append(v, wagering.Violation{Code: code, Field: field, Reason: reason})
	}
	tooLong := fmt.Sprintf("must be at most %d bytes", wagering.MaxFieldLength)
	switch {
	case env.MessageID == "":
		add(wagering.FailureMissingField, "messageId", "is required")
	case len(env.MessageID) > wagering.MaxFieldLength:
		add(wagering.FailureInvalidField, "messageId", tooLong)
	}
	switch {
	case env.Type == "":
		add(wagering.FailureMissingField, "type", "is required")
	case env.Type != MessageTypeWagerRequested:
		add(wagering.FailureInvalidField, "type", "must be "+MessageTypeWagerRequested)
	}
	if env.OccurredAt == "" {
		add(wagering.FailureMissingField, "occurredAt", "is required")
	} else if _, err := time.Parse(time.RFC3339Nano, env.OccurredAt); err != nil {
		add(wagering.FailureInvalidField, "occurredAt", "must be an RFC 3339 timestamp")
	}
	if len(env.CorrelationID) > wagering.MaxFieldLength {
		add(wagering.FailureInvalidField, "correlationId", tooLong)
	}
	if len(v) > 0 {
		return request{}, &wagering.InputError{Violations: v}
	}
	d := env.Data
	return request{MessageID: env.MessageID, CorrelationID: env.CorrelationID, Raw: wagering.RawCommand{
		ProviderID: d.ProviderID, ExternalTransactionID: d.ExternalTransactionID, IdempotencyKey: d.IdempotencyKey,
		PlayerID: d.PlayerID, WalletID: d.WalletID, RoundID: d.RoundID, GameID: d.GameID, Kind: d.Kind,
		Amount: d.Money.Amount, Currency: d.Money.Currency,
		ReferenceExternalTransactionID: d.ReferenceExternalTransactionID,
	}}, nil
}
```

- [ ] **Step 4: Escrever o teste da decisão**

`internal/adapters/sqsconsumer/consumer_test.go`:

```go
//go:build !unit && !e2e

package sqsconsumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/postgres"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

type fakeMetrics struct {
	mu      sync.Mutex
	retries int
	dlq     map[string]int
}

func (m *fakeMetrics) SQSRetry() { m.mu.Lock(); m.retries++; m.mu.Unlock() }
func (m *fakeMetrics) SQSDeadLetter(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dlq == nil {
		m.dlq = map[string]int{}
	}
	m.dlq[reason]++
}

type fixture struct {
	h       *apptest.Harness
	q       sqstest.Queues
	owner   *sqs.Client
	metrics *fakeMetrics
	c       *Consumer
}

func newFixture(t *testing.T, deps app.Deps, h *apptest.Harness) *fixture {
	t.Helper()
	ws, err := app.NewWageringService(deps, wagering.DefaultRetryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	q := sqstest.NewQueues(t, sqstest.Options{})
	f := &fixture{h: h, q: q, owner: sqstest.Owner(t), metrics: &fakeMetrics{}}
	f.c = New(f.owner, app.NewIntakeService(deps, h.Inbox, ws), f.metrics, slog.New(slog.DiscardHandler), Settings{
		InputURL: q.Input.URL, DLQURL: q.InputDLQ.URL, RetryBaseDelay: time.Second, RetryMaxDelay: 4 * time.Second,
		SenderProviders: map[string]string{sqstest.ProviderAAccount: "provider-a", sqstest.ProviderBAccount: "provider-b"},
	})
	return f
}

func body(msgID, provider string, w *wallet.Wallet, kind, amount, ext, ref string) string {
	refField := ""
	if ref != "" {
		refField = fmt.Sprintf(`,"referenceExternalTransactionId":%q`, ref)
	}
	return fmt.Sprintf(`{"messageId":%q,"type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z",`+
		`"data":{"providerId":%q,"externalTransactionId":%q,"idempotencyKey":%q,"playerId":%q,"walletId":%q,`+
		`"roundId":"round-1","gameId":"game-1","kind":%q,"money":{"amount":%q,"currency":"BRL"}%s}}`,
		msgID, provider, ext, provider+":"+ext, w.PlayerID(), w.ID(), kind, amount, refField)
}

// deliver sends body as sender and returns it as the consumer receives it.
func (f *fixture) deliver(t *testing.T, sender *sqs.Client, body string) types.Message {
	t.Helper()
	sqstest.Send(t, sender, f.q.Input.URL, body, "group-1")
	msgs := sqstest.Receive(t, f.owner, f.q.Input.URL, 10*time.Second)
	if len(msgs) != 1 {
		t.Fatalf("received %d messages, want 1", len(msgs))
	}
	return msgs[0]
}

func (f *fixture) inputEmpty(t *testing.T) {
	t.Helper()
	if msgs := sqstest.Receive(t, f.owner, f.q.Input.URL, 1500*time.Millisecond); len(msgs) != 0 {
		t.Fatalf("input still has %d messages", len(msgs))
	}
}

func (f *fixture) balance(t *testing.T, w *wallet.Wallet) string {
	t.Helper()
	got, err := f.h.Wallets.Get(context.Background(), w.ID())
	if err != nil {
		t.Fatal(err)
	}
	return got.Balance().Amount()
}

func TestHandleProcessesAndDeletes(t *testing.T) {
	h := apptest.New(t)
	f := newFixture(t, h.Deps, h)
	w := h.OpenWallet(t, "100.00")
	msgID := "msg-" + uuid.NewString()
	msg := f.deliver(t, sqstest.Client(t, sqstest.ProviderAAccount), body(msgID, "provider-a", w, "BET", "25.00", uuid.NewString(), ""))

	f.c.HandleMessage(context.Background(), msg)

	f.inputEmpty(t)
	if f.balance(t, w) != "75.00" {
		t.Fatalf("balance = %s", f.balance(t, w))
	}
	if _, err := h.Inbox.Get(context.Background(), app.ConsumerWagerTransactions, msgID); err != nil {
		t.Fatalf("inbox: %v", err)
	}
}

// deleteFails simulates a crash after commit: the delete never reaches SQS.
type deleteFails struct{ API }

func (d deleteFails) DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	return nil, errors.New("connection reset")
}

func TestRedeliveryAfterLostDeleteIsDuplicate(t *testing.T) {
	h := apptest.New(t)
	f := newFixture(t, h.Deps, h)
	w := h.OpenWallet(t, "100.00")
	msg := f.deliver(t, sqstest.Client(t, sqstest.ProviderAAccount), body("msg-"+uuid.NewString(), "provider-a", w, "BET", "25.00", uuid.NewString(), ""))

	crashing := New(deleteFails{f.owner}, f.c.intake, f.metrics, f.c.log, f.c.s)
	crashing.HandleMessage(context.Background(), msg)
	if _, err := f.owner.ChangeMessageVisibility(context.Background(), &sqs.ChangeMessageVisibilityInput{
		QueueUrl: aws.String(f.q.Input.URL), ReceiptHandle: msg.ReceiptHandle, VisibilityTimeout: 0,
	}); err != nil {
		t.Fatal(err)
	}
	again := sqstest.Receive(t, f.owner, f.q.Input.URL, 10*time.Second)
	if len(again) != 1 {
		t.Fatalf("redelivery: got %d messages", len(again))
	}
	f.c.HandleMessage(context.Background(), again[0])

	f.inputEmpty(t)
	if f.balance(t, w) != "75.00" || h.Metrics.Count("inbox_duplicate") != 1 {
		t.Fatalf("balance %s duplicates %d, want one debit and one duplicate", f.balance(t, w), h.Metrics.Count("inbox_duplicate"))
	}
}

func TestHandleDeadLetters(t *testing.T) {
	h := apptest.New(t)
	f := newFixture(t, h.Deps, h)
	w := h.OpenWallet(t, "100.00")
	providerA, providerB := sqstest.Client(t, sqstest.ProviderAAccount), sqstest.Client(t, sqstest.ProviderBAccount)
	reusedID, conflictExt := "msg-"+uuid.NewString(), uuid.NewString()
	f.c.HandleMessage(context.Background(), f.deliver(t, providerA, body(reusedID, "provider-a", w, "BET", "1.00", uuid.NewString(), "")))
	f.c.HandleMessage(context.Background(), f.deliver(t, providerA, body("msg-"+uuid.NewString(), "provider-a", w, "BET", "1.00", conflictExt, "")))

	cases := []struct {
		name   string
		sender *sqs.Client
		body   string
		reason string
	}{
		{"unknown sender", sqstest.Owner(t), body("msg-"+uuid.NewString(), "provider-a", w, "BET", "5.00", uuid.NewString(), ""), "PROVIDER_IDENTITY_MISMATCH"},
		{"spoofed provider", providerB, body("msg-"+uuid.NewString(), "provider-a", w, "BET", "5.00", uuid.NewString(), ""), "PROVIDER_IDENTITY_MISMATCH"},
		{"malformed", providerA, `{"messageId":`, "MALFORMED_PAYLOAD"},
		{"numeric amount", providerA, `{"messageId":"m","type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00Z","data":{"money":{"amount":5}}}`, "INVALID_MONEY"},
		{"opening", providerA, body("msg-"+uuid.NewString(), "provider-a", w, "OPENING", "5.00", uuid.NewString(), ""), "KIND_NOT_ALLOWED"},
		{"reused message id", providerA, body(reusedID, "provider-a", w, "BET", "2.00", uuid.NewString(), ""), "MESSAGE_ID_REUSED"},
		{"key conflict", providerA, body("msg-"+uuid.NewString(), "provider-a", w, "BET", "9.00", conflictExt, ""), "IDEMPOTENCY_KEY_CONFLICT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := f.deliver(t, tc.sender, tc.body)
			f.c.HandleMessage(context.Background(), msg)
			dead := sqstest.Receive(t, f.owner, f.q.InputDLQ.URL, 10*time.Second)
			if len(dead) != 1 || aws.ToString(dead[0].Body) != tc.body || sqstest.FailureReason(dead[0]) != tc.reason {
				t.Fatalf("dlq = %+v, want the original body with failureReason %s", dead, tc.reason)
			}
			sqstest.Delete(t, f.owner, f.q.InputDLQ.URL, dead[0])
			f.inputEmpty(t)
		})
	}
	if f.balance(t, w) != "98.00" {
		t.Fatalf("balance = %s, dead-lettered messages must not move money", f.balance(t, w))
	}
	if f.metrics.dlq["PROVIDER_IDENTITY_MISMATCH"] != 2 || f.metrics.dlq["MESSAGE_ID_REUSED"] != 1 {
		t.Fatalf("dlq metrics = %v", f.metrics.dlq)
	}
}

func TestHandleBusinessOutcomesDelete(t *testing.T) {
	h := apptest.New(t)
	f := newFixture(t, h.Deps, h)
	w := h.OpenWallet(t, "10.00")
	providerA := sqstest.Client(t, sqstest.ProviderAAccount)
	for _, b := range []string{
		body("msg-"+uuid.NewString(), "provider-a", w, "BET", "50.00", uuid.NewString(), ""),                     // REJECTED
		body("msg-"+uuid.NewString(), "provider-a", w, "REFUND", "5.00", uuid.NewString(), "missing-"+uuid.NewString()), // PENDING_REFERENCE
	} {
		f.c.HandleMessage(context.Background(), f.deliver(t, providerA, b))
		f.inputEmpty(t)
	}
	if dead := sqstest.Receive(t, f.owner, f.q.InputDLQ.URL, time.Second); len(dead) != 0 {
		t.Fatal("business outcomes must not be dead-lettered")
	}
}

func TestHandleTransientFailureRetriesLater(t *testing.T) {
	h := apptest.New(t)
	deps := h.Deps
	deps.Tx = postgres.NewTxManager(h.DB, config.Config{Database: config.Database{LockTimeout: 300 * time.Millisecond, StatementTimeout: 5 * time.Second}})
	f := newFixture(t, deps, h)
	w := h.OpenWallet(t, "100.00")

	locked, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- h.Tx.WithinTx(context.Background(), func(ctx context.Context) error {
			if _, err := h.Wallets.GetForUpdate(ctx, w.ID()); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	msg := f.deliver(t, sqstest.Client(t, sqstest.ProviderAAccount), body("msg-"+uuid.NewString(), "provider-a", w, "BET", "25.00", uuid.NewString(), ""))
	f.c.HandleMessage(context.Background(), msg)
	if f.metrics.retries != 1 || f.balance(t, w) != "100.00" {
		t.Fatalf("retries %d balance %s, want a retry and no movement", f.metrics.retries, f.balance(t, w))
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	again := sqstest.Receive(t, f.owner, f.q.Input.URL, 10*time.Second)
	if len(again) != 1 || again[0].Attributes["ApproximateReceiveCount"] != "2" {
		t.Fatalf("redelivery = %+v", again)
	}
	f.c.HandleMessage(context.Background(), again[0])
	f.inputEmpty(t)
	if f.balance(t, w) != "75.00" {
		t.Fatalf("balance = %s after retry", f.balance(t, w))
	}
}

func TestHandleAfterHTTPReplaysAndDeletes(t *testing.T) {
	h := apptest.New(t)
	f := newFixture(t, h.Deps, h)
	w := h.OpenWallet(t, "100.00")
	ext := uuid.NewString()
	cmd, err := wagering.ParseCommand(wagering.RawCommand{ProviderID: "provider-a", ExternalTransactionID: ext,
		IdempotencyKey: "provider-a:" + ext, PlayerID: w.PlayerID().String(), WalletID: w.ID().String(),
		RoundID: "round-1", GameID: "game-1", Kind: "BET", Amount: "40.00", Currency: "BRL"})
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	if _, err := ws.Process(context.Background(), cmd, app.Meta{Channel: app.ChannelHTTP}, nil); err != nil {
		t.Fatal(err)
	}
	f.c.HandleMessage(context.Background(), f.deliver(t, sqstest.Client(t, sqstest.ProviderAAccount),
		body("msg-"+uuid.NewString(), "provider-a", w, "BET", "40.00", ext, "")))
	f.inputEmpty(t)
	if f.balance(t, w) != "60.00" || h.Metrics.Count("replay:sqs") != 1 {
		t.Fatalf("balance %s replays %d, want one debit and an sqs replay", f.balance(t, w), h.Metrics.Count("replay:sqs"))
	}
}
```

- [ ] **Step 5: Rodar e confirmar que falham**

Run: `go test -tags=unit ./internal/adapters/jsonstrict/... ./internal/adapters/sqsconsumer/...`
Expected: FAIL de compilação, `undefined: parseRequest` e `undefined: retryDelay`.

- [ ] **Step 6: Implementar a decisão**

`internal/adapters/sqsconsumer/consumer.go`:

```go
// Package sqsconsumer consumes wager operations from the input FIFO queue.
// Each message is decoded strictly, its sender is mapped to the provider it
// represents, and the operation goes through the same use case as HTTP with
// the inbox entry in the same SQL transaction. A message is deleted only after
// that transaction committed; invalid or refused messages go to the DLQ with a
// failureReason; transient failures are retried with backoff and, once
// exhausted, redriven to the DLQ by SQS.
package sqsconsumer

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// API is the part of the SQS client the consumer uses.
type API interface {
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, opts ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	ChangeMessageVisibility(ctx context.Context, in *sqs.ChangeMessageVisibilityInput, opts ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error)
	SendMessage(ctx context.Context, in *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// Intake is the use case behind the consumer (app.IntakeService).
type Intake interface {
	Handle(ctx context.Context, messageID string, cmd wagering.Command, meta app.Meta) (app.IntakeResult, error)
}

// Metrics are the consumer's own measurements.
type Metrics interface {
	SQSRetry()
	SQSDeadLetter(reason string)
}

// Settings configure the consumer. Queue URLs are known after start.
type Settings struct {
	InputURL        string
	DLQURL          string
	Workers         int
	WaitTime        time.Duration
	MaxMessages     int
	RetryBaseDelay  time.Duration
	RetryMaxDelay   time.Duration
	ShutdownTimeout time.Duration
	// SenderProviders maps the SenderId attribute to the providerId it represents.
	SenderProviders map[string]string
}

// Consumer handles input messages.
type Consumer struct {
	api     API
	intake  Intake
	metrics Metrics
	log     *slog.Logger
	s       Settings

	// lifecycle (runner.go)
	mu          sync.Mutex
	stopPolling context.CancelFunc
	abortWork   context.CancelFunc
	pollCtx     context.Context
	workCtx     context.Context
	done        chan struct{}
}

// New builds a Consumer.
func New(api API, intake Intake, metrics Metrics, log *slog.Logger, s Settings) *Consumer {
	return &Consumer{api: api, intake: intake, metrics: metrics, log: log, s: s}
}

const (
	attrSenderID     = "SenderId"
	attrReceiveCount = "ApproximateReceiveCount"
	attrGroupID      = "MessageGroupId"
	settleTimeout    = 5 * time.Second
)

type action int

const (
	actDelete action = iota
	actDeadLetter
	actRetry
)

type decision struct {
	action action
	reason string
}

// HandleMessage handles one received message and settles it with SQS.
func (c *Consumer) HandleMessage(ctx context.Context, msg types.Message) {
	c.settle(ctx, msg, c.decide(ctx, msg))
}

func (c *Consumer) decide(ctx context.Context, msg types.Message) decision {
	log := c.log.With("sqsMessageId", aws.ToString(msg.MessageId))
	provider, known := c.s.SenderProviders[msg.Attributes[attrSenderID]]
	if !known {
		log.WarnContext(ctx, "message from an unknown sender", "senderId", msg.Attributes[attrSenderID])
		return decision{actDeadLetter, string(wagering.FailureProviderIdentityMismatch)}
	}
	req, err := parseRequest(aws.ToString(msg.Body))
	if err != nil {
		return decision{actDeadLetter, inputCode(err)}
	}
	corr := req.CorrelationID
	if corr == "" {
		corr = req.MessageID
	}
	log = log.With("messageId", req.MessageID, "correlationId", corr, "providerId", provider, "walletId", req.Raw.WalletID)
	if req.Raw.ProviderID != provider {
		log.WarnContext(ctx, "providerId does not match the sender", "claimedProviderId", req.Raw.ProviderID)
		return decision{actDeadLetter, string(wagering.FailureProviderIdentityMismatch)}
	}
	cmd, err := wagering.ParseCommand(req.Raw)
	if err != nil {
		return decision{actDeadLetter, inputCode(err)}
	}
	res, err := c.intake.Handle(ctx, req.MessageID, cmd, app.Meta{CorrelationID: corr})
	switch {
	case errors.Is(err, app.ErrIdempotencyKeyConflict):
		return decision{actDeadLetter, "IDEMPOTENCY_KEY_CONFLICT"}
	case errors.Is(err, app.ErrExternalTransactionConflict):
		return decision{actDeadLetter, "EXTERNAL_TRANSACTION_CONFLICT"}
	case err != nil:
		log.WarnContext(ctx, "message will be retried", "error", err.Error(), "transient", errors.Is(err, app.ErrTransient))
		return decision{action: actRetry}
	}
	switch {
	case res.Outcome == app.IntakeDuplicate:
		log.InfoContext(ctx, "duplicate message dropped")
		return decision{action: actDelete}
	case res.Outcome == app.IntakeMessageReused:
		return decision{actDeadLetter, string(wagering.FailureMessageIDReused)}
	case res.Transaction.Status() == wagering.StatusFailed:
		return decision{actDeadLetter, string(wagering.FailureInfrastructure)}
	}
	return decision{action: actDelete}
}

func inputCode(err error) string {
	var in *wagering.InputError
	if errors.As(err, &in) {
		return string(in.Code())
	}
	return string(wagering.FailureMalformedPayload)
}

// settle applies d. It runs even if ctx was canceled after the commit, so a
// committed message is still deleted; a retry of canceled work releases the
// message at once instead of backing off.
func (c *Consumer) settle(ctx context.Context, msg types.Message, d decision) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	switch d.action {
	case actDeadLetter:
		if err := c.deadLetter(sctx, msg, d.reason); err != nil {
			c.log.ErrorContext(sctx, "dead-letter failed; message will be retried",
				"sqsMessageId", aws.ToString(msg.MessageId), "reason", d.reason, "error", err.Error())
			c.retry(sctx, msg, c.backoff(msg))
			return
		}
		c.metrics.SQSDeadLetter(d.reason)
		c.delete(sctx, msg)
	case actRetry:
		delay := c.backoff(msg)
		if ctx.Err() != nil {
			delay = 0
		}
		c.retry(sctx, msg, delay)
	default:
		c.delete(sctx, msg)
	}
}

func (c *Consumer) deadLetter(ctx context.Context, msg types.Message, reason string) error {
	group := msg.Attributes[attrGroupID]
	if group == "" {
		group = "unknown"
	}
	_, err := c.api.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl: aws.String(c.s.DLQURL), MessageBody: msg.Body,
		MessageGroupId: aws.String(group), MessageDeduplicationId: msg.MessageId,
		MessageAttributes: map[string]types.MessageAttributeValue{
			"failureReason": {DataType: aws.String("String"), StringValue: aws.String(reason)},
		},
	})
	return err
}

func (c *Consumer) delete(ctx context.Context, msg types.Message) {
	if _, err := c.api.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl: aws.String(c.s.InputURL), ReceiptHandle: msg.ReceiptHandle,
	}); err != nil {
		c.log.WarnContext(ctx, "delete failed; the redelivery will be dropped by the inbox",
			"sqsMessageId", aws.ToString(msg.MessageId), "error", err.Error())
	}
}

// retry leaves msg for redelivery after delay.
func (c *Consumer) retry(ctx context.Context, msg types.Message, delay time.Duration) {
	c.metrics.SQSRetry()
	if _, err := c.api.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl: aws.String(c.s.InputURL), ReceiptHandle: msg.ReceiptHandle,
		VisibilityTimeout: int32(delay / time.Second),
	}); err != nil {
		c.log.WarnContext(ctx, "visibility change failed; the queue's visibility timeout applies",
			"sqsMessageId", aws.ToString(msg.MessageId), "error", err.Error())
	}
}

func (c *Consumer) backoff(msg types.Message) time.Duration {
	n, err := strconv.Atoi(msg.Attributes[attrReceiveCount])
	if err != nil || n < 1 {
		n = 1
	}
	return retryDelay(n, c.s.RetryBaseDelay, c.s.RetryMaxDelay)
}

// retryDelay is base×2^(n−1) for the n-th receive, capped at maxDelay.
func retryDelay(n int, base, maxDelay time.Duration) time.Duration {
	d := base
	for i := 1; i < n && d < maxDelay; i++ {
		d *= 2
	}
	return min(d, maxDelay)
}
```

Os campos de ciclo de vida do `Consumer` são usados na Task 6. Até lá, o `go vet` pode acusar campos não usados; se
acusar, mantenha-os e siga.

- [ ] **Step 7: Rodar os testes**

Run: `go test -race -tags=unit ./internal/adapters/jsonstrict/... ./internal/adapters/sqsconsumer/... ./internal/adapters/httpapi/... && go test -race -count=1 -tags=integration ./internal/adapters/sqsconsumer/... ./internal/adapters/httpapi/...`
Expected: `ok`.

- [ ] **Step 8: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/adapters/jsonstrict internal/adapters/sqsconsumer internal/adapters/httpapi
git commit -m "feat(sqs): strict message decoding and per-message delete, dead-letter or retry" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Consumidor SQS: workers, shutdown e módulo Fx

**Files:**
- Create: `internal/adapters/sqsconsumer/runner.go`, `internal/adapters/sqsconsumer/module.go`
- Test: `internal/adapters/sqsconsumer/runner_test.go` (integration, package `sqsconsumer`)

**Interfaces:**
- Consumes:
  - `Consumer`, `HandleMessage` e `Settings` (Task 5);
  - `awssqs.Clients` e `awssqs.Queues` (Task 2);
  - `app.IntakeService` (Task 4);
  - `*metrics.Metrics` (Task 3).
- Produces:
  - Ciclo de vida: `(*Consumer).Start()` e `(*Consumer).Stop(ctx) error`.
  - `sqsconsumer.Module`, que fornece `*Consumer` e o liga ao ciclo de vida do Fx.

- [ ] **Step 1: Escrever o teste que falha**

`internal/adapters/sqsconsumer/runner_test.go`:

```go
//go:build !unit && !e2e

package sqsconsumer

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

func eventually(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func runSettings(q sqstest.Queues) Settings {
	return Settings{InputURL: q.Input.URL, DLQURL: q.InputDLQ.URL, Workers: 2, WaitTime: time.Second, MaxMessages: 10,
		RetryBaseDelay: time.Second, RetryMaxDelay: 4 * time.Second, ShutdownTimeout: 5 * time.Second,
		SenderProviders: map[string]string{sqstest.ProviderAAccount: "provider-a"}}
}

func TestConsumerProcessesWhileRunning(t *testing.T) {
	h := apptest.New(t)
	ws, _ := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	q := sqstest.NewQueues(t, sqstest.Options{})
	c := New(sqstest.Owner(t), app.NewIntakeService(h.Deps, h.Inbox, ws), &fakeMetrics{}, slog.New(slog.DiscardHandler), runSettings(q))
	c.Start()

	providerA := sqstest.Client(t, sqstest.ProviderAAccount)
	shared := h.OpenWallet(t, "100.00")
	others := make([]*wallet.Wallet, 3)
	for i := range others {
		others[i] = h.OpenWallet(t, "50.00")
		sqstest.Send(t, providerA, q.Input.URL, body("msg-"+uuid.NewString(), "provider-a", others[i], "BET", "10.00", uuid.NewString(), ""), others[i].ID().String())
	}
	for range 4 {
		sqstest.Send(t, providerA, q.Input.URL, body("msg-"+uuid.NewString(), "provider-a", shared, "BET", "5.00", uuid.NewString(), ""), shared.ID().String())
	}
	get := func(w *wallet.Wallet) string {
		got, err := h.Wallets.Get(context.Background(), w.ID())
		if err != nil {
			t.Fatal(err)
		}
		return got.Balance().Amount()
	}
	eventually(t, 20*time.Second, func() bool {
		if get(shared) != "80.00" {
			return false
		}
		for _, w := range others {
			if get(w) != "40.00" {
				return false
			}
		}
		return true
	}, "all messages processed")

	start := time.Now()
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("idle stop took %s", time.Since(start))
	}
}

// blockingIntake blocks until its context ends, like a transaction held up
// by a long lock wait, and then reports the cancellation as transient.
type blockingIntake struct{ entered chan struct{} }

func (b blockingIntake) Handle(ctx context.Context, _ string, _ wagering.Command, _ app.Meta) (app.IntakeResult, error) {
	close(b.entered)
	<-ctx.Done()
	return app.IntakeResult{}, app.ErrTransient
}

func TestStopReleasesInFlightMessage(t *testing.T) {
	h := apptest.New(t)
	q := sqstest.NewQueues(t, sqstest.Options{})
	intake := blockingIntake{entered: make(chan struct{})}
	s := runSettings(q)
	s.Workers, s.ShutdownTimeout = 1, 500*time.Millisecond
	owner := sqstest.Owner(t)
	c := New(owner, intake, &fakeMetrics{}, slog.New(slog.DiscardHandler), s)
	c.Start()

	w := h.OpenWallet(t, "10.00")
	sqstest.Send(t, sqstest.Client(t, sqstest.ProviderAAccount), q.Input.URL, body("msg-"+uuid.NewString(), "provider-a", w, "BET", "1.00", uuid.NewString(), ""), "g")
	select {
	case <-intake.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("message never reached the intake")
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := sqstest.Receive(t, owner, q.Input.URL, 3*time.Second)
	if len(again) != 1 || again[0].Attributes["ApproximateReceiveCount"] != "2" {
		t.Fatalf("aborted message must be visible again at once, got %+v", again)
	}
}

// slowIntake signals that it started, finishes its work after a delay and
// reports a duplicate (so the message is deleted).
type slowIntake struct {
	entered  chan struct{}
	once     *sync.Once
	finished *atomic.Int32
}

func (s slowIntake) Handle(context.Context, string, wagering.Command, app.Meta) (app.IntakeResult, error) {
	s.once.Do(func() { close(s.entered) })
	time.Sleep(time.Second)
	s.finished.Add(1)
	return app.IntakeResult{Outcome: app.IntakeDuplicate}, nil
}

func TestStopWaitsForInFlightWork(t *testing.T) {
	h := apptest.New(t)
	q := sqstest.NewQueues(t, sqstest.Options{})
	intake := slowIntake{entered: make(chan struct{}), once: &sync.Once{}, finished: &atomic.Int32{}}
	owner := sqstest.Owner(t)
	c := New(owner, intake, &fakeMetrics{}, slog.New(slog.DiscardHandler), runSettings(q))
	c.Start()
	w := h.OpenWallet(t, "10.00")
	sqstest.Send(t, sqstest.Client(t, sqstest.ProviderAAccount), q.Input.URL, body("msg-"+uuid.NewString(), "provider-a", w, "BET", "1.00", uuid.NewString(), ""), "g")
	select {
	case <-intake.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("message never reached the intake")
	}
	start := time.Now()
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Fatalf("Stop returned after %s, before the in-flight work finished", time.Since(start))
	}
	finished := intake.finished
	if finished.Load() != 1 {
		t.Fatalf("finished = %d, the in-flight message must complete before Stop returns", finished.Load())
	}
	if msgs := sqstest.Receive(t, owner, q.Input.URL, 3*time.Second); len(msgs) != 0 {
		t.Fatalf("completed message was not deleted: %v", aws.ToString(msgs[0].Body))
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/adapters/sqsconsumer/ -run 'Running|Stop'`
Expected: FAIL de compilação, `c.Start undefined`.

- [ ] **Step 3: Implementar**

`internal/adapters/sqsconsumer/runner.go`:

```go
package sqsconsumer

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// abortGrace bounds the wait for workers after their work was canceled.
const abortGrace = 5 * time.Second

// Start launches Settings.Workers goroutines; each long-polls the input
// queue and handles its batch one message at a time.
func (c *Consumer) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pollCtx, c.stopPolling = context.WithCancel(context.Background())
	c.workCtx, c.abortWork = context.WithCancel(context.Background())
	c.done = make(chan struct{})
	finished := make(chan struct{}, c.s.Workers)
	for range c.s.Workers {
		go func() {
			defer func() { finished <- struct{}{} }()
			c.poll()
		}()
	}
	go func() {
		for range c.s.Workers {
			<-finished
		}
		close(c.done)
	}()
}

func (c *Consumer) poll() {
	for c.pollCtx.Err() == nil {
		out, err := c.api.ReceiveMessage(c.pollCtx, &sqs.ReceiveMessageInput{
			QueueUrl:                    aws.String(c.s.InputURL),
			MaxNumberOfMessages:         int32(c.s.MaxMessages),
			WaitTimeSeconds:             int32(c.s.WaitTime / time.Second),
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameAll},
		})
		if err != nil {
			if c.pollCtx.Err() != nil {
				return
			}
			c.log.Warn("receive failed", "error", err.Error())
			select {
			case <-c.pollCtx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		for i, msg := range out.Messages {
			if c.pollCtx.Err() != nil {
				c.release(out.Messages[i:])
				break
			}
			c.HandleMessage(c.workCtx, msg)
		}
	}
}

// release makes received but unstarted messages visible again at once.
func (c *Consumer) release(msgs []types.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), settleTimeout)
	defer cancel()
	for _, msg := range msgs {
		if _, err := c.api.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
			QueueUrl: aws.String(c.s.InputURL), ReceiptHandle: msg.ReceiptHandle, VisibilityTimeout: 0,
		}); err != nil {
			c.log.Warn("release failed; the visibility timeout applies", "sqsMessageId", aws.ToString(msg.MessageId), "error", err.Error())
		}
	}
}

// Stop stops polling and waits for in-flight messages up to
// Settings.ShutdownTimeout (or ctx). When time runs out it cancels the
// in-flight work: transactions roll back and those messages are released.
func (c *Consumer) Stop(ctx context.Context) error {
	c.mu.Lock()
	stopPolling, abortWork, done := c.stopPolling, c.abortWork, c.done
	c.mu.Unlock()
	if done == nil {
		return nil
	}
	stopPolling()
	wait, cancel := context.WithTimeout(ctx, c.s.ShutdownTimeout)
	defer cancel()
	select {
	case <-done:
		abortWork()
		return nil
	case <-wait.Done():
	}
	c.log.Warn("shutdown timeout reached; aborting in-flight messages")
	abortWork()
	select {
	case <-done:
		return nil
	case <-time.After(abortGrace):
		return errors.New("sqsconsumer: workers did not stop after abort")
	}
}
```

`internal/adapters/sqsconsumer/module.go`:

```go
package sqsconsumer

import (
	"context"
	"log/slog"

	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/awssqs"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
)

// Module runs the consumer inside the Fx lifecycle. It starts after the
// queues are resolved and stops before the clients and the database pool.
var Module = fx.Module("sqsconsumer",
	fx.Provide(newConsumer),
	fx.Invoke(register),
)

func newConsumer(cfg config.Config, clients *awssqs.Clients, intake *app.IntakeService, m *metrics.Metrics, log *slog.Logger) *Consumer {
	s := cfg.SQS
	return New(clients.Consumer, intake, m, log.With("component", "sqsconsumer"), Settings{
		Workers: s.Workers, WaitTime: s.WaitTime, MaxMessages: s.MaxMessages,
		RetryBaseDelay: s.RetryBaseDelay, RetryMaxDelay: s.RetryMaxDelay,
		ShutdownTimeout: s.ShutdownTimeout, SenderProviders: s.SenderProviders,
	})
}

func register(lc fx.Lifecycle, c *Consumer, q *awssqs.Queues) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			c.s.InputURL, c.s.DLQURL = q.Input, q.InputDLQ
			c.Start()
			return nil
		},
		OnStop: c.Stop,
	})
}
```

O `*awssqs.Queues` registra o hook dele no construtor, então ele resolve as URLs antes do `OnStart` do consumidor.

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -count=1 -tags=integration ./internal/adapters/sqsconsumer/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/adapters/sqsconsumer
git commit -m "feat(sqs): concurrent long-polling consumer with graceful shutdown and visibility release" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: Relay da outbox (núcleo)

**Files:**
- Modify: `internal/app/ports.go`, `internal/adapters/postgres/outbox_repository.go`, `internal/adapters/postgres/module.go`
- Create: `internal/app/outbox_relay.go`
- Test: `internal/app/outbox_relay_test.go` (integration, package `app_test`)

**Interfaces:**
- Consumes: `apptest.NewIsolated`, `RecordingMetrics` (Task 3) e `app.Metrics.OutboxPublishAttempt/OutboxLag`.
- Produces:
  - `app.OutboxEvent{ID, AggregateID uuid.UUID; EventType string; Payload []byte; OccurredAt time.Time;
    Attempts int}`.
  - `app.OutboxRelayRepository`, com três métodos:
    - `Claim(ctx, owner string, lease time.Duration, limit int) ([]OutboxEvent, error)`;
    - `MarkPublished(ctx, id uuid.UUID, owner string) (bool, error)`;
    - `Reschedule(ctx, id uuid.UUID, owner string, delay time.Duration, lastError string) error`.
  - `app.EventPublisher{Publish(ctx, e OutboxEvent) error}`.
  - Relay:
    - `app.RelaySettings{Owner string; BatchSize int; Lease, RetryBaseDelay, RetryMaxDelay time.Duration}`;
    - `app.NewOutboxRelay(repo, pub, clock, metrics, log, settings) (*OutboxRelay, error)`;
    - `(*OutboxRelay).RunOnce(ctx) (claimed int, err error)`.
  - `postgres.OutboxRepository` implementa `app.OutboxRelayRepository` e o `postgres.Module` o expõe como essa porta
    também.

- [ ] **Step 1: Escrever o teste que falha**

`internal/app/outbox_relay_test.go`:

```go
//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

type recordingPublisher struct {
	mu   sync.Mutex
	fail error
	sent map[uuid.UUID]int
}

func (p *recordingPublisher) Publish(_ context.Context, e app.OutboxEvent) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail != nil {
		return p.fail
	}
	if p.sent == nil {
		p.sent = map[uuid.UUID]int{}
	}
	p.sent[e.ID]++
	return nil
}

func newRelay(t *testing.T, h *apptest.Harness, pub app.EventPublisher, owner string, lease time.Duration) *app.OutboxRelay {
	t.Helper()
	r, err := app.NewOutboxRelay(h.Outbox, pub, h.Deps.Clock, h.Metrics, slog.New(slog.DiscardHandler), app.RelaySettings{
		Owner: owner, BatchSize: 50, Lease: lease, RetryBaseDelay: time.Second, RetryMaxDelay: 4 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// produce commits n BETs on fresh wallets and returns the ids of every event written.
func produce(t *testing.T, h *apptest.Harness, n int) []uuid.UUID {
	t.Helper()
	ws, _ := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	for range n {
		w := h.OpenWallet(t, "100.00")
		if _, err := ws.Process(context.Background(), apptest.Command(t, w, "provider-a", "BET", "1.00", ""), app.Meta{}, nil); err != nil {
			t.Fatal(err)
		}
	}
	var ids []uuid.UUID
	if err := h.DB.Raw(`SELECT id FROM outbox_events ORDER BY occurred_at, id`).Scan(&ids).Error; err != nil {
		t.Fatal(err)
	}
	return ids
}

type outboxState struct {
	PublishedAt *time.Time
	LockedBy    *string
	Attempts    int
	LastError   *string
	NextAttempt time.Time `gorm:"column:next_attempt_at"`
}

func stateOf(t *testing.T, h *apptest.Harness, id uuid.UUID) outboxState {
	t.Helper()
	var s outboxState
	if err := h.DB.Raw(`SELECT published_at, locked_by, attempts, last_error, next_attempt_at FROM outbox_events WHERE id = ?`, id).Scan(&s).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func TestRelayPublishesCommittedEventsOnce(t *testing.T) {
	h := apptest.NewIsolated(t)
	ids := produce(t, h, 3) // OPENING + BET events
	pub := &recordingPublisher{}
	relay := newRelay(t, h, pub, "relay-a", 30*time.Second)
	n, err := relay.RunOnce(context.Background())
	if err != nil || n != len(ids) {
		t.Fatalf("RunOnce = %d %v, want %d", n, err, len(ids))
	}
	for _, id := range ids {
		if pub.sent[id] != 1 || stateOf(t, h, id).PublishedAt == nil {
			t.Fatalf("event %s published %d times, state %+v", id, pub.sent[id], stateOf(t, h, id))
		}
	}
	if n, err := relay.RunOnce(context.Background()); err != nil || n != 0 {
		t.Fatalf("second RunOnce = %d %v, want nothing left", n, err)
	}
	if h.Metrics.Count("outbox:published") != len(ids) || h.Metrics.Count("outbox_lag") != len(ids) {
		t.Fatal("publish metrics missing")
	}
}

func TestConcurrentRelaysClaimDisjointEvents(t *testing.T) {
	h := apptest.NewIsolated(t)
	ids := produce(t, h, 15)
	pub := &recordingPublisher{}
	relays := []*app.OutboxRelay{newRelay(t, h, pub, "relay-a", 30*time.Second), newRelay(t, h, pub, "relay-b", 30*time.Second)}
	var wg sync.WaitGroup
	for _, r := range relays {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				n, err := r.RunOnce(context.Background())
				if err != nil || n == 0 {
					return
				}
			}
		}()
	}
	wg.Wait()
	for _, id := range ids {
		if pub.sent[id] != 1 {
			t.Fatalf("event %s published %d times, want exactly once", id, pub.sent[id])
		}
	}
}

func TestPublishFailureIsRescheduled(t *testing.T) {
	h := apptest.NewIsolated(t)
	ids := produce(t, h, 1)
	pub := &recordingPublisher{fail: errors.New("sqs unavailable")}
	relay := newRelay(t, h, pub, "relay-a", 30*time.Second)
	if _, err := relay.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := stateOf(t, h, ids[0])
	if s.PublishedAt != nil || s.LockedBy != nil || s.Attempts != 1 || s.LastError == nil || *s.LastError != "sqs unavailable" ||
		!s.NextAttempt.After(time.Now().Add(-time.Second)) {
		t.Fatalf("state after failure = %+v", s)
	}
	if n, _ := relay.RunOnce(context.Background()); n != 0 {
		t.Fatal("a rescheduled event must wait for its next attempt")
	}
	pub.fail = nil
	time.Sleep(1200 * time.Millisecond)
	if n, err := relay.RunOnce(context.Background()); err != nil || n != len(ids) || stateOf(t, h, ids[0]).PublishedAt == nil {
		t.Fatalf("retry = %d %v", n, err)
	}
	if h.Metrics.Count("outbox:failed") < 1 {
		t.Fatal("failed attempts not counted")
	}
}

// claimOnly simulates a relay that crashes after publishing, before marking.
type claimOnly struct{ app.OutboxRelayRepository }

func (claimOnly) MarkPublished(context.Context, uuid.UUID, string) (bool, error) {
	return false, errors.New("process died")
}

func TestExpiredLeaseIsRepublishedWithSameEventID(t *testing.T) {
	h := apptest.NewIsolated(t)
	ids := produce(t, h, 1)
	pub := &recordingPublisher{}
	crashed, err := app.NewOutboxRelay(claimOnly{h.Outbox}, pub, h.Deps.Clock, h.Metrics, slog.New(slog.DiscardHandler),
		app.RelaySettings{Owner: "relay-a", BatchSize: 50, Lease: time.Second, RetryBaseDelay: time.Second, RetryMaxDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crashed.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	survivor := newRelay(t, h, pub, "relay-b", 30*time.Second)
	if n, _ := survivor.RunOnce(context.Background()); n != 0 {
		t.Fatal("a leased event must not be claimed before its lease expires")
	}
	time.Sleep(1200 * time.Millisecond)
	if n, err := survivor.RunOnce(context.Background()); err != nil || n != len(ids) {
		t.Fatalf("after lease = %d %v", n, err)
	}
	for _, id := range ids {
		s := stateOf(t, h, id)
		if pub.sent[id] != 2 || s.PublishedAt == nil || s.Attempts != 2 {
			t.Fatalf("event %s sent %d times, state %+v; want republished with the same id", id, pub.sent[id], s)
		}
		if ok, err := h.Outbox.MarkPublished(context.Background(), id, "relay-a"); err != nil || ok {
			t.Fatalf("stale owner mark = %v %v, want false", ok, err)
		}
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/app/ -run 'Relay|Publish|Lease'`
Expected: FAIL de compilação, `undefined: app.NewOutboxRelay`.

- [ ] **Step 3: Implementar as portas e o repositório**

Em `internal/app/ports.go`:

```go
// OutboxEvent is a stored event to publish. ID is the eventId, the
// deduplication key of every republication.
type OutboxEvent struct {
	ID          uuid.UUID
	AggregateID uuid.UUID
	EventType   string
	Payload     []byte
	OccurredAt  time.Time
	Attempts    int
}

// OutboxRelayRepository leases pending events to one relay at a time.
type OutboxRelayRepository interface {
	// Claim leases up to limit unpublished events that are due and not
	// leased (or whose lease expired) to owner for lease, incrementing their
	// attempts. Oldest first.
	Claim(ctx context.Context, owner string, lease time.Duration, limit int) ([]OutboxEvent, error)
	// MarkPublished records the publication if owner still holds the lease.
	MarkPublished(ctx context.Context, id uuid.UUID, owner string) (bool, error)
	// Reschedule releases owner's lease and makes the event due again after
	// delay, keeping lastError.
	Reschedule(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, lastError string) error
}

// EventPublisher sends one event to the broker.
type EventPublisher interface {
	Publish(ctx context.Context, e OutboxEvent) error
}
```

Em `internal/adapters/postgres/outbox_repository.go` (acrescente `sort`, `strings` e `github.com/google/uuid`):

```go
var _ app.OutboxRelayRepository = (*OutboxRepository)(nil)

const maxLastError = 1000

// Claim leases due events with FOR UPDATE SKIP LOCKED, so concurrent relays
// never claim the same event. Times come from the database clock.
func (r *OutboxRepository) Claim(ctx context.Context, owner string, lease time.Duration, limit int) ([]app.OutboxEvent, error) {
	var rows []outboxEventModel
	err := conn(ctx, r.db).Raw(`
UPDATE outbox_events
   SET locked_by = ?, locked_until = now() + make_interval(secs => ?), attempts = attempts + 1
 WHERE id IN (SELECT id FROM outbox_events
               WHERE published_at IS NULL AND next_attempt_at <= now()
                 AND (locked_until IS NULL OR locked_until < now())
               ORDER BY occurred_at, id
               LIMIT ?
               FOR UPDATE SKIP LOCKED)
RETURNING *`, owner, lease.Seconds(), limit).Scan(&rows).Error
	if err != nil {
		return nil, mapError(err)
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].OccurredAt.Equal(rows[j].OccurredAt) {
			return rows[i].OccurredAt.Before(rows[j].OccurredAt)
		}
		return rows[i].ID.String() < rows[j].ID.String()
	})
	out := make([]app.OutboxEvent, 0, len(rows))
	for _, m := range rows {
		out = append(out, app.OutboxEvent{ID: m.ID, AggregateID: m.AggregateID, EventType: m.EventType,
			Payload: m.Payload, OccurredAt: m.OccurredAt.UTC(), Attempts: m.Attempts})
	}
	return out, nil
}

// MarkPublished sets published_at if owner still holds the lease.
func (r *OutboxRepository) MarkPublished(ctx context.Context, id uuid.UUID, owner string) (bool, error) {
	res := conn(ctx, r.db).Exec(`
UPDATE outbox_events SET published_at = now(), locked_by = NULL, locked_until = NULL, last_error = NULL
 WHERE id = ? AND locked_by = ? AND published_at IS NULL`, id, owner)
	if res.Error != nil {
		return false, mapError(res.Error)
	}
	return res.RowsAffected == 1, nil
}

// Reschedule releases owner's lease and schedules the next attempt.
func (r *OutboxRepository) Reschedule(ctx context.Context, id uuid.UUID, owner string, delay time.Duration, lastError string) error {
	if len(lastError) > maxLastError {
		lastError = lastError[:maxLastError]
	}
	lastError = strings.ToValidUTF8(lastError, "?")
	res := conn(ctx, r.db).Exec(`
UPDATE outbox_events
   SET next_attempt_at = now() + make_interval(secs => ?), locked_by = NULL, locked_until = NULL, last_error = ?
 WHERE id = ? AND locked_by = ? AND published_at IS NULL`, delay.Seconds(), lastError, id, owner)
	return mapError(res.Error)
}
```

Em `postgres.Module`, troque a linha do outbox por
`fx.Annotate(NewOutboxRepository, fx.As(new(app.OutboxRepository)), fx.As(new(app.OutboxRelayRepository)))`. Se o
`fx.As` duplo não fornecer as duas portas na versão do Fx em uso, use dois `fx.Provide` com um adaptador e registre a
troca.

- [ ] **Step 4: Implementar o relay**

`internal/app/outbox_relay.go`:

```go
package app

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// RelaySettings tune the outbox relay. Owner identifies this instance's
// leases; Lease must exceed the time to publish one batch.
type RelaySettings struct {
	Owner          string
	BatchSize      int
	Lease          time.Duration
	RetryBaseDelay time.Duration
	RetryMaxDelay  time.Duration
}

// OutboxRelay publishes committed outbox events. Each batch is leased to this
// instance; an event is marked published only after the broker accepted it,
// and a failed publish is rescheduled with capped backoff. If the instance
// dies between publishing and marking, the lease expires and another
// instance republishes the event with the same eventId: delivery is at least
// once and consumers deduplicate by eventId. Events are never dropped.
type OutboxRelay struct {
	repo    OutboxRelayRepository
	pub     EventPublisher
	clock   Clock
	metrics Metrics
	log     *slog.Logger
	s       RelaySettings
}

// NewOutboxRelay validates the settings and builds the relay.
func NewOutboxRelay(repo OutboxRelayRepository, pub EventPublisher, clock Clock, metrics Metrics, log *slog.Logger, s RelaySettings) (*OutboxRelay, error) {
	switch {
	case s.Owner == "":
		return nil, errors.New("app: relay owner is required")
	case s.BatchSize < 1 || s.Lease <= 0 || s.RetryBaseDelay <= 0 || s.RetryMaxDelay < s.RetryBaseDelay:
		return nil, errors.New("app: relay batch size, lease and retry delays must be positive (max >= base)")
	}
	return &OutboxRelay{repo: repo, pub: pub, clock: clock, metrics: metrics, log: log, s: s}, nil
}

// RunOnce publishes one batch and returns how many events it claimed.
// Events left unpublished when ctx ends are retried after their lease.
func (r *OutboxRelay) RunOnce(ctx context.Context) (int, error) {
	events, err := r.repo.Claim(ctx, r.s.Owner, r.s.Lease, r.s.BatchSize)
	if err != nil {
		return 0, err
	}
	for _, e := range events {
		if ctx.Err() != nil {
			break
		}
		r.publish(ctx, e)
	}
	return len(events), nil
}

func (r *OutboxRelay) publish(ctx context.Context, e OutboxEvent) {
	log := r.log.With("eventId", e.ID.String(), "eventType", e.EventType, "walletId", e.AggregateID.String(), "attempts", e.Attempts)
	if err := r.pub.Publish(ctx, e); err != nil {
		r.metrics.OutboxPublishAttempt(OutboxFailed)
		delay := backoff(e.Attempts, r.s.RetryBaseDelay, r.s.RetryMaxDelay)
		log.WarnContext(ctx, "publish failed; rescheduled", "error", err.Error(), "retryIn", delay.String())
		if err := r.repo.Reschedule(context.WithoutCancel(ctx), e.ID, r.s.Owner, delay, err.Error()); err != nil {
			log.ErrorContext(ctx, "reschedule failed; the lease will expire", "error", err.Error())
		}
		return
	}
	r.metrics.OutboxPublishAttempt(OutboxPublished)
	r.metrics.OutboxLag(r.clock().Sub(e.OccurredAt))
	ok, err := r.repo.MarkPublished(context.WithoutCancel(ctx), e.ID, r.s.Owner)
	switch {
	case err != nil:
		log.ErrorContext(ctx, "mark failed; the event will be republished with the same eventId", "error", err.Error())
	case !ok:
		log.InfoContext(ctx, "lease lost before marking; another relay owns the event")
	}
}

// backoff is base×2^(attempt−1), capped at maxDelay.
func backoff(attempt int, base, maxDelay time.Duration) time.Duration {
	d := base
	for i := 1; i < attempt && d < maxDelay; i++ {
		d *= 2
	}
	return min(d, maxDelay)
}
```

- [ ] **Step 5: Rodar os testes**

Run: `go test -race -count=1 -tags=integration ./internal/app/... ./internal/adapters/postgres/...`
Expected: `ok`.

- [ ] **Step 6: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/app internal/adapters/postgres
git commit -m "feat(outbox): leased relay that publishes committed events at least once" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Publisher SQS, loop em segundo plano e módulo do relay

**Files:**
- Create: `internal/adapters/awssqs/publisher.go`, `internal/platform/background/background.go`, `internal/adapters/workers/workers.go`
- Test: `internal/adapters/awssqs/publisher_test.go` (integration), `internal/platform/background/background_test.go` (unit), `internal/adapters/workers/outbox_test.go` (integration)

**Interfaces:**
- Consumes: `app.OutboxRelay`, `app.EventPublisher` e `app.OutboxEvent` (Task 7); `awssqs.Clients/Queues` (Task 2);
  `apptest.NewIsolated` e `sqstest.*`.
- Produces:
  - `awssqs.NewPublisher(c *Clients, q *Queues) *Publisher`, que implementa `app.EventPublisher`.
  - Loop em segundo plano:
    - `background.Job func(ctx) (more bool, err error)`;
    - `background.NewLoop(name string, interval time.Duration, job Job, log *slog.Logger) *Loop`;
    - `(*Loop).Start()` e `(*Loop).Stop(ctx, timeout) error`;
    - `background.Register(lc, loop, stopTimeout)`.
  - `workers.OutboxModule`, que fornece `app.EventPublisher` e `*app.OutboxRelay` e roda o loop.

- [ ] **Step 1: Escrever os testes que falham**

`internal/platform/background/background_test.go`:

```go
//go:build !integration && !e2e

package background

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoopRunsAgainWhileThereIsMoreWork(t *testing.T) {
	var calls atomic.Int32
	loop := NewLoop("test", time.Hour, func(context.Context) (bool, error) {
		return calls.Add(1) < 5, nil
	}, slog.New(slog.DiscardHandler))
	loop.Start()
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := loop.Stop(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Fatalf("calls = %d, want 5 back-to-back runs then idle", calls.Load())
	}
}

func TestLoopWaitsIntervalAfterErrorsAndStopsPromptly(t *testing.T) {
	var calls atomic.Int32
	loop := NewLoop("test", 50*time.Millisecond, func(context.Context) (bool, error) {
		calls.Add(1)
		return true, errors.New("boom")
	}, slog.New(slog.DiscardHandler))
	loop.Start()
	time.Sleep(275 * time.Millisecond)
	if err := loop.Stop(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n < 3 || n > 8 {
		t.Fatalf("calls = %d, want errors to wait one interval each", n)
	}
}

func TestStopCancelsTheRunningJob(t *testing.T) {
	loop := NewLoop("test", time.Hour, func(ctx context.Context) (bool, error) {
		<-ctx.Done()
		return false, ctx.Err()
	}, slog.New(slog.DiscardHandler))
	loop.Start()
	time.Sleep(20 * time.Millisecond)
	if err := loop.Stop(context.Background(), time.Second); err != nil {
		t.Fatalf("stop = %v", err)
	}
}

func TestStopReportsAStuckJob(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	loop := NewLoop("test", time.Hour, func(context.Context) (bool, error) {
		<-release
		return false, nil
	}, slog.New(slog.DiscardHandler))
	loop.Start()
	time.Sleep(20 * time.Millisecond)
	if err := loop.Stop(context.Background(), 50*time.Millisecond); err == nil {
		t.Fatal("a job that ignores cancellation must make Stop fail after the timeout")
	}
}
```

`internal/adapters/awssqs/publisher_test.go`:

```go
//go:build !unit && !e2e

package awssqs

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

func TestPublisherSendsEventToFIFOQueue(t *testing.T) {
	q := sqstest.NewQueues(t, sqstest.Options{})
	owner := sqstest.Owner(t)
	pub := NewPublisher(&Clients{Publisher: owner}, &Queues{Events: q.Events.URL})
	e := app.OutboxEvent{ID: uuid.New(), AggregateID: uuid.New(), EventType: "WagerTransactionProcessed",
		Payload: []byte(`{"eventId":"x"}`), OccurredAt: time.Now()}
	for range 2 { // the second publish is a republication: deduplicated by eventId
		if err := pub.Publish(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	msgs := sqstest.Receive(t, owner, q.Events.URL, 5*time.Second)
	if len(msgs) != 1 {
		t.Fatalf("received %d messages, want 1 (deduplicated)", len(msgs))
	}
	m := msgs[0]
	if aws.ToString(m.Body) != string(e.Payload) || m.Attributes["MessageGroupId"] != e.AggregateID.String() ||
		m.Attributes["MessageDeduplicationId"] != e.ID.String() ||
		aws.ToString(m.MessageAttributes["eventType"].StringValue) != e.EventType {
		t.Fatalf("message = %+v", m)
	}
}
```

`internal/adapters/workers/outbox_test.go`:

```go
//go:build !unit && !e2e

package workers

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/awssqs"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/background"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

func TestOutboxLoopDeliversEventsToTheQueue(t *testing.T) {
	h := apptest.NewIsolated(t)
	q := sqstest.NewQueues(t, sqstest.Options{})
	owner := sqstest.Owner(t)
	relay, err := app.NewOutboxRelay(h.Outbox, awssqs.NewPublisher(&awssqs.Clients{Publisher: owner}, &awssqs.Queues{Events: q.Events.URL}),
		h.Deps.Clock, h.Metrics, slog.New(slog.DiscardHandler),
		app.RelaySettings{Owner: "relay-test", BatchSize: 50, Lease: 30 * time.Second, RetryBaseDelay: time.Second, RetryMaxDelay: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	loop := background.NewLoop("outbox", 100*time.Millisecond, outboxJob(relay, 50), slog.New(slog.DiscardHandler))
	loop.Start()
	defer func() {
		if err := loop.Stop(context.Background(), 5*time.Second); err != nil {
			t.Error(err)
		}
	}()

	w := h.OpenWallet(t, "100.00")
	ws, _ := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	if _, err := ws.Process(context.Background(), apptest.Command(t, w, "provider-a", "BET", "10.00", ""), app.Meta{}, nil); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"WagerTransactionProcessed": false, "WalletBalanceChanged": false}
	deadline := time.Now().Add(15 * time.Second)
	seen := 0
	for seen < 4 && time.Now().Before(deadline) { // OPENING and BET: Processed + BalanceChanged each
		for _, m := range sqstest.Receive(t, owner, q.Events.URL, 2*time.Second) {
			var env struct {
				EventType   string `json:"eventType"`
				AggregateID string `json:"aggregateId"`
			}
			if err := json.Unmarshal([]byte(aws.ToString(m.Body)), &env); err != nil {
				t.Fatal(err)
			}
			if env.AggregateID != w.ID().String() || m.Attributes["MessageGroupId"] != w.ID().String() {
				t.Fatalf("event %+v in group %s", env, m.Attributes["MessageGroupId"])
			}
			want[env.EventType] = true
			seen++
			sqstest.Delete(t, owner, q.Events.URL, m)
		}
	}
	if seen != 4 || !want["WagerTransactionProcessed"] || !want["WalletBalanceChanged"] {
		t.Fatalf("seen %d events, types %v", seen, want)
	}
	var pending int64
	if err := h.DB.Raw(`SELECT count(*) FROM outbox_events WHERE published_at IS NULL`).Scan(&pending).Error; err != nil || pending != 0 {
		t.Fatalf("unpublished = %d %v", pending, err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test -tags=unit ./internal/platform/background/... ; go test -tags=integration ./internal/adapters/awssqs/... ./internal/adapters/workers/...`
Expected: FAIL de compilação, `undefined: NewLoop`, `undefined: NewPublisher` e `undefined: outboxJob`.

- [ ] **Step 3: Implementar**

`internal/platform/background/background.go`:

```go
// Package background runs periodic jobs (outbox relay, reference worker)
// inside the Fx lifecycle: a job runs again at once while it reports more
// work, otherwise after an interval; stopping cancels the running job and
// waits for it, with a deadline.
package background

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.uber.org/fx"
)

// Job does one unit of work; more=true asks to run again at once.
type Job func(ctx context.Context) (more bool, err error)

// Loop runs a Job until stopped.
type Loop struct {
	name     string
	interval time.Duration
	job      Job
	log      *slog.Logger
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewLoop builds a stopped loop.
func NewLoop(name string, interval time.Duration, job Job, log *slog.Logger) *Loop {
	return &Loop{name: name, interval: interval, job: job, log: log.With("component", name)}
}

// Start runs the loop in a goroutine.
func (l *Loop) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel, l.done = cancel, make(chan struct{})
	go l.run(ctx)
}

func (l *Loop) run(ctx context.Context) {
	defer close(l.done)
	for {
		more, err := l.job(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			l.log.WarnContext(ctx, "background job failed", "error", err.Error())
			more = false
		}
		if more {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(l.interval):
		}
	}
}

// Stop cancels the running job and waits for the loop to end, up to timeout
// or ctx.
func (l *Loop) Stop(ctx context.Context, timeout time.Duration) error {
	if l.cancel == nil {
		return nil
	}
	l.cancel()
	select {
	case <-l.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("background: %s: %w", l.name, ctx.Err())
	case <-time.After(timeout):
		return fmt.Errorf("background: %s did not stop within %s", l.name, timeout)
	}
}

// Register starts the loop with the application and stops it on shutdown.
func Register(lc fx.Lifecycle, l *Loop, stopTimeout time.Duration) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error { l.Start(); return nil },
		OnStop:  func(ctx context.Context) error { return l.Stop(ctx, stopTimeout) },
	})
}
```

`internal/adapters/awssqs/publisher.go`:

```go
package awssqs

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
)

var _ app.EventPublisher = (*Publisher)(nil)

// Publisher sends outbox events to the events FIFO queue.
// MessageGroupId = aggregateId (walletId) keeps one wallet's events in order
// per publisher; MessageDeduplicationId = eventId drops republications within
// SQS's deduplication window. Consumers order by walletVersion.
type Publisher struct {
	client *sqs.Client
	queues *Queues
}

// NewPublisher builds a Publisher with the publisher identity.
func NewPublisher(c *Clients, q *Queues) *Publisher { return &Publisher{client: c.Publisher, queues: q} }

// Publish implements app.EventPublisher.
func (p *Publisher) Publish(ctx context.Context, e app.OutboxEvent) error {
	_, err := p.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               aws.String(p.queues.Events),
		MessageBody:            aws.String(string(e.Payload)),
		MessageGroupId:         aws.String(e.AggregateID.String()),
		MessageDeduplicationId: aws.String(e.ID.String()),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"eventType": {DataType: aws.String("String"), StringValue: aws.String(e.EventType)},
		},
	})
	return err
}
```

`internal/adapters/workers/workers.go`:

```go
// Package workers wires the background jobs: the outbox relay and the
// PENDING_REFERENCE worker.
package workers

import (
	"context"
	"log/slog"
	"os"

	"github.com/google/uuid"
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/awssqs"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/background"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

// OutboxModule relays committed outbox events to the events queue.
var OutboxModule = fx.Module("outbox",
	fx.Provide(
		fx.Annotate(awssqs.NewPublisher, fx.As(new(app.EventPublisher))),
		newOutboxRelay,
	),
	fx.Invoke(registerOutbox),
)

func newOutboxRelay(cfg config.Config, repo app.OutboxRelayRepository, pub app.EventPublisher, clock app.Clock, m app.Metrics, log *slog.Logger) (*app.OutboxRelay, error) {
	return app.NewOutboxRelay(repo, pub, clock, m, log.With("component", "outbox"), app.RelaySettings{
		Owner: instanceID(), BatchSize: cfg.Outbox.BatchSize, Lease: cfg.Outbox.Lease,
		RetryBaseDelay: cfg.Outbox.RetryBaseDelay, RetryMaxDelay: cfg.Outbox.RetryMaxDelay,
	})
}

// outboxJob runs one batch; a full batch means there may be more.
func outboxJob(relay *app.OutboxRelay, batch int) background.Job {
	return func(ctx context.Context) (bool, error) {
		n, err := relay.RunOnce(ctx)
		return n == batch, err
	}
}

func registerOutbox(lc fx.Lifecycle, cfg config.Config, relay *app.OutboxRelay, log *slog.Logger) {
	loop := background.NewLoop("outbox", cfg.Outbox.PollInterval, outboxJob(relay, cfg.Outbox.BatchSize), log)
	background.Register(lc, loop, cfg.Outbox.ShutdownTimeout)
}

// instanceID names this process's outbox leases.
func instanceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "wallet"
	}
	return host + "-" + uuid.NewString()[:8]
}
```

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=unit ./internal/platform/background/... && go test -race -count=1 -tags=integration ./internal/adapters/awssqs/... ./internal/adapters/workers/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/adapters/awssqs internal/platform/background internal/adapters/workers
git commit -m "feat(outbox): SQS event publisher and background relay loop" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Worker de referências pendentes

**Files:**
- Modify: `internal/app/ports.go`, `internal/app/wagering_service.go`, `internal/app/module.go`, `internal/adapters/postgres/transaction_repository.go`, `internal/adapters/workers/workers.go`
- Create: `internal/app/reference_service.go`
- Test: `internal/app/reference_service_test.go` (integration, package `app_test`), `internal/adapters/postgres/transaction_repository_test.go` (acrescentar casos)

**Interfaces:**
- Consumes: `WageringService.apply` e `completed` (no mesmo pacote), `background.*` (Task 8) e `apptest.NewIsolated`.
- Produces:
  - Duas operações novas em `app.TransactionRepository`, ambas exigindo transação:
    - `ClaimDuePending(ctx, now time.Time) (*wagering.WagerTransaction, error)`: a pendência mais antiga vencida em
      `now`, com `FOR NO KEY UPDATE SKIP LOCKED`; `ErrNotFound` se não houver nenhuma;
    - `GetForUpdate(ctx, id uuid.UUID) (*wagering.WagerTransaction, error)`: lê e trava uma transação com
      `FOR NO KEY UPDATE`.
  - Refatoração: `(*WageringService).transientFailure(ctx, err) error`, que classifica e conta; `nil` quando o erro é
    permanente. O `failure()` passa a usá-lo, sem mudar o comportamento.
  - Serviço: `app.NewReferenceService(d Deps, wagering *WageringService) *ReferenceService` e
    `(*ReferenceService).ResumeNext(ctx) (found bool, err error)`.
  - Módulos:
    - `app.Module` fornece `*ReferenceService`;
    - `workers.ReferenceModule` roda `ResumeNext` num `background.Loop` com `REFWORKER_POLL_INTERVAL`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/app/reference_service_test.go`:

```go
//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

// worker builds a ReferenceService whose clock runs ahead by skew, so
// pending operations are already due.
func worker(t *testing.T, h *apptest.Harness, policy wagering.RetryPolicy, skew time.Duration, outbox app.OutboxRepository) *app.ReferenceService {
	t.Helper()
	d := h.Deps
	d.Clock = func() time.Time { return apptest.Clock().Add(skew) }
	if outbox != nil {
		d.Outbox = outbox
	}
	ws, err := app.NewWageringService(d, policy)
	if err != nil {
		t.Fatal(err)
	}
	return app.NewReferenceService(d, ws)
}

func process(t *testing.T, h *apptest.Harness, policy wagering.RetryPolicy, cmd wagering.Command) *wagering.WagerTransaction {
	t.Helper()
	ws, err := app.NewWageringService(h.Deps, policy)
	if err != nil {
		t.Fatal(err)
	}
	res, err := ws.Process(context.Background(), cmd, app.Meta{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Transaction
}

func reload(t *testing.T, h *apptest.Harness, tx *wagering.WagerTransaction) *wagering.WagerTransaction {
	t.Helper()
	got, err := h.Transactions.Get(context.Background(), tx.ID())
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestWorkerResolvesWhenReferenceArrives(t *testing.T) {
	h := apptest.NewIsolated(t)
	policy := wagering.DefaultRetryPolicy()
	w := h.OpenWallet(t, "100.00")
	bet := apptest.Command(t, w, "provider-a", "BET", "25.00", "")
	refund := process(t, h, policy, apptest.Command(t, w, "provider-a", "REFUND", "25.00", bet.ExternalTransactionID))
	if refund.Status() != wagering.StatusPendingReference {
		t.Fatalf("refund = %s", refund.Status())
	}
	betTx := process(t, h, policy, bet)

	rs := worker(t, h, policy, time.Hour, nil)
	found, err := rs.ResumeNext(context.Background())
	if err != nil || !found {
		t.Fatalf("ResumeNext = %v %v", found, err)
	}
	got := reload(t, h, refund)
	if got.Status() != wagering.StatusProcessed || got.ReferenceTransactionID() != betTx.ID() || got.Attempts() != 1 {
		t.Fatalf("refund after worker = %+v", got.State())
	}
	if b, _ := h.Wallets.Get(context.Background(), w.ID()); b.Balance().Amount() != "100.00" {
		t.Fatalf("balance = %s, want the bet refunded", b.Balance().Amount())
	}
	rows := h.OutboxRows(t, w.ID())
	last := rows[len(rows)-1]
	if last.CausationID != refund.ID().String() || h.Metrics.Count("completed:REFUND:PROCESSED:worker") != 1 {
		t.Fatalf("last event %+v, worker completion not recorded", last)
	}
	if found, err := rs.ResumeNext(context.Background()); err != nil || found {
		t.Fatalf("nothing left: %v %v", found, err)
	}
}

func TestWorkerKeepsWaitingThenExpires(t *testing.T) {
	h := apptest.NewIsolated(t)
	policy := wagering.RetryPolicy{BaseDelay: time.Second, MaxDelay: time.Second, MaxAttempts: 3}
	w := h.OpenWallet(t, "100.00")
	pending := process(t, h, policy, apptest.Command(t, w, "provider-a", "ROLLBACK", "5.00", "never-arrives"))

	// Each attempt schedules the next one from the worker's clock, so every
	// later attempt needs a clock further ahead.
	if found, err := worker(t, h, policy, time.Hour, nil).ResumeNext(context.Background()); err != nil || !found {
		t.Fatalf("second attempt = %v %v", found, err)
	}
	got := reload(t, h, pending)
	if got.Status() != wagering.StatusPendingReference || got.Attempts() != 2 || h.Metrics.Count("reference_retry") != 1 {
		t.Fatalf("after retry = %+v (retries %d)", got.State(), h.Metrics.Count("reference_retry"))
	}

	if found, err := worker(t, h, policy, 2*time.Hour, nil).ResumeNext(context.Background()); err != nil || !found {
		t.Fatalf("last attempt = %v %v", found, err)
	}
	got = reload(t, h, pending)
	if got.Status() != wagering.StatusRejected || got.FailureCode() != wagering.FailureReferenceNotFound {
		t.Fatalf("expired = %+v", got.State())
	}
	types := map[string]int{}
	for _, r := range h.OutboxRows(t, w.ID()) {
		types[r.EventType]++
	}
	if types["WagerTransactionPendingReference"] != 1 || types["WagerTransactionRejected"] != 1 {
		t.Fatalf("event types = %v, want one pending and one rejected", types)
	}
}

func TestWorkerSkipsOperationsNotYetDue(t *testing.T) {
	h := apptest.NewIsolated(t)
	w := h.OpenWallet(t, "100.00")
	process(t, h, wagering.DefaultRetryPolicy(), apptest.Command(t, w, "provider-a", "REFUND", "5.00", "later"))
	if found, err := worker(t, h, wagering.DefaultRetryPolicy(), 0, nil).ResumeNext(context.Background()); err != nil || found {
		t.Fatalf("not due = %v %v", found, err)
	}
}

func TestConcurrentWorkersResumeEachOperationOnce(t *testing.T) {
	h := apptest.NewIsolated(t)
	policy := wagering.DefaultRetryPolicy()
	const n = 6
	refunds := make([]*wagering.WagerTransaction, n)
	for i := range n {
		w := h.OpenWallet(t, "100.00")
		bet := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
		refunds[i] = process(t, h, policy, apptest.Command(t, w, "provider-a", "REFUND", "10.00", bet.ExternalTransactionID))
		process(t, h, policy, bet)
	}
	var resumed atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		rs := worker(t, h, policy, time.Hour, nil) // built here: t.Fatal only in the test goroutine
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				found, err := rs.ResumeNext(context.Background())
				if err != nil {
					errs <- err
					return
				}
				if !found {
					return
				}
				resumed.Add(1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if resumed.Load() != n {
		t.Fatalf("resumed = %d, want %d (each exactly once)", resumed.Load(), n)
	}
	for _, r := range refunds {
		if reload(t, h, r).Status() != wagering.StatusProcessed {
			t.Fatalf("refund %s not processed", r.ID())
		}
	}
}

type failingOutbox struct{}

func (failingOutbox) Append(context.Context, ...events.Envelope) error { return errors.New("disk full") }

func TestPermanentFailureMarksThePendingRowFailed(t *testing.T) {
	h := apptest.NewIsolated(t)
	policy := wagering.DefaultRetryPolicy()
	w := h.OpenWallet(t, "100.00")
	bet := apptest.Command(t, w, "provider-a", "BET", "20.00", "")
	refund := process(t, h, policy, apptest.Command(t, w, "provider-a", "REFUND", "20.00", bet.ExternalTransactionID))
	process(t, h, policy, bet)

	found, err := worker(t, h, policy, time.Hour, failingOutbox{}).ResumeNext(context.Background())
	if err != nil || !found {
		t.Fatalf("ResumeNext = %v %v", found, err)
	}
	got := reload(t, h, refund)
	if got.Status() != wagering.StatusFailed || got.FailureCode() != wagering.FailureInfrastructure {
		t.Fatalf("refund = %+v, want FAILED/INFRASTRUCTURE_FAILURE", got.State())
	}
	if b, _ := h.Wallets.Get(context.Background(), w.ID()); b.Balance().Amount() != "80.00" {
		t.Fatalf("balance = %s, a failed resume must not move money", b.Balance().Amount())
	}
}
```

Os testes de repositório vão em `internal/adapters/postgres/transaction_repository_test.go`, seguindo o padrão do
arquivo. Três casos:
- `ClaimDuePending` fora de transação → erro;
- uma pendência com `next_attempt_at` futuro não é retornada para `now` atual, mas é para `now + 1h`;
- duas transações concorrentes → enquanto a primeira mantém o claim, a segunda recebe `ErrNotFound` (`SKIP LOCKED`).

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/app/ -run Worker`
Expected: FAIL de compilação, `undefined: app.NewReferenceService`.

- [ ] **Step 3: Implementar o repositório**

Em `internal/app/ports.go`, acrescentar a `TransactionRepository`:

```go
	// ClaimDuePending locks the oldest PENDING_REFERENCE operation due at
	// now, skipping rows locked by other workers; ErrNotFound if none. It
	// requires a transaction.
	ClaimDuePending(ctx context.Context, now time.Time) (*wagering.WagerTransaction, error)
	// GetForUpdate reads and locks a transaction; it requires a transaction.
	GetForUpdate(ctx context.Context, id uuid.UUID) (*wagering.WagerTransaction, error)
```

Em `transaction_repository.go`:

```go
// noKeyUpdate locks the row against writers without blocking the key-share
// locks taken by foreign keys of operations that reference it.
var noKeyUpdate = clause.Locking{Strength: "NO KEY UPDATE"}

// ClaimDuePending locks the oldest due PENDING_REFERENCE row (SKIP LOCKED).
func (r *TransactionRepository) ClaimDuePending(ctx context.Context, now time.Time) (*wagering.WagerTransaction, error) {
	if _, ok := txFrom(ctx); !ok {
		return nil, errNoTransaction
	}
	var m transactionModel
	err := conn(ctx, r.db).
		Clauses(clause.Locking{Strength: noKeyUpdate.Strength, Options: clause.LockingOptionsSkipLocked}).
		Where("status = ? AND next_attempt_at <= ?", string(wagering.StatusPendingReference), now).
		Order("next_attempt_at, id").Take(&m).Error
	if err != nil {
		return nil, mapError(err)
	}
	return transactionFromModel(m)
}

// GetForUpdate reads and locks one transaction.
func (r *TransactionRepository) GetForUpdate(ctx context.Context, id uuid.UUID) (*wagering.WagerTransaction, error) {
	if _, ok := txFrom(ctx); !ok {
		return nil, errNoTransaction
	}
	var m transactionModel
	if err := conn(ctx, r.db).Clauses(noKeyUpdate).Where("id = ?", id).Take(&m).Error; err != nil {
		return nil, mapError(err)
	}
	return transactionFromModel(m)
}
```

(acrescente `time` aos imports). Se o `Take` com `Order` e `Locking` gerar SQL inválido na versão do GORM (por
exemplo, o `FOR` antes do `LIMIT`), use um `Raw` equivalente:
`SELECT * FROM wager_transactions WHERE status = 'PENDING_REFERENCE' AND next_attempt_at <= ? ORDER BY next_attempt_at, id LIMIT 1 FOR NO KEY UPDATE SKIP LOCKED`.
Registre a troca no relatório.

- [ ] **Step 4: Extrair a classificação de erros transitórios**

Em `wagering_service.go`, substituir o `failure` por:

```go
// failure classifies an error of the processing transaction, which has
// already rolled back. Transient errors are returned for retry; nothing is
// recorded. Anything else is a permanent failure and is recorded as FAILED.
func (s *WageringService) failure(ctx context.Context, cmd wagering.Command, meta Meta, alongside func(ctx context.Context) error, err error) (TransactionResult, error) {
	var hookErr *alongsideError
	if errors.As(err, &hookErr) {
		return TransactionResult{}, err
	}
	if terr := s.transientFailure(ctx, err); terr != nil {
		return TransactionResult{}, terr
	}
	return s.recordFailure(ctx, cmd, meta, alongside, err)
}

// transientFailure returns err as a retryable error (counting the conflict)
// or nil when err is a permanent failure.
func (s *WageringService) transientFailure(ctx context.Context, err error) error {
	switch {
	case ctx.Err() != nil:
		// The request was canceled or timed out: the rollback is not a
		// verdict about the operation, so never record FAILED.
		s.d.Metrics.ConcurrencyConflict(ConflictTransient)
		return fmt.Errorf("%w: %w", ErrTransient, ctx.Err())
	case errors.Is(err, ErrLockTimeout):
		s.d.Metrics.ConcurrencyConflict(ConflictLockTimeout)
		return err
	case errors.Is(err, ErrVersionConflict):
		s.d.Metrics.ConcurrencyConflict(ConflictVersion)
		return err
	case errors.Is(err, ErrTransient):
		s.d.Metrics.ConcurrencyConflict(ConflictTransient)
		return err
	case errors.Is(err, ErrConflict):
		// A unique index refused a write inside processing (a race the wallet
		// lock should prevent); retrying re-reads the winner's state.
		s.d.Metrics.ConcurrencyConflict(ConflictUnique)
		return fmt.Errorf("%w: %v", ErrTransient, err)
	}
	return nil
}
```

Os testes do Plano 3 continuam passando sem mudança.

- [ ] **Step 5: Implementar o serviço e o módulo**

`internal/app/reference_service.go`:

```go
package app

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// ReferenceService resumes PENDING_REFERENCE operations: each call claims
// the oldest due operation in its own transaction (FOR NO KEY UPDATE SKIP
// LOCKED, so workers on any instance never take the same row), locks the
// wallet and re-runs the same processing as Process. Restarts lose nothing:
// the pending state lives only in the database.
type ReferenceService struct {
	d        Deps
	wagering *WageringService
}

// NewReferenceService builds a ReferenceService.
func NewReferenceService(d Deps, wagering *WageringService) *ReferenceService {
	return &ReferenceService{d: d, wagering: wagering}
}

// ResumeNext resumes one due operation; found=false when none is due. The
// outcome is PROCESSED, REJECTED (including REFERENCE_NOT_FOUND once the
// attempts are exhausted) or still PENDING_REFERENCE with the next attempt
// scheduled. A transient error leaves the operation due for the next run; a
// permanent one marks it FAILED.
func (s *ReferenceService) ResumeNext(ctx context.Context) (bool, error) {
	var (
		t    *wagering.WagerTransaction
		meta Meta
	)
	err := s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		claimed, err := s.d.Transactions.ClaimDuePending(ctx, s.d.Clock())
		if err != nil {
			return err
		}
		t = claimed
		id := t.ID().String()
		meta = Meta{CorrelationID: id, CausationID: id, Channel: ChannelWorker}
		return s.wagering.apply(ctx, t, meta)
	})
	switch {
	case t == nil && errors.Is(err, ErrNotFound):
		return false, nil
	case t == nil:
		return false, err
	case err != nil:
		if terr := s.wagering.transientFailure(ctx, err); terr != nil {
			return true, terr
		}
		return true, s.fail(ctx, t.ID(), meta, err)
	}
	if t.Status() == wagering.StatusPendingReference {
		s.d.Metrics.ReferenceRetry()
		return true, nil
	}
	s.wagering.completed(ctx, t, meta)
	return true, nil
}

// fail records a permanent infrastructure failure on the pending row itself,
// in a new transaction, unless it was resolved meanwhile.
func (s *ReferenceService) fail(ctx context.Context, id uuid.UUID, meta Meta, cause error) error {
	s.d.Log.ErrorContext(ctx, "permanent failure resuming pending wager transaction",
		"transactionId", id.String(), "error", cause.Error())
	var failed *wagering.WagerTransaction
	err := s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		t, err := s.d.Transactions.GetForUpdate(ctx, id)
		if err != nil {
			return err
		}
		if t.Status() != wagering.StatusPendingReference {
			return nil
		}
		if err := t.MarkFailed(s.d.Clock()); err != nil {
			return err
		}
		failed = t
		return s.d.Transactions.Update(ctx, t)
	})
	if err != nil {
		return errors.Join(cause, err)
	}
	if failed != nil {
		s.wagering.completed(ctx, failed, meta)
	}
	return nil
}
```

Em `app.Module`, acrescentar ao `fx.Provide`: `NewReferenceService`. Em `internal/adapters/workers/workers.go`:

```go
// ReferenceModule resumes due PENDING_REFERENCE operations.
var ReferenceModule = fx.Module("refworker",
	fx.Invoke(func(lc fx.Lifecycle, cfg config.Config, svc *app.ReferenceService, log *slog.Logger) {
		loop := background.NewLoop("refworker", cfg.RefWorker.PollInterval, svc.ResumeNext, log)
		background.Register(lc, loop, cfg.RefWorker.ShutdownTimeout)
	}),
)
```

- [ ] **Step 6: Rodar os testes**

Run: `go test -race -count=1 -tags=integration ./internal/app/... ./internal/adapters/postgres/... ./internal/adapters/workers/...`
Expected: `ok`. Os testes de Process do Plano 3 continuam verdes.

- [ ] **Step 7: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/app internal/adapters/postgres internal/adapters/workers
git commit -m "feat(app): reference worker resuming due pending operations across instances" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Composição com toggles, prazo de desligamento e fluxo completo

**Files:**
- Modify: `internal/platform/composition/composition.go`, `internal/platform/composition/composition_test.go`, `cmd/wallet/main.go`
- Create: `internal/platform/composition/stop_test.go` (unit), `internal/platform/composition/flow_test.go` (integration)

**Interfaces:**
- Consumes: todos os módulos (`awssqs.Module`, `sqsconsumer.Module`, `workers.OutboxModule`,
  `workers.ReferenceModule` e os anteriores).
- Produces:
  - `composition.Modules(cfg config.Config) fx.Option`, que entrega o `cfg` com `fx.Supply` e inclui só os módulos
    ligados. O `config.Module` continua existindo para os testes de módulo.
  - `composition.StopTimeout(cfg) time.Duration`.
  - `cmd/wallet` carrega a config uma vez e usa os dois.

- [ ] **Step 1: Escrever os testes que falham**

`internal/platform/composition/stop_test.go`:

```go
//go:build !integration && !e2e

package composition

import (
	"testing"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

func TestStopTimeoutCoversEnabledComponents(t *testing.T) {
	cfg := config.Config{
		HTTP:      config.HTTP{ShutdownTimeout: 15 * time.Second},
		SQS:       config.SQS{ShutdownTimeout: 20 * time.Second},
		Outbox:    config.Outbox{ShutdownTimeout: 10 * time.Second},
		RefWorker: config.RefWorker{ShutdownTimeout: 10 * time.Second},
		Toggles:   config.Toggles{HTTP: true, Consumer: true, Outbox: true, RefWorker: true},
	}
	if got := StopTimeout(cfg); got != 65*time.Second {
		t.Fatalf("all on = %s, want 65s", got)
	}
	cfg.Toggles = config.Toggles{HTTP: true}
	if got := StopTimeout(cfg); got != 25*time.Second {
		t.Fatalf("http only = %s, want 25s", got)
	}
}
```

Em `internal/platform/composition/composition_test.go`:
- `setEnv` passa a usar `DATABASE_URL=pgtest.FreshDatabase(t)`. Com todos os toggles ligados, o relay e o worker
  fazem claim global; num banco compartilhado, mexeriam nas linhas de testes de outros pacotes rodando em paralelo.
- `setEnv` também define `AWS_ACCESS_KEY_ID=test`, `AWS_SECRET_ACCESS_KEY=test`, `SQS_ENDPOINT=sqstest.Endpoint()`
  e os nomes das filas de um `sqstest.NewQueues(t, sqstest.Options{})`.
- `setEnv` passa a devolver o `config.Config` carregado com `config.Load(os.Getenv)`.
- Os testes existentes usam `composition.Modules(cfg)`.

Acrescente:

```go
func TestToggleCombinationsValidate(t *testing.T) {
	cfg := setEnv(t)
	for name, toggles := range map[string]config.Toggles{
		"all":           {HTTP: true, Consumer: true, Outbox: true, RefWorker: true},
		"http only":     {HTTP: true},
		"consumer only": {Consumer: true},
		"workers only":  {Outbox: true, RefWorker: true},
	} {
		t.Run(name, func(t *testing.T) {
			c := cfg
			c.Toggles = toggles
			if !toggles.HTTP {
				c.Auth = config.Auth{} // a process without HTTP needs no IdP
			}
			if err := fx.ValidateApp(fx.NopLogger, composition.Modules(c)); err != nil {
				t.Fatal(err)
			}
		})
	}
}
```

`internal/platform/composition/flow_test.go`:

```go
//go:build !unit && !e2e

package composition_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/httpapi"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/composition"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

func message(w *wallet.Wallet, kind, amount, ext, ref string) string {
	refField := ""
	if ref != "" {
		refField = fmt.Sprintf(`,"referenceExternalTransactionId":%q`, ref)
	}
	return fmt.Sprintf(`{"messageId":%q,"type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z",`+
		`"data":{"providerId":"provider-a","externalTransactionId":%q,"idempotencyKey":%q,"playerId":%q,"walletId":%q,`+
		`"roundId":"round-1","gameId":"game-1","kind":%q,"money":{"amount":%q,"currency":"BRL"}%s}}`,
		"msg-"+uuid.NewString(), ext, "provider-a:"+ext, w.PlayerID(), w.ID(), kind, amount, refField)
}

func TestSQSToEventsFlow(t *testing.T) {
	q := sqstest.NewQueues(t, sqstest.Options{})
	for k, v := range map[string]string{
		"DATABASE_URL": pgtest.FreshDatabase(t), "OIDC_ISSUER_URL": kctest.IssuerURL(), "HTTP_ADDR": "127.0.0.1:0",
		"LOG_LEVEL": "error", "AWS_ACCESS_KEY_ID": "test", "AWS_SECRET_ACCESS_KEY": "test", "SQS_ENDPOINT": sqstest.Endpoint(),
		"SQS_INPUT_QUEUE": q.Input.Name, "SQS_INPUT_DLQ": q.InputDLQ.Name, "SQS_EVENTS_QUEUE": q.Events.Name,
		"SQS_WAIT_TIME": "1s", "OUTBOX_POLL_INTERVAL": "100ms", "REFWORKER_POLL_INTERVAL": "100ms",
		"REFERENCE_RETRY_BASE_DELAY": "200ms", "REFERENCE_RETRY_MAX_DELAY": "200ms",
	} {
		t.Setenv(k, v)
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	var (
		wallets *app.WalletService
		queries *app.QueryService
		server  *httpapi.Server
	)
	fxApp := fxtest.New(t, fx.NopLogger, composition.Modules(cfg), fx.Populate(&wallets, &queries, &server))
	fxApp.RequireStart()
	stopped := false
	defer func() {
		if !stopped {
			fxApp.RequireStop()
		}
	}()

	amount, _ := money.Parse("100.00", "BRL")
	w, err := wallets.Open(context.Background(), app.OpenWalletCommand{PlayerID: uuid.New(), InitialBalance: amount}, app.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	providerA := sqstest.Client(t, sqstest.ProviderAAccount)
	betExt := uuid.NewString()
	sqstest.Send(t, providerA, q.Input.URL, message(w, "REFUND", "25.00", uuid.NewString(), betExt), w.ID().String())
	sqstest.Send(t, providerA, q.Input.URL, message(w, "BET", "25.00", betExt, ""), w.ID().String())

	types := map[string]int{}
	owner := sqstest.Owner(t)
	for deadline := time.Now().Add(30 * time.Second); types["WalletBalanceChanged"] < 3 && time.Now().Before(deadline); {
		for _, m := range sqstest.Receive(t, owner, q.Events.URL, 2*time.Second) {
			var env struct {
				EventType string `json:"eventType"`
			}
			_ = json.Unmarshal([]byte(aws.ToString(m.Body)), &env)
			types[env.EventType]++
			sqstest.Delete(t, owner, q.Events.URL, m)
		}
	}
	// OPENING, BET and REFUND each change the balance; the REFUND waited first.
	if types["WalletBalanceChanged"] != 3 || types["WagerTransactionPendingReference"] != 1 || types["WagerTransactionProcessed"] != 3 {
		t.Fatalf("published event types = %v", types)
	}
	got, err := queries.Wallet(context.Background(), w.ID())
	if err != nil || got.Balance().Amount() != "100.00" {
		t.Fatalf("balance = %v %v, want the bet refunded by the worker", got.Balance().Amount(), err)
	}

	resp, err := http.Get("http://" + server.Addr() + "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	var ready struct {
		Checks map[string]string `json:"checks"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&ready)
	resp.Body.Close()
	if resp.StatusCode != 200 || ready.Checks["sqs"] != "UP" || ready.Checks["postgres"] != "UP" {
		t.Fatalf("ready = %d %v", resp.StatusCode, ready.Checks)
	}

	start := time.Now()
	fxApp.RequireStop()
	stopped = true
	if time.Since(start) > 15*time.Second {
		t.Fatalf("stop took %s", time.Since(start))
	}
}
```

Os dois arquivos de teste de integração estão no pacote `composition_test`.

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test -tags=unit ./internal/platform/composition/... ; go test -tags=integration ./internal/platform/composition/...`
Expected: FAIL de compilação, `undefined: StopTimeout` e `Modules` com a assinatura errada.

- [ ] **Step 3: Implementar**

`internal/platform/composition/composition.go`:

```go
// Package composition is the composition root: the Fx modules of the
// service, chosen by the component toggles, shared by cmd/wallet and the
// composition tests.
package composition

import (
	"log/slog"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/auth"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/awssqs"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/httpapi"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/postgres"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/sqsconsumer"
	"github.com/brunopstephan/backend-challenge-go/internal/adapters/workers"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/logging"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/metrics"
)

// stopMargin leaves room, after every drain, to close SQS and PostgreSQL.
const stopMargin = 10 * time.Second

// Modules returns the application graph for cfg. Fx starts modules in
// dependency order and stops them in reverse: the HTTP server, the consumer
// and the background workers drain before the database pool closes.
func Modules(cfg config.Config) fx.Option {
	opts := []fx.Option{fx.Supply(cfg), logging.Module, metrics.Module, postgres.Module, app.Module}
	if cfg.Toggles.HTTP {
		opts = append(opts, auth.Module, httpapi.Module)
	}
	if cfg.Toggles.Consumer || cfg.Toggles.Outbox {
		opts = append(opts, awssqs.Module)
	}
	if cfg.Toggles.Consumer {
		opts = append(opts, sqsconsumer.Module)
	}
	if cfg.Toggles.Outbox {
		opts = append(opts, workers.OutboxModule)
	}
	if cfg.Toggles.RefWorker {
		opts = append(opts, workers.ReferenceModule)
	}
	return fx.Options(opts...)
}

// StopTimeout is the Fx stop budget. OnStop hooks run one after another, so
// it is the sum of the enabled components' drain timeouts plus a margin.
func StopTimeout(cfg config.Config) time.Duration {
	d := stopMargin
	if cfg.Toggles.HTTP {
		d += cfg.HTTP.ShutdownTimeout
	}
	if cfg.Toggles.Consumer {
		d += cfg.SQS.ShutdownTimeout
	}
	if cfg.Toggles.Outbox {
		d += cfg.Outbox.ShutdownTimeout
	}
	if cfg.Toggles.RefWorker {
		d += cfg.RefWorker.ShutdownTimeout
	}
	return d
}

// Logger routes Fx's own events to the service logger.
func Logger() fx.Option {
	return fx.WithLogger(func(l *slog.Logger) fxevent.Logger { return &fxevent.SlogLogger{Logger: l} })
}
```

`cmd/wallet/main.go`:

```go
// Command wallet runs the wallet service. Component toggles choose what this
// process runs: the HTTP API, the SQS consumer, the outbox relay and the
// reference worker (all by default).
package main

import (
	"fmt"
	"os"

	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/composition"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

func main() {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	fx.New(composition.Modules(cfg), composition.Logger(), fx.StopTimeout(composition.StopTimeout(cfg))).Run()
}
```

- [ ] **Step 4: Rodar os testes e o binário**

Run:

```bash
go test -race -tags=unit ./internal/platform/composition/...
go test -race -count=1 -tags=integration ./internal/platform/composition/...
go build -o /tmp/wallet ./cmd/wallet && echo build-ok
```

Expected: `ok` e `build-ok`.

- [ ] **Step 5: Verificação final do plano**

```bash
gofmt -l . && go vet ./... && go test -race -tags=unit ./... && go test -race -count=1 ./...
go list -deps ./internal/app | grep -E 'aws|gin|oidc|prometheus|gorm|internal/adapters' ; echo "app deps checked"
go list -f '{{join .Imports "\n"}}' ./internal/domain/... | sort -u
```

Expected:
- Tudo `ok`.
- A busca em `internal/app` não imprime nada antes de `app deps checked`.
- O domínio só importa stdlib, uuid e `internal/domain`.

- [ ] **Step 6: Commit**

```bash
git add internal/platform/composition cmd/wallet
git commit -m "feat(platform): toggle-driven composition with a stop budget covering every component" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
