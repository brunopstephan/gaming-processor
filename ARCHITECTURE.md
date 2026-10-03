# Arquitetura

Este documento descreve as decisões do serviço e o porquê de cada uma. O enunciado está em
[docs/CHALLENGE.md](docs/CHALLENGE.md) e o design original em
[docs/superpowers/specs/2026-10-01-wallet-service-design.md](docs/superpowers/specs/2026-10-01-wallet-service-design.md).
Para subir e usar o serviço, veja o [README.md](README.md).

## 1. Visão e pacotes

Um único binário (`cmd/wallet`) roda quatro componentes, ligados ou desligados por variáveis de ambiente: a API HTTP,
o consumidor SQS, o relay da outbox e o worker de referências pendentes. O estado fica todo no PostgreSQL, então
qualquer número de instâncias pode rodar ao mesmo tempo.

| Pacote | Responsabilidade |
| --- | --- |
| `internal/domain/money` | `Money`, moedas ISO 4217, parsing canônico e aritmética com checagem de overflow |
| `internal/domain/wallet` | agregado `Wallet` e `LedgerEntry` |
| `internal/domain/wagering` | `WagerTransaction`, tipos, estados, regras de negócio, `FailureCode`, hash canônico, política de retry |
| `internal/domain/events` | envelope e os 4 eventos de integração |
| `internal/app` | casos de uso (carteira, operações, consultas, reconciliação, inbox, worker de referências, relay da outbox) e as portas (repositórios, `TxManager`, `Clock`, `Metrics`) |
| `internal/adapters/postgres` | modelos GORM, repositórios, `TxManager`, mapeamento de erros |
| `internal/adapters/httpapi` | Gin: rotas, DTOs, middlewares (correlação, log, métricas, autenticação) e o contrato de erros |
| `internal/adapters/auth` | verificação de tokens com go-oidc e `Principal` |
| `internal/adapters/sqsconsumer`, `awssqs`, `workers` | consumidor da fila de entrada, clientes e publisher SQS, laços de fundo |
| `internal/platform` | config, logging, metrics, health, background, composition (Fx), faultinject |

Regras de dependência:

- O domínio importa só a biblioteca padrão e `uuid`: sem Fx, Gin, GORM nem AWS. Os modelos de persistência são structs
  separadas, convertidas explicitamente em entidades.
- `internal/app` importa apenas a biblioteca padrão, o domínio e `internal/platform/faultinject`. Nada de AWS, Gin,
  go-oidc, Prometheus, GORM nem adapters: tudo o que é externo entra por interface.
- Os adapters implementam as portas de `app`. Só `internal/platform/composition` conhece todos eles e monta o grafo
  do Fx.

## 2. Dinheiro

- `Money` guarda `int64` em unidades menores (centavos) e a moeda. Nunca há `float`. Os campos são privados e o valor é
  imutável. O zero value é inválido e toda operação sobre ele falha (`ErrUninitialized`); `Equal(Money{}, Money{})` é
  verdadeiro.
- A escala é fixa em 2. Por isso só entram moedas ISO 4217 com expoente 2 (tabela embutida em
  `internal/domain/money/iso4217.go`). JPY (0), BHD (3) e códigos inexistentes são rejeitados com
  `UNSUPPORTED_CURRENCY`.
- A entrada externa aceita **só** a forma canônica `^(0|[1-9]\d*)\.\d{2}$`. Ficam de fora vazio, `25`, `25.0`,
  `025.00`, `+25.00`, `-1.00`, `1e3`, `NaN`, `Infinity`, espaços e `25.001` (`INVALID_MONEY`). Um número JSON no lugar
  da string também é `INVALID_MONEY`. Como só há uma forma válida, não existe normalização antes do hash de
  idempotência.
- Overflow é checado no parsing, em `Add`, `Sub` e `Negate` (inclusive `MinInt64`). Faixa:
  ±92.233.720.368.547.758,07.
- Valores negativos só existem em cálculos internos (por exemplo, `difference` da reconciliação).
- No banco, cada `Money` vira duas colunas: `*_minor bigint` e `currency char(3)`.

## 3. Persistência e transações

- GORM sobre pgx, com SQL explícito nos caminhos críticos (lock, claim, `ON CONFLICT`, `UPDATE` com guarda de
  versão). Não há AutoMigrate: o schema vem de `migrations/` (golang-migrate).
- `TxManager.WithinTx` abre uma transação READ COMMITTED e a guarda no `context`. Os repositórios usam a transação do
  contexto, se houver. Um `WithinTx` aninhado entra na transação externa, sem savepoints: um erro engolido por dentro
  ainda aborta a externa. Trabalho que precisa commitar sozinho (a gravação `FAILED`) começa de um contexto sem
  transação.
- Cada transação executa `set_config('lock_timeout', ..., true)` e `set_config('statement_timeout', ..., true)`, locais
  à transação (`DB_LOCK_TIMEOUT`, padrão 5s; `DB_STATEMENT_TIMEOUT`, padrão 10s).
- A reconciliação usa `WithinSnapshot`: transação `REPEATABLE READ READ ONLY`, para que o saldo e a soma do ledger
  venham do mesmo instante. Ela nunca entra em outra transação.
