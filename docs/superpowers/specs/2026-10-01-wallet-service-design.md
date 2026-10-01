# Design — Serviço de Carteiras e Apostas Distribuído (Go)

Data: 2026-10-01 · Status: aguardando revisão · Enunciado: `docs/CHALLENGE.md` (README original)

## 1. Objetivo e escopo

Implementar 100% do enunciado (seções 1–15), sem funcionalidades além do pedido. Critério de pronto: todos os
itens da seção 13 (verificação obrigatória) automatizados e passando, documentação da seção 15 entregue, nenhum
critério eliminatório da seção 14 violado.

Fora de escopo (opcionais do enunciado não adotados): tracing OpenTelemetry, partidas dobradas, testes de carga.
Grafana entra por pedido explícito do usuário.

## 2. Stack e dependências

| Responsabilidade | Escolha |
| --- | --- |
| Linguagem | Go 1.25 (`go.mod` e `golang:1.25` no Dockerfile) |
| Composição | `go.uber.org/fx` |
| HTTP | Gin |
| Persistência | PostgreSQL 17 + GORM (`gorm.io/driver/postgres`, pgx por baixo) com SQL explícito nos caminhos críticos; sem AutoMigrate |
| Migrations | golang-migrate (`NNNNNN_nome.up.sql` / `.down.sql`) |
| Mensageria | AWS SDK Go v2 (SQS) contra **MiniStack** (`ministackorg/ministack`, `AUTH=true`) |
| IdP | Keycloak (realm importado), validação com `github.com/coreos/go-oidc/v3` |
| IDs | `github.com/google/uuid` (UUIDv7) |
| Logs | `log/slog` (JSON) |
| Métricas | `prometheus/client_golang` + Prometheus + Grafana (dashboards provisionados) |
| Testes | `testing` puro (sem testify), `go test -race`, build tags |
| Config | loader manual com `os.Getenv` + validação |

**Por que MiniStack:** desde 23/03/2026 a imagem do LocalStack exige `LOCALSTACK_AUTH_TOKEN` (conta), o que impede
reproduzir de um checkout limpo; IAM enforcement no LocalStack é recurso pago. Spike validou no MiniStack: FIFO,
deduplicação, redrive para DLQ, `AccessDenied` por política IAM, rejeição de access key desconhecida.
Limitações verificadas (documentar): não valida o secret nem a assinatura SigV4; o `SenderId` retorna o account id
(não o user id); um usuário IAM de outra conta é negado no envio cross-account mesmo com políticas corretas (bug
na avaliação da política de identidade). Somente a credencial root da conta funciona cross-account. Spike de
multi-conta: uma access key de 12 dígitos vira root da conta; a política da fila permite ou nega por conta;
`SenderId` = conta remetente.

## 3. Estrutura de pacotes

```
cmd/wallet/main.go            # binário único: HTTP + consumer SQS + workers (toggles por env)
internal/
  domain/                     # puro: sem gin/gorm/fx/aws
    money/                    # Money, Currency, parsing, erros
    wallet/                   # Wallet (agregado), LedgerEntry
    wagering/                 # WagerTransaction, Kind, Status, FailureCode, transições, hash canônico
    events/                   # tipos concretos dos eventos + envelope
  app/                        # casos de uso + portas (interfaces de repositório, TxManager, Clock, IDs)
  adapters/
    postgres/                 # modelos GORM, repositórios, TxManager, mapeamento Money
    httpapi/                  # Gin: handlers, DTOs, middlewares (auth, correlation, log, métricas)
    sqsconsumer/              # consumer da fila de entrada
    outbox/                   # publisher da outbox
    auth/                     # verificador go-oidc + Principal
  platform/                   # config, logger, métricas, health, módulos fx
  faultinject/                # hooks de falha; implementação real só com build tag `faultinject`
migrations/
deploy/                       # keycloak realm, init MiniStack, nginx, prometheus.yml, grafana provisioning
test/integration/ test/e2e/
docs/CHALLENGE.md             # enunciado original movido
```

O domínio não importa Fx, Gin, GORM nem AWS. Modelos de persistência são structs separadas, mapeadas
explicitamente para entidades via reidratação.

## 4. Modelo de domínio

### 4.1 Money
- `type Money struct{ minor int64; currency Currency }`, campos privados, imutável. O zero value é inválido
  (`IsValid()`), e operações sobre um Money não inicializado retornam `ErrInvalidMoney`.