- Dois papéis: `wallet_owner` (dono do schema, usado pelas migrations) e `wallet_app` (a aplicação). `wallet_app` tem
  `SELECT, INSERT` no ledger e na inbox, e `SELECT, INSERT, UPDATE` em `wallets`, `wager_transactions` e
  `outbox_events`. Ninguém ganha `DELETE`.
- O ledger (`wallet_ledger_entries`) é append-only: triggers `BEFORE UPDATE OR DELETE` e `BEFORE TRUNCATE` lançam
  exceção, inclusive para o dono.
- Constraints que garantem as invariantes independentemente do código:

| Invariante | Mecanismo |
| --- | --- |
| saldo nunca negativo, versão >= 1 | `CHECK` em `wallets` |
| uma carteira por (jogador, moeda) | `UNIQUE (player_id, currency)` |
| idempotência por provedor | `UNIQUE (provider_id, idempotency_key)` e `UNIQUE (provider_id, external_transaction_id)` |
| um único crédito inicial por carteira | índice único parcial `(wallet_id) WHERE kind='OPENING'` |
| uma referência não recebe duas reversões do mesmo tipo | índice único parcial `(reference_transaction_id, kind) WHERE status='PROCESSED' AND kind IN ('REFUND','ROLLBACK')` |
| uma BET recebe uma única reversão (REFUND ou ROLLBACK) | índice único parcial `(reference_transaction_id) WHERE status='PROCESSED' AND kind IN ('REFUND','ROLLBACK') AND reference_kind='BET'` |
| referência composta consistente | FK `(reference_transaction_id, reference_kind)` para `(id, kind)`, `MATCH FULL` |
| só códigos de rejeição de negócio em `REJECTED`; `FAILED` só com `INFRASTRUCTURE_FAILURE` | `CHECK` em `wager_transactions` |
| formato por origem e por status, valor por tipo (`LOSS` = 0, demais > 0) | `CHECK` em `wager_transactions` |
| máquina de estados e colunas de identidade imutáveis | trigger `BEFORE UPDATE` em `wager_transactions` |
| a entrada do ledger pertence à carteira da sua transação | FK `(transaction_id, wallet_id)` para `(id, wallet_id)` |
| `depois = antes ± valor` por direção, valor > 0 | `CHECK` no ledger |
| uma entrada de ledger por (carteira, transação) | `UNIQUE (wallet_id, transaction_id)` |
| snapshot de evento imutável | trigger em `outbox_events` (só a contabilidade de entrega muda) |

`wager_transactions.wallet_id` não tem FK de propósito: uma rejeição `WALLET_NOT_FOUND` precisa guardar o id pedido
para auditoria.

## 4. Concorrência e locks

Lock pessimista por linha da carteira, mais uma guarda de versão:

```
BEGIN
  INSERT wager_transactions (PENDING) ... ON CONFLICT DO NOTHING   -- idempotência; concorrentes esperam no índice
  SELECT ... FROM wallets WHERE id = $1 FOR UPDATE                  -- lock da carteira
  SELECT da referência (se houver)                                  -- depois do lock da carteira
  domínio: Debit/Credit no agregado reidratado
  UPDATE wallets SET balance_minor, version = version + 1 WHERE id = $1 AND version = $anterior
  UPDATE wager_transactions; INSERT ledger; INSERT outbox (+ INSERT inbox, no SQS)
COMMIT
```

- **Ordem fixa de locks:** linha da transação, depois a carteira, depois a leitura das referências. O worker de
  referências segue a mesma ordem: trava a linha pendente (`FOR NO KEY UPDATE SKIP LOCKED`), depois a carteira.
- Carteiras diferentes avançam em paralelo; não há lock global.
- A guarda `version = $anterior` é uma segunda defesa: se algo escrever na carteira sem o lock, o `UPDATE` não casa e
  o erro é `ErrVersionConflict`.
- **Erros transitórios** (`ErrTransient`): perda de conexão, `lock_timeout` (`55P03`), `statement_timeout` (`57014`),
  `40001`, `40P01`, desligamento do servidor, `ErrVersionConflict` e cancelamento ou prazo do contexto. Viram **503**
  com `Retry-After: 1` no HTTP e retry com backoff no SQS, e nunca gravam `FAILED`. São contados em
  `concurrency_conflicts_total{type}` com `type` em `lock_timeout`, `version`, `unique` e `transient`.
- Uma violação de unicidade inesperada durante o processamento também é tratada como transitória (`unique`): a nova
  tentativa relê o estado do vencedor.

## 5. Idempotência

- **Hash canônico** (SHA-256 em hex) do JSON com chaves ordenadas e sem espaços, com os campos `providerId`,
  `externalTransactionId`, `playerId`, `walletId`, `roundId`, `gameId`, `kind`, `money.amount`, `money.currency` e
  `referenceExternalTransactionId` (omitido se ausente). Ficam de fora a chave de idempotência, `messageId`, `type`,
  `occurredAt` e os headers. Não há normalização: UUIDs só são aceitos em minúsculas na forma `8-4-4-4-12` (senão
  `INVALID_ID`) e o dinheiro só na forma canônica, então o hash usa os valores exatamente como chegaram.
- **Chave escopada por provedor**, usada exatamente como recebida (não vazia, até 255 bytes, sem caracteres de
  controle). No HTTP vem do header `Idempotency-Key`; no SQS, de `data.idempotencyKey`. Os dois canais montam o mesmo
  `Command`.
- **Replay:** mesma chave e mesmo hash devolvem o resultado persistido, com `idempotentReplay: true` e o saldo
  gravado em `result_balance_minor` (o saldo daquele momento, não o atual).
- **Os dois 409:** mesma chave com hash diferente é `IDEMPOTENCY_KEY_CONFLICT`; mesmo `externalTransactionId` com
  outra chave é `EXTERNAL_TRANSACTION_CONFLICT`. (`WALLET_ALREADY_EXISTS` é outro 409, do `POST /wallets`.)
- **Corrida:** a primeira coisa na transação é o `INSERT ... ON CONFLICT DO NOTHING`. Quem perde espera o commit do
  vencedor no índice único, relê pelas duas chaves e responde como replay (ou conflito).
- **Cruzamento HTTP e SQS:** a inbox é por mensagem, mas a idempotência do domínio é por operação. Uma operação
  recebida por HTTP e depois por SQS (ou o contrário) com a mesma chave é respondida como replay, sem reaplicar nada
  (o E2E `TestSameOperationOverHTTPAndSQS` confere).

## 6. Operações, referências pendentes e reversões

| Tipo | Movimento | Referência |
| --- | --- | --- |
| `BET` | débito; exige saldo | proibida (`REFERENCE_NOT_ALLOWED`) |
| `WIN` | crédito | opcional: uma BET `PROCESSED` da mesma rodada (o valor pode diferir) |
| `LOSS` | nenhum movimento: sem ledger e sem incremento de versão; emite só `WagerTransactionProcessed`; valor exatamente `0.00` | proibida |
| `REFUND` | crédito do valor da BET | obrigatória: BET `PROCESSED` |
| `ROLLBACK` | inverso do original: BET vira crédito; WIN e REFUND viram débito | obrigatória: BET, WIN ou REFUND `PROCESSED` |

`BET`, `WIN`, `REFUND` e `ROLLBACK` exigem valor > 0. `OPENING` é interno (só pelo `POST /wallets` com saldo > 0) e
rejeitado como `KIND_NOT_ALLOWED` se vier por HTTP ou SQS.

- A referência é resolvida por `(providerId, referenceExternalTransactionId)`, depois do lock da carteira, e só dentro
  do mesmo provedor. A operação e a referência precisam concordar em jogador, carteira, moeda e rodada
  (`REFERENCE_MISMATCH`). Nas reversões o valor deve ser igual (`AMOUNT_MISMATCH`).
- **Reversão única:** os dois índices parciais da seção 3 garantem que uma referência não recebe duas reversões bem
  sucedidas do mesmo tipo e que uma BET recebe uma única, seja REFUND ou ROLLBACK (senão o mesmo débito voltaria duas
  vezes). A segunda é `REFERENCE_ALREADY_REVERSED`. Um ROLLBACK de REFUND é permitido (a aposta volta a valer); um novo
  REFUND da mesma BET continua bloqueado. Reversão que debitaria mais que o saldo é
  `INSUFFICIENT_FUNDS_FOR_REVERSAL`.

**Referência indisponível:**

| Situação | Resultado |
| --- | --- |
| a referência não existe | `PENDING_REFERENCE` (HTTP 202), com o evento `WagerTransactionPendingReference` |
| existe, mas está `PENDING_REFERENCE` | continua esperando (conta uma tentativa) |
| existe, mas está `REJECTED` ou `FAILED` | `REJECTED` com `REFERENCE_NOT_PROCESSED` |
| as tentativas se esgotam sem a referência | `REJECTED` com `REFERENCE_NOT_FOUND` (ou `REFERENCE_NOT_PROCESSED`, se ela existia mas seguia pendente) |

**Worker de referências** (`REFWORKER_ENABLED`):

- Cada execução reivindica **uma** pendência: `SELECT ... WHERE status='PENDING_REFERENCE' AND next_attempt_at <= now
  ORDER BY next_attempt_at, id ... FOR NO KEY UPDATE SKIP LOCKED`, na própria transação, com o lock da carteira na
  ordem padrão. Rodando em qualquer instância, nenhuma pega a mesma linha. Se o resultado for "ainda pendente", ele
  agenda a próxima tentativa na mesma linha. Reinícios não perdem nada: o estado só vive no banco.
- **Backoff:** base 2s, dobrando, teto de 5min, máximo de 10 tentativas
  (`REFERENCE_RETRY_BASE_DELAY`, `REFERENCE_RETRY_MAX_DELAY`, `REFERENCE_RETRY_MAX_ATTEMPTS`). A primeira tentativa,
  síncrona, conta como a número 1: com o padrão 10, há 9 retentativas do worker.
- **Bloqueio na cabeça da fila:** o worker pega sempre a pendência mais antiga que está vencida. Se ela falhar só com
  erro transitório (a transação dá rollback e `next_attempt_at` não muda), ela continua sendo a próxima, e as
  seguintes esperam até ela avançar. Um erro permanente a marca `FAILED` e libera a fila.