- `Currency`: qualquer código ISO 4217 cujo expoente seja 2 (tabela embutida no pacote). Códigos com outro
  expoente (JPY = 0, BHD = 3) e códigos inexistentes são rejeitados, porque a escala é fixa em 2. Os cenários
  principais usam BRL e os testes de incompatibilidade usam BRL × USD.
- `Parse(amount, currency)` (entrada externa) aceita **só a forma canônica** `^(0|[1-9]\d*)\.\d{2}$`. Rejeita:
  vazio, `25`, `25.0`, `025.00`, `+25.00`, `-1.00`, `1e3`, `NaN`, `Infinity`, espaços e `25.001`. Por isso não há
  normalização antes do hash.
- Overflow checado no parsing, em `Add`, `Sub` e `Negate` (incluindo `MinInt64`). Limite: ±92.233.720.368.547.758,07.
- Operações: `Zero(cur)`, `Add`, `Sub`, `Negate`, `Cmp`, `IsZero`, `IsNegative`, `String()` e `MarshalJSON` →
  `{"amount":"25.00","currency":"BRL"}`. `UnmarshalJSON` usa o parsing estrito. Moedas diferentes → `ErrCurrencyMismatch`.
- Negativos só existem em cálculos internos (ex.: `difference`). Persistência: `*_minor BIGINT` + `currency CHAR(3)`.

### 4.2 Wallet
- Campos: id, playerId, currency, balance, version, createdAt e updatedAt (privados).
- `Open(id, playerId, initialBalance, now)` cria com version 1. Se o saldo inicial for > 0, devolve também o
  `LedgerEntry` de abertura (before 0, after = inicial) **sem incrementar a versão**. `Rehydrate(...)` reconstrói
  sem efeitos.
- `Debit(m)` e `Credit(m)` validam moeda e não negatividade, incrementam a versão e devolvem
  `LedgerEntry` (com saldo anterior e posterior). Débito sem saldo → `ErrInsufficientFunds`.
- `LedgerEntry` imutável: o construtor valida `after = before ± amount` conforme a direção (`DEBIT`/`CREDIT`) e
  amount > 0.

### 4.3 WagerTransaction
- Kinds: `OPENING`, `BET`, `WIN`, `LOSS`, `REFUND`, `ROLLBACK`. Origens: `INTERNAL` (só OPENING) e `EXTERNAL`.
- Construtores distintos: `NewOpening(...)` (sem campos externos) e `NewExternal(cmd)` (todos obrigatórios;
  rejeita OPENING). `Rehydrate(...)` não emite eventos nem reaplica transições.
- Máquina de estados (validada no domínio **e** por trigger no banco):

```
PENDING ──▶ PROCESSED | REJECTED | PENDING_REFERENCE | FAILED
PENDING_REFERENCE ──▶ PROCESSED | REJECTED | FAILED   (retentativa incrementa attempts, sem mudar de estado)
PROCESSED, REJECTED, FAILED: terminais, sem transições
```

- Política de valores: `LOSS` exige exatamente `0.00`. `BET`, `WIN`, `REFUND` e `ROLLBACK` exigem > 0. `OPENING`
  aceita ≥ 0, mas saldo 0 não cria OPENING.
- `FAILED`: falha permanente de infraestrutura, registrada para auditoria em **todos os fluxos**:
  - HTTP/SQS (síncrono): um erro permanente faz rollback da transação principal. Em seguida, uma transação separada
    grava a operação como `FAILED` com `failureCode=INFRASTRUCTURE_FAILURE`. O HTTP responde 500 com o failureCode, e
    no SQS a mensagem vai para a DLQ. O replay devolve `FAILED`.
  - Worker de referências: um erro não retentável, ou transitórios acima de `REFERENCE_MAX_INFRA_ATTEMPTS` →
    `PENDING_REFERENCE → FAILED`.
  - Um erro transitório nunca grava FAILED: dá rollback e o cliente, o SQS ou o worker faz retry.
- **Transitório vs permanente:** transitório = falha de conexão, timeout, `55P03` lock_timeout, `40001`, `40P01`,
  `57014` statement_timeout, throttling ou 5xx do SQS. Permanente = validação, decodificação, violação de
  constraint inesperada.

### 4.4 Erros
Erros de domínio são sentinelas ou tipos (`errors.Is`/`errors.As`), nunca `panic`. `*RejectionError{Code}` carrega
o `FailureCode` estável. Todo I/O recebe `context.Context`.

### 4.5 Failure codes (estáveis, documentados)
Toda rejeição traz um `failureCode` estável e uma `category`: `CORRECTABLE` (entrada corrigível) ou `DEFINITIVE`
(resultado definitivo).

- **Entrada inválida — `CORRECTABLE`, 400, não persistida** (o construtor do domínio rejeita o valor, então não
  existe entidade a salvar):
  - `MALFORMED_PAYLOAD`: JSON ou envelope inválido
  - `MISSING_FIELD`
  - `INVALID_ID`: UUID não canônico ou inválido
  - `INVALID_MONEY`: formato, escala, sinal ou overflow
  - `UNSUPPORTED_CURRENCY`
  - `UNKNOWN_KIND`
  - `KIND_NOT_ALLOWED`: OPENING enviado por HTTP ou SQS
  - `INVALID_AMOUNT_FOR_KIND`: `LOSS ≠ 0.00`, ou zero em BET/WIN/REFUND/ROLLBACK
  - `MISSING_REFERENCE`: REFUND/ROLLBACK sem `referenceExternalTransactionId`
  - `MISSING_IDEMPOTENCY_KEY`
  - `INVALID_IDEMPOTENCY_KEY`

  O corpo inclui `details[{field, reason}]`.
- **Rejeição de negócio — `DEFINITIVE`, persistida como `REJECTED`, 422:**
  - saldo: `INSUFFICIENT_FUNDS`, `INSUFFICIENT_FUNDS_FOR_REVERSAL`
  - carteira: `WALLET_NOT_FOUND`, `WALLET_PLAYER_MISMATCH`, `CURRENCY_MISMATCH`
  - referência: `REFERENCE_NOT_FOUND`, `REFERENCE_NOT_PROCESSED`, `REFERENCE_KIND_INVALID`, `REFERENCE_MISMATCH`,
    `AMOUNT_MISMATCH`, `REFERENCE_ALREADY_REVERSED`
- **Falha permanente de infraestrutura — `DEFINITIVE`, persistida como `FAILED`, 500:** `INFRASTRUCTURE_FAILURE`.
- **Somente SQS:** `PROVIDER_IDENTITY_MISMATCH`, `MESSAGE_ID_REUSED`. Vão para a DLQ sem efeito, com
  `failureReason`.

## 5. Operações e referências

| Tipo | Movimento | Referência |
| --- | --- | --- |
| BET | débito; saldo suficiente | — |
| WIN | crédito | opcional: BET `PROCESSED` da mesma rodada (o valor pode diferir) |
| LOSS | nenhum; sem ledger e sem bump de versão; emite só `WagerTransactionProcessed` | — |
| REFUND | crédito do valor da BET | obrigatória: BET `PROCESSED` |
| ROLLBACK | inverso do original (BET→crédito, WIN→débito, REFUND→débito) | obrigatória: BET, WIN ou REFUND `PROCESSED` |

- Resolução por `(providerId, referenceExternalTransactionId)` com a carteira já travada. A operação e a referência
  devem concordar em jogador, carteira, moeda e rodada (`REFERENCE_MISMATCH`). Nas reversões o valor deve ser
  igual (`AMOUNT_MISMATCH`).
- **Uma referência não recebe duas reversões bem-sucedidas do mesmo tipo** (literal do enunciado): índice único
  parcial `(reference_transaction_id, kind) WHERE status='PROCESSED' AND kind IN ('REFUND','ROLLBACK')`.
- **REFUND e ROLLBACK sobre a mesma BET** devolveriam o mesmo débito duas vezes. Por isso, uma BET aceita **uma
  única** reversão bem-sucedida, seja REFUND ou ROLLBACK: segundo índice único parcial
  `(reference_transaction_id) WHERE status='PROCESSED' AND kind IN ('REFUND','ROLLBACK') AND reference_kind='BET'`.
  A segunda reversão → `REFERENCE_ALREADY_REVERSED`.