- Um erro permanente do worker (não retentável) faz `PENDING_REFERENCE -> FAILED` na própria linha; um erro transitório
  nunca grava `FAILED`.

## 7. Inbox e consumidor SQS

- **Inbox transacional:** o consumidor chama `IntakeService.Handle`, que passa o `INSERT` da inbox como `alongside` do
  `WageringService.Process`. A inbox entra na **mesma** transação que o efeito (transação, saldo, ledger e outbox).
  Ou tudo commita, ou nada. O `DeleteMessage` só acontece depois do commit.
- **Inbox escopada por provedor** (interpretação adotada): `consumer_name = "wager-transactions/<providerId>"`, então um
  `messageId` só é único dentro do provedor. O `providerId` mapeado tem no máximo 64 bytes (a coluna é `varchar(100)`),
  validado na configuração.
- A inbox guarda o hash do conteúdo (hash do payload mais a chave de idempotência). Mesmo `messageId` com outro
  conteúdo é `MESSAGE_ID_REUSED`.

**Disposições de cada mensagem:**

| Resultado | Ação |
| --- | --- |
| `PROCESSED`, `REJECTED` ou `PENDING_REFERENCE` commitado (inclusive replay de uma operação vinda por HTTP) | delete |
| `messageId` já na inbox com o mesmo conteúdo | delete e `inbox_duplicates_total` |
| a mesma redelivery, mas a transação gravada está `FAILED` (o envio à DLQ não terminou antes) | DLQ com `INFRASTRUCTURE_FAILURE` e delete |
| erro transitório (inclusive pânico no tratamento) | sem delete; `ChangeMessageVisibility` com backoff por `ApproximateReceiveCount`; `sqs_retries_total`. Esgotadas as 5 recepções, o próprio SQS faz o redrive para a DLQ |
| entrada inválida (códigos `CORRECTABLE`), remetente desconhecido ou `providerId` diferente do remetente (`PROVIDER_IDENTITY_MISMATCH`), `MESSAGE_ID_REUSED`, `IDEMPOTENCY_KEY_CONFLICT`, `EXTERNAL_TRANSACTION_CONFLICT` | `SendMessage` para a DLQ de entrada com `failureReason` e delete; `sqs_dlq_total{reason}` |
| falha permanente de infraestrutura | `FAILED` gravado em transação separada (junto com a inbox) e DLQ com `INFRASTRUCTURE_FAILURE` |

- **DLQ:** a cópia leva o mesmo corpo, os atributos `failureReason` e `senderId`, o mesmo `MessageGroupId` e
  `MessageDeduplicationId` = id da mensagem no SQS (assim, repetir o envio à DLQ dentro de 5 minutos é deduplicado).
  Se o envio à DLQ falhar, a mensagem volta para retry. Se o delete falhar depois dele, a reentrega será enviada de
  novo à DLQ e deduplicada.
- **Backoff:** `SQS_RETRY_BASE_DELAY` x 2^(n-1) no n-ésimo recebimento, com teto `SQS_RETRY_MAX_DELAY` (2s, 4s, 8s, 16s,
  32s, ... até 5min). Com `maxReceiveCount=5`, uma queda do PostgreSQL de cerca de 60s (2+4+8+16+32) é tolerada antes do
  redrive para a DLQ. A base mínima é 1s porque a visibilidade é em segundos inteiros.
- **Ordem por carteira dentro do lote:** o FIFO já entrega um grupo (`MessageGroupId` = `walletId`) em ordem. Dentro de
  um mesmo recebimento, quando uma mensagem de um grupo fica para retry, as seguintes do mesmo grupo no lote são
  liberadas (`ChangeMessageVisibility(0)`) sem processar, para não ultrapassá-la.
- **Concorrência:** `SQS_CONSUMER_WORKERS` goroutines por instância (padrão 4), long polling de `SQS_WAIT_TIME` (20s) e
  até `SQS_MAX_MESSAGES` (10) mensagens por recebimento. Cada goroutine trata o lote uma mensagem por vez.
- **Shutdown:** o polling para; o trabalho em andamento tem até `SQS_SHUTDOWN_TIMEOUT` (20s). Se estourar, o contexto é
  cancelado (rollback) e as mensagens são liberadas com visibilidade 0, com mais `AbortGrace` (12s) para os workers
  terminarem. Uma mensagem já commitada ainda é apagada, mesmo com o contexto cancelado (o settle usa um contexto
  próprio de 5s).

## 8. Outbox e eventos

- **Escrita:** os eventos são gravados na mesma transação da mudança de estado, na tabela `outbox_events`.
- **Claim com lease:** um único `UPDATE ... WHERE id IN (SELECT ... FOR UPDATE SKIP LOCKED)` pega até
  `OUTBOX_BATCH_SIZE` (50) eventos não publicados, vencidos e sem lease ativo, grava `locked_by` e
  `locked_until = now() + lease` e soma uma tentativa. Todos os tempos vêm do relógio do **banco**, então o relógio
  das instâncias não importa.