- ROLLBACK de um REFUND é permitido (referencia o REFUND, debita e a aposta volta a valer). Um novo REFUND da mesma
  BET continua bloqueado, por ser um segundo REFUND bem-sucedido da mesma referência. WIN e REFUND só aceitam
  ROLLBACK, então o primeiro índice já os cobre.
- Reversão que debitaria mais que o saldo → `INSUFFICIENT_FUNDS_FOR_REVERSAL`.

### Referência indisponível
| Situação | Resultado |
| --- | --- |
| Não existe | `PENDING_REFERENCE` + evento `WagerTransactionPendingReference`. O worker tenta com backoff exponencial (base 2s, ×2, teto 5min, máx. 10 tentativas, configurável). Esgotado → `REJECTED/REFERENCE_NOT_FOUND` + `WagerTransactionRejected` |
| Existe, mas `PENDING_REFERENCE` | continua esperando (conta tentativa) |
| Existe, mas `REJECTED`/`FAILED` | `REJECTED/REFERENCE_NOT_PROCESSED` |

**Worker de referências:** claim por `SELECT ... WHERE status='PENDING_REFERENCE' AND next_attempt_at <= now()
FOR UPDATE SKIP LOCKED LIMIT n`. Cada item é processado na sua própria transação, com o lock da carteira na ordem
padrão. Retoma após reinício, em qualquer instância.

## 6. Persistência (schema)

Dois papéis no Postgres: `wallet_owner` (migrations) e `wallet_app` (aplicação). `wallet_app` não tem
`UPDATE`/`DELETE`/`TRUNCATE` em `wallet_ledger_entries`. Além disso, triggers bloqueiam alterações mesmo para o owner.

- **wallets:** `id uuid PK`, `player_id uuid`, `currency char(3)`, `balance_minor bigint CHECK (>= 0)`,
  `version bigint CHECK (>= 1)`, `created_at`, `updated_at`, `UNIQUE (player_id, currency)`.
- **wager_transactions:** `id uuid PK`, `origin`, `kind`, `status` (CHECKs de domínio), `wallet_id uuid NOT NULL`
  (sem FK, para permitir auditar `WALLET_NOT_FOUND`), `player_id`, `amount_minor bigint CHECK (>= 0)`,
  `currency`, `provider_id`, `external_transaction_id`, `idempotency_key`, `payload_hash`, `round_id`, `game_id`,
  `reference_external_transaction_id`, `reference_transaction_id uuid FK NULL`, `reference_kind` (kind da
  referência resolvida, denormalizado para o índice de reversão), `failure_code`,
  `result_balance_minor`, `attempts`, `next_attempt_at`, `created_at`, `updated_at`, `processed_at`.
  - CHECK: `origin='INTERNAL'` ⇔ `kind='OPENING'` e campos externos NULL. `origin='EXTERNAL'` ⇒ campos externos NOT NULL.
  - `UNIQUE (provider_id, idempotency_key)`, `UNIQUE (provider_id, external_transaction_id)`.
  - `UNIQUE (wallet_id) WHERE kind='OPENING'` (impede crédito inicial duplicado).
  - Os dois índices únicos parciais de reversão (seção 5). Índice `(next_attempt_at) WHERE status='PENDING_REFERENCE'`.
  - Trigger: rejeita UPDATE de linhas em estado terminal e transições inválidas.
- **wallet_ledger_entries:** `id uuid (v7) PK`, `wallet_id FK`, `transaction_id FK`, `direction CHECK`,
  `amount_minor CHECK (> 0)`, `currency`, `balance_before_minor CHECK (>= 0)`, `balance_after_minor CHECK (>= 0)`,
  `created_at`, `CHECK (after = before ± amount por direção)`, `UNIQUE (wallet_id, transaction_id)`.
  Triggers `BEFORE UPDATE OR DELETE` e `BEFORE TRUNCATE` → exceção.
- **inbox_messages:** `PK (consumer_name, message_id)`, `payload_hash`, `received_at`, `processed_at`.
- **outbox_events:** `id uuid PK (= eventId)`, `aggregate_type`, `aggregate_id`, `event_type`, `payload jsonb`,
  `occurred_at`, `attempts`, `next_attempt_at`, `locked_by`, `locked_until`, `published_at`, `last_error`.
  Trigger impede alterar `payload`, `event_type`, `aggregate_id` e `occurred_at`. Índice parcial de pendentes.