- **Publicação fora da transação** em `wallet-events.fifo` (`MessageGroupId` = `aggregateId`, `MessageDeduplicationId`
  = `eventId`, atributo `eventType`), com timeout de `OUTBOX_LEASE`/2, para que uma chamada travada não passe do lease.
  Em seguida `UPDATE ... SET published_at WHERE id = $1 AND locked_by = $me`: o **fencing** por `locked_by` impede que
  quem perdeu o lease marque o evento. Falha de publicação: `next_attempt_at` com backoff (`OUTBOX_RETRY_BASE_DELAY`
  1s até `OUTBOX_RETRY_MAX_DELAY` 5min) e `last_error`. Eventos nunca são descartados.
- **Queda entre publicar e marcar:** o lease expira e outra instância republica **com o mesmo `eventId`**
  (`FAULT=crash_after_publish_before_mark`, cenário E2E 6).
- **Ordem:** o claim ordena por `(occurred_at, id)`, mas com vários publishers a ordem por carteira **não é garantida**.
  O consumidor deve ordenar por `walletVersion` (presente em `WalletBalanceChanged`).
- **Duplicatas:** a deduplicação do SQS FIFO só vale por 5 minutos. Depois disso, ou se o relay republicar em outro
  momento, o mesmo evento pode chegar duas vezes. O consumidor deve deduplicar por `eventId`.
- `outbox_lag_seconds` só observa publicações bem sucedidas (tempo entre `occurredAt` e a publicação). Falhas aparecem
  em `outbox_publish_attempts_total{result="failed"}`.

**Envelope** (todos os eventos): `eventId` (UUIDv7), `eventType`, `aggregateId` (o `walletId`), `correlationId`,
`causationId` (opcional: `messageId` ou `transactionId` de origem), `occurredAt` (RFC 3339 UTC, milissegundos),
`version` (1) e `data`.

| Evento | Campos de `data` |
| --- | --- |
| `WagerTransactionProcessed` | `transactionId`, `walletId`, `playerId`, `origin`, `kind`, `money`, `providerId?`, `externalTransactionId?`, `roundId?`, `gameId?`, `referenceTransactionId?`, `balanceAfter` |
| `WagerTransactionRejected` | `transactionId`, `walletId`, `providerId`, `externalTransactionId`, `kind`, `money`, `failureCode` |
| `WalletBalanceChanged` | `walletId`, `transactionId`, `direction`, `money`, `balanceBefore`, `balanceAfter`, `walletVersion` |
| `WagerTransactionPendingReference` | `transactionId`, `walletId`, `providerId`, `externalTransactionId`, `kind`, `referenceExternalTransactionId`, `attempts`, `nextAttemptAt` |

`WagerTransactionProcessed` também sai para `LOSS` e para a abertura (`OPENING`, sem os campos externos);
`WalletBalanceChanged` só quando o saldo muda. A fila `wallet-events.fifo` tem redrive para `wallet-events-dlq.fifo`.

## 9. Autenticação e autorização

- **Keycloak** (realm `wallet`, importado) e **go-oidc**: o middleware exige `Authorization: Bearer`, verifica a
  assinatura RS256 contra o JWKS (cache e rotação; busca com timeout de 5s), o `iss` (`OIDC_ISSUER_URL`), o `aud`
  (`OIDC_AUDIENCE`, padrão `wallet-api`) e o `exp`. Daí sai um `Principal{ClientID, ProviderID, Scopes}` (o
  `provider_id` é um claim fixo por cliente). O issuer é o endereço público do Keycloak, e o JWKS pode ser buscado em
  outro host (`OIDC_JWKS_URL`, o interno no compose).
- **Matriz de acesso:**

| Endpoint | Acesso |
| --- | --- |
| `POST /wagering/transactions` | scope `wagering`; o `providerId` do corpo deve ser o do token (403, sem efeito) |
| `GET /providers/:providerId/wagering/transactions/:externalTransactionId` | scope `wagering`; `:providerId` igual ao do token (403 caso contrário) |
| `GET /wagering/transactions/:transactionId` | scope `wagering` (só as próprias) ou `wagering:read` (todas) |
| `POST /wallets`, `GET /wallets/:walletId`, `GET /wallets/:walletId/ledger`, `POST /wallets/:walletId/reconciliation` | scope `wallets` |
| `/health/live`, `/health/ready`, `/metrics` | públicos |

- **401** para token ausente, inválido, expirado ou sem a audience; **403** para scope errado ou `providerId`
  divergente; **404** para transação de outro provedor (não revela a existência). O replay também é escopado por
  provedor.
- **Queda do IdP:** o JWKS é buscado sob demanda; se o Keycloak estiver fora e a chave não estiver em cache, a
  verificação falha e a resposta é **401**, não 503. O processo sobe mesmo assim.
- **Identidade no SQS:** o broker determina o provedor. O consumidor lê o atributo `SenderId` (a conta AWS remetente) e o
  traduz pelo mapa `SQS_SENDER_PROVIDER_MAP`. Se o remetente é desconhecido ou `data.providerId` não bate, a mensagem
  vai para a DLQ com `PROVIDER_IDENTITY_MISMATCH`, sem efeito. A queue policy só permite `SendMessage` às contas dos
  provedores. Um mapa vazio usa o padrão; chaves duplicadas valem a última.