**Transações entre repositórios:** `app.TxManager.WithinTx(ctx, fn)` abre a transação GORM e a guarda no `context`.
Os repositórios usam a transação do contexto quando existe. O caso de uso delimita a transação. Timeouts por sessão:
`SET LOCAL lock_timeout` e `statement_timeout`.

## 7. Concorrência

Lock pessimista por linha mais guarda de versão:

```
BEGIN
  INSERT wager_transactions ... ON CONFLICT DO NOTHING        -- idempotência (concorrentes bloqueiam no índice)
  SELECT ... FROM wallets WHERE id=$1 FOR UPDATE                -- lock da carteira
  SELECT referência ... (quando houver)                         -- ordem fixa: carteira → transações
  domínio: Debit/Credit no agregado reidratado
  UPDATE wallets SET balance_minor=$, version=version+1, updated_at=now() WHERE id=$1 AND version=$old
  INSERT ledger; INSERT outbox; (SQS: INSERT inbox)
COMMIT
```

Na prática, o fluxo idempotente é: (1) buscar por `(provider_id, idempotency_key)` ou `(provider_id, external_id)`.
Se encontrar, faz replay ou responde conflito. (2) Senão, executa o bloco acima. Uma corrida no INSERT cai no
`ON CONFLICT`, e então o processo relê e responde replay.
Lock timeout e version mismatch → erro transitório (503 / retry SQS), contado em `concurrency_conflicts_total`.
Sem locks globais: carteiras distintas avançam em paralelo.

## 8. Idempotência

- Hash: SHA-256 (hex) do JSON canônico (chaves ordenadas, sem espaços) de `providerId`, `externalTransactionId`,
  `playerId`, `walletId`, `roundId`, `gameId`, `kind`, `money.amount`, `money.currency` e
  `referenceExternalTransactionId` (omitido se ausente). Ficam fora a chave, `messageId`, `type`, `occurredAt` e
  headers. **Sem normalização:** money e UUIDs só são aceitos na forma canônica (UUID em minúsculas,
  `8-4-4-4-12`; outro formato → `INVALID_ID`), então o hash usa os valores exatamente como recebidos. HTTP e SQS
  montam o mesmo `Command`.
- A chave é escopada por provedor e usada exatamente como recebida (não vazia, ≤ 255 caracteres).
- Mesma chave e mesmo hash → resultado persistido, `idempotentReplay:true`, saldo de `result_balance_minor`.
  Mesma chave e hash diferente → 409 `IDEMPOTENCY_KEY_CONFLICT`. Mesmo external id com outra chave → 409
  `EXTERNAL_TRANSACTION_CONFLICT`.

## 9. Autenticação e autorização

- Keycloak, realm `wallet` importado automaticamente. Clientes confidenciais com `client_credentials`:
  `provider-a` e `provider-b` (scope `wagering`, hardcoded claim `provider_id`), `wallet-internal` (scopes
  `wallets wagering:read`) e `short-lived` (lifespan curto, para o teste de token expirado). Audience mapper
  `aud=wallet-api`.
- Middleware Gin: Bearer obrigatório. go-oidc verifica assinatura (JWKS com cache/rotação), `iss`, `aud`, `exp`
  e RS256. Gera um `Principal{ClientID, ProviderID, Scopes}`.

| Endpoint | Acesso |
| --- | --- |
| `POST /wagering/transactions` | scope `wagering`; o `providerId` do body deve ser igual ao do token (403, sem efeito) |
| `GET /providers/:providerId/wagering/transactions/:extId` | scope `wagering`, `:providerId` igual ao do token |
| `GET /wagering/transactions/:id` | provedor: só as próprias; interno (`wagering:read`): todas |
| `POST /wallets`, `GET /wallets/:id`, `GET /wallets/:id/ledger`, `POST /wallets/:id/reconciliation` | scope `wallets` |
| `/health/live`, `/health/ready`, `/metrics` | públicos |

- Respostas: 401 para token ausente, inválido ou expirado. 403 para scope errado. **404 para transação de outro
  provedor** (não revela existência). O replay é escopado por provedor.
- **SQS — a identidade do broker determina o `providerId`:**
  - Cada provedor tem sua própria conta AWS no MiniStack (`provider-a` → `111111111111`, `provider-b` →
    `222222222222`) e usa a credencial dela.
  - A queue policy de `wager-transactions.fifo` permite `SendMessage` somente a essas contas. Qualquer outra conta
    recebe `AccessDenied`.
  - O consumidor lê o atributo `SenderId` e o converte em `providerId` pelo mapa configurável
    `SQS_SENDER_PROVIDER_MAP`. Se `data.providerId` não bater, a mensagem vai para a DLQ com
    `PROVIDER_IDENTITY_MISMATCH`, sem efeito financeiro.
  - Na AWS real, o mesmo mapa usaria o user id do IAM.
- Usuários IAM da conta dona (`000000000000`), cada um com privilégio mínimo:
  - `wallet-consumer`: recebe, apaga, muda visibilidade e envia para a DLQ de entrada.
  - `wallet-publisher`: envia para `wallet-events.fifo`.
  - O consumidor executa todas as validações de domínio.
- Limitação documentada: os provedores usam credencial root da própria conta, porque o MiniStack nega usuário IAM
  em envio cross-account (bug verificado no spike).

## 10. Contrato HTTP

| Status | Situação | Corpo |
| --- | --- | --- |
| 200 | PROCESSED (novo ou replay) | `{transactionId, status, balance, idempotentReplay}` |
| 202 | PENDING_REFERENCE | `{transactionId, status, idempotentReplay}` |
| 400 | entrada inválida | `{"error":{"failureCode","category":"CORRECTABLE","message","details":[{field,reason}]}}` |
| 401/403/404 | auth / scope ou providerId divergente / inexistente ou de outro provedor | `{"error":{failureCode,message}}` |
| 409 | conflitos de idempotência; `WALLET_ALREADY_EXISTS` | `{"error":{failureCode,message}}` |
| 422 | REJECTED (novo ou replay) | `{transactionId, status:"REJECTED", failureCode, category:"DEFINITIVE", idempotentReplay}` |
| 500 | FAILED (novo ou replay) | `{transactionId, status:"FAILED", failureCode:"INFRASTRUCTURE_FAILURE", category:"DEFINITIVE", idempotentReplay}` |
| 503 | Postgres indisponível, lock timeout | `{"error":{"failureCode":"TEMPORARILY_UNAVAILABLE"}}` + `Retry-After` |

- `POST /wallets`: cria a carteira (version 1). Com saldo > 0 cria, no mesmo commit, `OPENING/PROCESSED`, o
  crédito no ledger e a outbox `WagerTransactionProcessed` + `WalletBalanceChanged`. Saldo 0 → nada disso.
  Duplicada → 409.
- `GET /wallets/:id/ledger?cursor&limit` (padrão 50, máx. 200): ordenado por `id` (UUIDv7). O cursor é opaco
  (base64url do último id). Resposta `{items, nextCursor}`.
- `GET /wagering/transactions/:id` e a consulta por provedor: status, failureCode, attempts e nextAttemptAt.
- `POST /wallets/:id/reconciliation`: transação `REPEATABLE READ READ ONLY`. Lê a carteira e
  `SUM(credit) − SUM(debit)` e `COUNT` do ledger. `difference = stored − calculated`. Divergência → log WARN e
  `reconciliation_divergences_total`. Não altera nada.
- `GET /health/live` sempre 200. `GET /health/ready` faz `SELECT 1` e `GetQueueAttributes` com timeout
  (503 se falhar).

## 11. Consumidor SQS e inbox

- Filas: `wager-transactions.fifo` (VisibilityTimeout 30s, redrive `maxReceiveCount=5` →
  `wager-transactions-dlq.fifo`) e `wallet-events.fifo` → `wallet-events-dlq.fifo`.
- Contrato do produtor: `MessageGroupId = walletId`, `MessageDeduplicationId = messageId`.
- N goroutines por instância (padrão 4), long polling de 20s, até 10 mensagens.
- Uma transação SQL contém: `INSERT inbox ... ON CONFLICT DO NOTHING`, o caso de uso compartilhado com o HTTP e o
  commit. **`DeleteMessage` só depois do commit.**

| Resultado | Ação |
| --- | --- |
| PROCESSED, REJECTED ou PENDING_REFERENCE commitado | delete |
| inbox já tem o messageId com o mesmo hash | delete + `duplicates_total` |
| transitório | sem delete; `ChangeMessageVisibility` com backoff exponencial por `ApproximateReceiveCount`; esgotado → redrive para a DLQ |
| entrada inválida (códigos `CORRECTABLE`), `PROVIDER_IDENTITY_MISMATCH`, `MESSAGE_ID_REUSED` | `SendMessage` para a DLQ com atributo `failureReason` + delete + `dlq_total` |
| falha permanente de infra | `FAILED` gravado em transação separada + DLQ + delete |