- **Menor privilégio** na conta dona das filas: `wallet-consumer` (receber, apagar, mudar visibilidade, enviar à DLQ de
  entrada) e `wallet-publisher` (enviar a `wallet-events.fifo`).

## 10. Fx e ciclo de vida

- Um `fx.Module` por área: `logging`, `metrics`, `postgres`, `app`, `sqs` (clientes), `sqsconsumer`, `outbox`,
  `refworker`, `auth` e `httpapi`. `composition.Modules(cfg)` escolhe os módulos pelos toggles.
- A configuração é validada antes de qualquer coisa (todos os erros de uma vez). No `OnStart`, o PostgreSQL é pingado e
  as filas são resolvidas por nome: sem eles o processo não sobe.
- **Toggles:** `HTTP_ENABLED`, `CONSUMER_ENABLED`, `OUTBOX_ENABLED`, `REFWORKER_ENABLED` (padrão: todos ligados). Sem
  HTTP não há `/metrics`, `/health` nem exigência de OIDC. O módulo SQS só entra com o consumidor ou a outbox.
- **Ordem de parada** (o Fx desfaz na ordem inversa da construção):
  1. o HTTP, primeiro: marca o readiness como "draining", para de aceitar conexões e espera as requisições em andamento
     até `HTTP_SHUTDOWN_TIMEOUT` (15s), depois força o fechamento;
  2. o consumidor e os workers drenam, cada um com seu prazo;
  3. por fim, o pool do PostgreSQL fecha.
- **`StopTimeout`** = 10s de margem + a soma dos prazos dos componentes ligados. Com tudo ligado:
  10s + 15s (HTTP) + 20s (consumidor) + 12s (`AbortGrace` do consumidor) + 10s (outbox) + 10s (worker de referências) =
  **77s**. Como os hooks de parada rodam em sequência, o orçamento é a soma. O `stop_grace_period` do compose é **90s**
  (o padrão de 10s do Docker mataria o processo no meio da drenagem).
- Durante a drenagem `/health/ready` responde 503, mas isso é invisível ao balanceador: não há espera antes de parar de
  aceitar conexões. O nginx tenta outra réplica quando a conexão é recusada (`proxy_next_upstream error timeout`, que
  por padrão não reenvia POST), e o cliente pode repetir um POST com segurança por causa da `Idempotency-Key`.

## 11. Observabilidade

**Logs** (`log/slog`, JSON, em stdout, nível `LOG_LEVEL`). Campos usados: `correlationId` (header `X-Correlation-Id` se
for válido, senão gerado; no SQS vem do envelope ou do `messageId`), `messageId`, `sqsMessageId`, `transactionId`,
`walletId`, `providerId`, `eventId`, `eventType`, `kind`, `status`, `failureCode`, `component`, `route`, `method`,
`latencyMs`. Nunca vão tokens, corpos de requisição nem o payload financeiro completo. Erros de autenticação e falhas
permanentes aparecem em WARN e ERROR; divergências de reconciliação, em WARN.

**Métricas** (`/metrics`, registro próprio de cada processo):

| Métrica | Labels | Significado |
| --- | --- | --- |
| `wager_transactions_total` | `kind`, `status`, `channel` (`http`, `sqs`, `worker`) | operações concluídas |
| `idempotent_replays_total` | `channel` | operações respondidas do resultado persistido |
| `concurrency_conflicts_total` | `type` (`lock_timeout`, `version`, `unique`, `transient`) | conflitos transitórios de concorrência |
| `processing_duration_seconds` | `channel` | latência do processamento (histograma) |
| `reconciliation_divergences_total` | | reconciliações em que o ledger diverge do saldo |
| `inbox_duplicates_total` | | mensagens descartadas pela inbox |
| `sqs_retries_total` | | mensagens deixadas para reentrega |
| `sqs_dlq_total` | `reason` | mensagens enviadas à DLQ pelo consumidor, por motivo |
| `outbox_publish_attempts_total` | `result` (`published`, `failed`) | tentativas de publicação |
| `outbox_lag_seconds` | | atraso entre a ocorrência e a publicação (só as bem sucedidas) |
| `reference_retries_total` | | tentativas do worker que mantiveram a operação pendente |
| `http_requests_total` | `method`, `route`, `status` | requisições HTTP |
| `http_request_duration_seconds` | `method`, `route` | latência HTTP (histograma) |

Há ainda as métricas padrão de Go e de processo. O Prometheus (`deploy/prometheus/prometheus.yml`) faz scrape de **todas**
as réplicas, descobertas por `dns_sd_configs` do serviço `wallet` (porta 8080, a cada 5s). O dashboard "Wallet
Service" (`deploy/grafana/dashboards/wallet.json`, provisionado) tem painéis de operações por status, por tipo e
canal, latência p50/p95/p99, replays, conflitos, duplicatas da inbox, retries do SQS, DLQ por motivo, atraso e
resultado da outbox, retentativas de referência, divergências de reconciliação e HTTP por rota e status. Só usa
métricas que o código registra (um teste confere).

**Saúde:** `GET /health/live` responde sempre 200. `GET /health/ready` consulta o PostgreSQL (ping) e o SQS
(`GetQueueAttributes` na fila de entrada com a identidade do consumidor e na de eventos com a do publisher, conforme os
componentes ligados), cada um com timeout de 2s, e responde 503 se algum falhar ou se o processo estiver drenando.

## 12. Failure codes

Toda rejeição traz um `failureCode` estável e uma `category`: `CORRECTABLE` (corrigir a entrada resolve) ou
`DEFINITIVE`. A lista vem de `internal/domain/wagering/failure.go`.

**Entrada inválida** (`CORRECTABLE`; HTTP 400; nunca persistida; no SQS vai para a DLQ com `failureReason` = o código e
é apagada). O corpo do HTTP inclui `details[{field, code, reason}]`.

| Código | Quando ocorre |
| --- | --- |
| `MALFORMED_PAYLOAD` | JSON ou envelope inválido, campo desconhecido, dados após o objeto, corpo acima de 1 MiB |
| `MISSING_FIELD` | campo obrigatório ausente |
| `INVALID_FIELD` | texto com mais de 255 bytes ou com caracteres de controle, tipo JSON errado, `type` ou `occurredAt` inválidos no envelope SQS |
| `INVALID_ID` | UUID inválido ou fora da forma canônica em minúsculas |
| `INVALID_MONEY` | formato, escala, sinal ou overflow do valor |
| `UNSUPPORTED_CURRENCY` | moeda que não é ISO 4217 com 2 decimais |
| `UNKNOWN_KIND` | tipo desconhecido |
| `KIND_NOT_ALLOWED` | `OPENING` enviado por HTTP ou SQS |
| `INVALID_AMOUNT_FOR_KIND` | `LOSS` diferente de `0.00`, ou zero em BET, WIN, REFUND ou ROLLBACK |
| `MISSING_REFERENCE` | REFUND ou ROLLBACK sem `referenceExternalTransactionId` |
| `REFERENCE_NOT_ALLOWED` | BET ou LOSS com `referenceExternalTransactionId` |
| `MISSING_IDEMPOTENCY_KEY` | header `Idempotency-Key` (ou `data.idempotencyKey`) ausente |
| `INVALID_IDEMPOTENCY_KEY` | chave com mais de 255 bytes ou com caracteres de controle |

**Rejeições de negócio** (`DEFINITIVE`; persistidas como `REJECTED`; HTTP 422; no SQS a mensagem é apagada, pois o
resultado foi commitado):

| Código | Quando ocorre |
| --- | --- |
| `INSUFFICIENT_FUNDS` | BET sem saldo |
| `INSUFFICIENT_FUNDS_FOR_REVERSAL` | reversão que debitaria mais que o saldo |
| `WALLET_NOT_FOUND` | a carteira não existe |
| `WALLET_PLAYER_MISMATCH` | a carteira não é do jogador informado |
| `CURRENCY_MISMATCH` | moeda diferente da da carteira |
| `REFERENCE_NOT_FOUND` | a referência não chegou até esgotar as tentativas |
| `REFERENCE_NOT_PROCESSED` | a referência está `REJECTED` ou `FAILED` (ou seguiu pendente até esgotar) |
| `REFERENCE_KIND_INVALID` | o tipo da referência não serve para o tipo da operação |
| `REFERENCE_MISMATCH` | jogador, carteira, moeda ou rodada diferentes da referência |
| `AMOUNT_MISMATCH` | valor de uma reversão diferente do da referência |
| `REFERENCE_ALREADY_REVERSED` | a referência (ou a BET) já recebeu uma reversão bem sucedida |

**Outros** (`DEFINITIVE`):

| Código | Quando ocorre | HTTP | SQS |
| --- | --- | --- | --- |
| `INFRASTRUCTURE_FAILURE` | falha permanente de infraestrutura; persistido como `FAILED` | 500 (replay também) | DLQ com `failureReason`, e apagada |
| `PROVIDER_IDENTITY_MISMATCH` | remetente desconhecido ou `providerId` diferente do remetente | não se aplica (o HTTP responde 403) | DLQ, sem efeito |
| `MESSAGE_ID_REUSED` | `messageId` reentregue com outro conteúdo | não se aplica | DLQ, sem efeito |

**Erros do contrato HTTP sem `failureCode` de domínio:**

| Status | `failureCode` | Situação |
| --- | --- | --- |
| 400 | `INVALID_CURSOR`, `INVALID_LIMIT` | cursor ilegível; `limit` fora de 1 a 200 |
| 401 | `UNAUTHENTICATED` | token ausente, inválido ou expirado |
| 403 | `FORBIDDEN` | scope insuficiente ou `providerId` divergente |
| 404 | `NOT_FOUND` | não existe, ou é de outro provedor |
| 409 | `IDEMPOTENCY_KEY_CONFLICT`, `EXTERNAL_TRANSACTION_CONFLICT`, `WALLET_ALREADY_EXISTS` | conflitos (no SQS os dois primeiros vão para a DLQ) |
| 500 | `INTERNAL_ERROR` | erro inesperado |
| 503 | `TEMPORARILY_UNAVAILABLE` | erro transitório; com `Retry-After: 1` |

## 13. Máquina de estados e eventos