- Cruzamento HTTP↔SQS: a inbox é nova, mas a idempotência do domínio responde replay sem reaplicar nada.
- Shutdown: para o polling, espera o trabalho em andamento até `SHUTDOWN_TIMEOUT` (20s). Se estourar, cancela o
  contexto (rollback) e faz `ChangeMessageVisibility(0)`.

## 12. Outbox e eventos

- Claim com lease: `UPDATE outbox_events SET locked_by=$me, locked_until=now()+$lease, attempts=attempts+1
  WHERE id IN (SELECT id ... WHERE published_at IS NULL AND next_attempt_at<=now() AND (locked_until IS NULL OR
  locked_until<now()) ORDER BY occurred_at FOR UPDATE SKIP LOCKED LIMIT 50) RETURNING *`.
- Publica fora da transação em `wallet-events.fifo` (`MessageGroupId=aggregateId`, `MessageDeduplicationId=eventId`)
  e marca `published_at WHERE id=$ AND locked_by=$me`. Se falhar: `next_attempt_at` com backoff com teto e
  `last_error`. Nunca descarta eventos.
- Se cair entre publicar e marcar: o lease expira e outra instância republica com o mesmo `eventId`.
- Limitação documentada: com vários publishers, a ordem por carteira não é garantida. O consumidor usa
  `walletVersion`.
- Envelope: `eventId` (v7), `eventType`, `aggregateId` (walletId), `correlationId`, `causationId?`
  (messageId ou transactionId de origem), `occurredAt` (RFC 3339 UTC), `version` (=1, definido no construtor) e
  `data` tipado.
  - `WagerTransactionProcessed{transactionId, walletId, playerId, origin, kind, money, providerId?, externalTransactionId?, roundId?, gameId?, referenceTransactionId?, balanceAfter}`
  - `WagerTransactionRejected{transactionId, walletId, providerId, externalTransactionId, kind, money, failureCode}`
  - `WalletBalanceChanged{walletId, transactionId, direction, money, balanceBefore, balanceAfter, walletVersion}`
  - `WagerTransactionPendingReference{transactionId, walletId, providerId, externalTransactionId, kind, referenceExternalTransactionId, attempts, nextAttemptAt}`

## 13. Fx e ciclo de vida

- Um `fx.Module` por área: config, logger, metrics, postgres, sqs, auth, app, httpapi, sqsconsumer, outbox,
  refworker. Injeção por construtores.
- Config validada no construtor (falha antes do start). Postgres e SQS verificam conectividade no `OnStart`.
- O Fx para na ordem inversa: o HTTP para de aceitar conexões e faz `Shutdown(ctx)` com drenagem; consumer e
  workers cancelam e aguardam `Done()` com prazo; só depois fecham as conexões com Postgres e SQS.
- Toggles por env (`HTTP_ENABLED`, `CONSUMER_ENABLED`, `OUTBOX_ENABLED`, `REFWORKER_ENABLED`), usados pelos testes.

## 14. Observabilidade

- slog JSON com `correlationId` (header `X-Correlation-Id` ou gerado; no SQS vem do envelope ou do messageId),
  `messageId`, `transactionId`, `walletId` e `providerId`. Sem tokens nem payload financeiro completo.
- Métricas: `wager_transactions_total{kind,status,channel}`, `idempotent_replays_total{channel}`,
  `inbox_duplicates_total`, `sqs_retries_total`, `sqs_dlq_total{reason}`, `concurrency_conflicts_total{type}`,
  `outbox_lag_seconds`, `outbox_publish_attempts_total{result}`, `processing_duration_seconds{channel}`,
  `reconciliation_divergences_total`, `reference_retries_total` e métricas HTTP.
- Prometheus (scrape via `dns_sd_configs` do serviço `wallet`) e Grafana com datasource e dashboard provisionados.

## 15. Ambiente (Docker Compose)

- Dev: `postgres`, `keycloak` (`--import-realm`), `ministack` (init cria filas, DLQs, usuários IAM, queue policy
  das contas de provedores e políticas),
  `migrate` (one-shot, golang-migrate), `wallet` (`deploy.replicas: 3`), `nginx` (LB em `:8000`), `prometheus` e
  `grafana`.
- Profile `test`: `postgres_test :5433`, `keycloak_test :8081`, `ministack_test :4567` (com o mesmo provisionamento),
  `migrate_test`.
- `docker compose up --build` sobe tudo. Os containers do projeto vivem sempre no compose.

## 16. Testes

| Comando | Roda | Infra |
| --- | --- | --- |
| `go test ./...` / `go test -race ./...` | **tudo** (unit + integração + E2E) | `docker compose --profile test up -d` |
| `go test -race -tags=unit ./...` | só unitários | nenhuma |
| `go test -race -tags=integration ./...` | só integração | profile test |
| `go test -race -tags=e2e ./...` | só E2E multi-instância e falhas | profile test |

- As tags funcionam como *filtros de exclusão*:
  - unit: `//go:build !integration && !e2e`
  - integration: `//go:build !unit && !e2e`
  - e2e: `//go:build !unit && !integration`

  Sem tag, roda tudo. Com uma tag, só aquela camada.
- Testes que precisam de infra **falham com mensagem clara** ("suba `docker compose --profile test up -d`") se ela
  não responder. Nunca são pulados em silêncio.
- A tag `faultinject` é interna: o `TestMain` do E2E compila o binário com ela, e o usuário não precisa passá-la.

- **Unitários:** Money (parsing, escala, limites, overflow, inválidos, moedas incompatíveis), invariantes da Wallet,
  LedgerEntry, transições, regras dos 5 tipos, política de zero, OPENING (metadados e eventos), hash canônico e
  conflito de payload.
- **Integração:** migrations up/down/up; constraints (saldo negativo, unicidades, OPENING duplicado, reversão
  duplicada); imutabilidade do ledger (UPDATE/DELETE/TRUNCATE falham); triggers de estado terminal; atomicidade
  (falha no meio → nada persistido); repositórios; handlers com tokens reais do Keycloak (ausente, inválido,
  expirado, scope errado, isolamento entre provedores em consultas e replays, nenhum efeito em acesso negado);
  inbox e reentrega; outbox com dois publishers concorrentes; retry e DLQ; composição Fx (`fx.ValidateApp`,
  start/stop, workers encerrados). Isolamento: UUIDs novos por teste e filas SQS próprias por teste.
- **E2E:** o `TestMain` compila o binário (com `-tags faultinject`) e sobe 3 processos do SO. Cenários da seção 13:
  (1) 50 BETs iguais em paralelo → 1 débito; (2) 80+80 sobre 100 → 1 PROCESSED, 1 `INSUFFICIENT_FUNDS`, saldo 20,
  1 débito, reenvios inalterados; (3) carteiras distintas em paralelo; (4) cenários sobre as 3 instâncias;
  (5) `FAULT=crash_after_commit_before_delete` → reentrega sem duplicar; (6) dois publishers +
  `FAULT=crash_after_publish_before_mark` → republicação com o mesmo eventId; (7) REFUND/ROLLBACK antes da
  referência → resolução, e outro caso → expiração `REFERENCE_NOT_FOUND`; (8) reinício preserva idempotência,
  pendências e consistência. Cruzamento HTTP↔SQS. Ao final de cada cenário: `saldo == Σcrédito − Σdébito` via
  reconciliação.

## 17. Documentação (entrega)

- `README.md` (substitui o enunciado, que vai para `docs/CHALLENGE.md`): pré-requisitos, variáveis de ambiente,
  filas, migrations (up/down), execução, exemplos `curl` com obtenção de token, comandos de teste, preparo da infra
  de teste, multi-instância e simulações de falha (build tags).
- `ARCHITECTURE.md`: dinheiro, transações, idempotência, locks, referências pendentes, reversões, inbox/outbox,
  authn/authz, Fx, shutdown, failure codes, máquina de estados, contratos de eventos e roteamento, limitações
  (MiniStack: assinatura não verificada, SenderId = conta, IAM cross-account só com root; ordem da outbox),
  tabela de failure codes com a category, e as interpretações adotadas.
- `.env.example` sem segredos reais. Código formatado com `gofmt`; `go vet ./...` limpo.