```
PENDING            --> PROCESSED | REJECTED | PENDING_REFERENCE | FAILED
PENDING_REFERENCE  --> PROCESSED | REJECTED | FAILED     (a retentativa soma tentativas, sem mudar de estado)
PROCESSED, REJECTED, FAILED: terminais
```

`PENDING` existe só dentro da transação: ao commitar, a linha já está em outro estado. A máquina é validada no domínio e
por trigger no banco (que também recusa qualquer `UPDATE` de linha terminal).

| Transição | Evento |
| --- | --- |
| `-> PROCESSED` | `WagerTransactionProcessed` e, se o saldo mudou, `WalletBalanceChanged` |
| `-> REJECTED` | `WagerTransactionRejected` |
| `-> PENDING_REFERENCE` (a primeira vez) | `WagerTransactionPendingReference` |
| `-> FAILED` | nenhum (registro de auditoria) |

`FAILED` é uma falha permanente de infraestrutura registrada para auditoria em todos os fluxos: o HTTP e o SQS dão
rollback da transação principal e gravam o `FAILED` em outra transação; o worker o grava na própria linha pendente. Um
erro transitório nunca gera `FAILED`. Os contratos dos eventos estão na seção 8.

## 14. Interpretações adotadas

O enunciado deixa lacunas; estas são as decisões tomadas:

- **A primeira tentativa síncrona conta como a número 1.** Com `REFERENCE_RETRY_MAX_ATTEMPTS=10` há 9 retentativas do
  worker.
- **WIN com referência opcional que nunca chega termina `REJECTED`** (`REFERENCE_NOT_FOUND`); uma referência que existe
  mas segue pendente até o fim vira `REFERENCE_NOT_PROCESSED`.
- **WIN sobre uma BET já revertida é aceito.** A BET reversa só bloqueia outra reversão, não um WIN.
- **`MaxFieldLength` (255) conta bytes** e campos de texto não aceitam caracteres de controle.
- **Códigos extras** além do enunciado: `INVALID_FIELD` e `REFERENCE_NOT_ALLOWED`.
- **Conflitos pelo SQS vão para a DLQ** (`IDEMPOTENCY_KEY_CONFLICT`, `EXTERNAL_TRANSACTION_CONFLICT`), pois não há
  resposta a quem enviou.
- **Inbox por provedor** (`wager-transactions/<providerId>`): o `messageId` só precisa ser único por provedor.
- **Uma redelivery duplicada cuja transação está `FAILED` vai para a DLQ** com `INFRASTRUCTURE_FAILURE`, porque o envio
  original à DLQ pode não ter terminado.
- **Aceite síncrono:** o serviço processa a operação dentro da requisição (ou da mensagem) e responde o resultado
  final, sem um estado intermediário de "aceito para processar depois". Por isso o item 8b do enunciado não se aplica.
  A exceção é `PENDING_REFERENCE`, que responde 202.
- **Uma BET aceita uma única reversão** (REFUND ou ROLLBACK), para não devolver o mesmo débito duas vezes.
- **O schema impõe as invariantes por conta própria** (FK composta da referência, lista de códigos de rejeição, ledger
  preso à carteira da transação, triggers de append-only), sem depender do código da aplicação.
- **Erros transitórios do worker de referências** são retentados sem limite próprio: o `REFERENCE_MAX_INFRA_ATTEMPTS`
  do design não foi implementado; só erros permanentes levam a `FAILED`.

## 15. Limitações e trabalho não concluído

- **MiniStack:** não verifica o secret nem a assinatura SigV4; o `SenderId` devolvido é o id da conta, não o do usuário
  IAM; um usuário IAM de outra conta é negado no envio entre contas mesmo com políticas corretas, então os provedores
  usam o root da própria conta; todo o estado fica em memória; reexecutar o provisionamento mantém as chaves IAM, e só um
  reinício do MiniStack gera chaves novas (então reinicie os wallets). Na AWS real, o mapa de remetentes usaria o id do usuário IAM. O E2E usa a credencial root do MiniStack de
  teste, enquanto o compose usa os perfis IAM de menor privilégio.
- **Ordem da outbox:** com vários publishers a ordem por carteira não é garantida; o consumidor deve ordenar por
  `walletVersion` e deduplicar por `eventId` (a deduplicação do SQS vale 5 minutos).
- **Cancelamento no meio do receive:** um cancelamento durante o `ReceiveMessage` pode deixar uma mensagem invisível
  por 30s (a visibilidade da fila) até ela reaparecer.
- **Cabeça da fila de referências:** uma pendência com erro transitório persistente atrasa as seguintes (seção 6).
- **A fila `wallet-events-dlq.fifo` é provisionada, mas nada a consome.** Não há consumidor dos eventos neste serviço.
- **Drenagem invisível ao balanceador:** não há pré-parada com atraso; a segurança vem das tentativas do nginx e da
  idempotência.
- **Fora do escopo:** testes de carga e tracing (OpenTelemetry), que eram opcionais no enunciado, e partidas dobradas.
- **Itens menores conhecidos nos testes:** o E2E não prova a
  sobreposição das requisições no cenário 1 e pode deixar processos órfãos se o binário de teste for morto.
