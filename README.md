# backend-challenge-go

Serviço distribuído de carteiras e apostas, em Go. Ele mantém carteiras com saldo e um ledger imutável, recebe
operações de provedores de jogos (BET, WIN, LOSS, REFUND e ROLLBACK) por HTTP e por SQS com idempotência, trata
referências que chegam fora de ordem e publica eventos de domínio por outbox. Roda em várias instâncias atrás de um
nginx, com Keycloak (OIDC), PostgreSQL, SQS (MiniStack), Prometheus e Grafana.

- Enunciado: [docs/CHALLENGE.md](docs/CHALLENGE.md)
- Arquitetura e decisões: [ARCHITECTURE.md](ARCHITECTURE.md)
- Design: [docs/superpowers/specs/2026-10-01-wallet-service-design.md](docs/superpowers/specs/2026-10-01-wallet-service-design.md)

## Pré-requisitos

- Docker com Compose v2.
- Go 1.25.11 ou superior (para rodar os testes e compilar fora do Docker).
- Um compilador C (gcc ou clang): os testes E2E compilam o binário com `-race`, que exige cgo. Já vem no macOS; no
  Linux instale `build-essential`.
- `curl` e `python3`, só para os scripts em `scripts/` e os exemplos abaixo.

## Subir tudo

```bash
docker compose up --build
```

Sobem, nesta ordem de dependência: `postgres` (com as roles `wallet_owner` e `wallet_app`), `migrate` (aplica as
migrations e termina), `keycloak` (importa o realm `wallet`), `ministack` e `ministack_init` (cria as filas, as
políticas e os usuários IAM, e termina), três réplicas de `wallet`, `nginx`, `prometheus` e `grafana`.

| Serviço | Endereço no host | Observação |
| --- | --- | --- |
| API (nginx, balanceia as 3 réplicas) | http://localhost:8000 | `WALLET_PORT`; `/metrics` não é exposto pelo nginx (só por réplica, via Prometheus) |
| Keycloak | http://localhost:8080 | admin: `admin` / `admin` |
| MiniStack (SQS, IAM) | http://localhost:4566 | |
| Prometheus | http://localhost:9090 | |
| Grafana | http://localhost:3000 | leitura anônima; admin: `admin` / `admin`; dashboard "Wallet Service" já provisionado |
| PostgreSQL | localhost:5432 | `POSTGRES_PORT`; usuários `wallet_owner` e `wallet_app` |

As réplicas do `wallet` não publicam porta no host: todo o tráfego passa pelo nginx. Para ver as 3 réplicas:

```bash
docker compose ps wallet           # wallet-wallet-1, -2 e -3
docker compose logs -f wallet      # logs JSON de todas, com o prefixo da réplica
```

Em http://localhost:9090/targets o job `wallet` lista as 3 réplicas, descobertas pelo DNS do Docker. Para outro número
de réplicas, use `docker compose up --build --scale wallet=2`.

Teste de ponta a ponta da stack no ar (abre carteira, aposta por HTTP, aposta por SQS e reconcilia):

```bash
scripts/smoke.sh
```

Para parar sem perder dados:

```bash
docker compose stop
```

`docker compose down -v` apaga os volumes, inclusive os dados do PostgreSQL. Use só quando quiser recomeçar do zero
(por exemplo, depois de editar as migrations ou o init das roles).

## Variáveis de ambiente

Todas estão documentadas, com os valores locais padrão, em [.env.example](.env.example). O `docker compose` lê um
`.env` na raiz (ignorado pelo git) só para as variáveis `${...}` do `docker-compose.yml`. As principais:

| Grupo | Variáveis |
| --- | --- |
| Banco | `DATABASE_URL` (obrigatória), `DB_MAX_OPEN_CONNS`, `DB_LOCK_TIMEOUT`, `DB_STATEMENT_TIMEOUT` |
| HTTP e log | `HTTP_ADDR` (padrão `:8080`), `HTTP_SHUTDOWN_TIMEOUT`, `LOG_LEVEL` |
| OIDC | `OIDC_ISSUER_URL` (obrigatória com HTTP), `OIDC_JWKS_URL`, `OIDC_AUDIENCE` (padrão `wallet-api`) |
| SQS e AWS | `AWS_REGION`, `AWS_SHARED_CREDENTIALS_FILE`, `SQS_ENDPOINT`, `SQS_CONSUMER_PROFILE`, `SQS_PUBLISHER_PROFILE`, `SQS_INPUT_QUEUE`, `SQS_INPUT_DLQ`, `SQS_EVENTS_QUEUE`, `SQS_CONSUMER_WORKERS`, `SQS_WAIT_TIME`, `SQS_MAX_MESSAGES`, `SQS_RETRY_BASE_DELAY`, `SQS_RETRY_MAX_DELAY`, `SQS_SHUTDOWN_TIMEOUT`, `SQS_SENDER_PROVIDER_MAP` |
| Toggles | `HTTP_ENABLED`, `CONSUMER_ENABLED`, `OUTBOX_ENABLED`, `REFWORKER_ENABLED` (todos ligados por padrão) |
| Outbox | `OUTBOX_POLL_INTERVAL`, `OUTBOX_BATCH_SIZE`, `OUTBOX_LEASE`, `OUTBOX_RETRY_BASE_DELAY`, `OUTBOX_RETRY_MAX_DELAY`, `OUTBOX_SHUTDOWN_TIMEOUT` |
| Referências pendentes | `REFWORKER_POLL_INTERVAL`, `REFWORKER_SHUTDOWN_TIMEOUT`, `REFERENCE_RETRY_BASE_DELAY`, `REFERENCE_RETRY_MAX_DELAY`, `REFERENCE_RETRY_MAX_ATTEMPTS` |
| Compose | `WALLET_PORT`, `POSTGRES_PORT`, `KEYCLOAK_PORT`, `MINISTACK_PORT`, `PROMETHEUS_PORT`, `GRAFANA_PORT`, `*_TEST_PORT`, `POSTGRES_PASSWORD`, `WALLET_OWNER_PASSWORD`, `WALLET_APP_PASSWORD`, `KEYCLOAK_ADMIN_PASSWORD`, `GRAFANA_ADMIN_PASSWORD` |

Uma configuração inválida (duração fora de 1ms a 24h, inteiro não positivo, `SQS_RETRY_BASE_DELAY` abaixo de 1s etc.)
impede o processo de subir, e todos os erros são listados de uma vez.

**Atenção:** `SQS_CONSUMER_PROFILE` e `SQS_PUBLISHER_PROFILE` vazios fazem o SDK da AWS usar a credencial padrão. No
MiniStack isso é o root da conta dona das filas, o que anula o menor privilégio. O compose define os dois perfis
(`wallet-consumer` e `wallet-publisher`).

## Filas (MiniStack)

O serviço `ministack_init` roda `deploy/ministack/provision.sh` (no container `amazon/aws-cli`; o script exige
bash 4.4 ou superior, por isso roda lá e não no macOS) depois que o MiniStack fica saudável. É seguro rodá-lo de novo.
Ele cria:

| Fila | Papel |
| --- | --- |
| `wager-transactions.fifo` | entrada dos provedores; visibilidade 30s; redrive para a DLQ com `maxReceiveCount=5` |
| `wager-transactions-dlq.fifo` | DLQ da entrada |
| `wallet-events.fifo` | eventos publicados pela outbox; visibilidade 30s; redrive para a DLQ com `maxReceiveCount=5` |
| `wallet-events-dlq.fifo` | DLQ dos eventos |

Todas são FIFO, sem deduplicação por conteúdo. Contas e identidades:

- A queue policy de `wager-transactions.fifo` permite `SendMessage` só às contas `111111111111` (`provider-a`) e
  `222222222222` (`provider-b`). Qualquer outra conta recebe `AccessDenied`. O consumidor traduz o `SenderId` da
  mensagem em `providerId` pelo mapa `SQS_SENDER_PROVIDER_MAP`.
- Na conta dona (`000000000000`) existem dois usuários IAM com privilégio mínimo: `wallet-consumer` (receber, apagar,
  mudar visibilidade e enviar para a DLQ de entrada) e `wallet-publisher` (enviar para `wallet-events.fifo`).
- As chaves IAM são geradas pelo MiniStack e gravadas em `.local/ministack/credentials` (ignorado pelo git), com os
  perfis `wallet-consumer`, `wallet-publisher`, `provider-a` e `provider-b`. Os dois provedores usam a credencial root
  da própria conta, porque o MiniStack nega usuários IAM no envio entre contas.
- Reexecutar o `ministack_init` (inclusive via `docker compose up -d wallet`) mantém as chaves: se o perfil já está no
  arquivo e a chave ainda consta em `iam list-access-keys`, nada é apagado nem recriado. O MiniStack guarda tudo em
  memória; só quando ele é reiniciado/recriado (estado perdido) o provisionamento cria chaves novas, e então os wallets
  precisam ser reiniciados (`docker compose restart wallet`) para ler o arquivo novo.

## Migrations

`docker compose up` já roda o serviço `migrate` (golang-migrate), que aplica as migrations com `up`. Para rodar à mão:

```bash
docker compose run --rm migrate
```

Para reverter a última migration, sobrescreva o comando do serviço:

```bash
docker compose run --rm migrate -path=/migrations -database='postgres://wallet_owner:wallet_owner@postgres:5432/wallet?sslmode=disable' down 1
```

(troque `wallet_owner` pela senha de `WALLET_OWNER_PASSWORD`, se a tiver mudado). O script
`deploy/postgres/initdb/01-roles.sh`, que cria as roles e o banco `wallet`, roda só quando o volume `pgdata` está
vazio. As tabelas e os grants vêm das migrations em `migrations/`.

## Autenticação e exemplos

O realm `wallet` do Keycloak (importado de `deploy/keycloak/realm-wallet.json`) tem clientes confidenciais com
`client_credentials`. O segredo de cada um é `<client_id>-secret` (valores de desenvolvimento, versionados). O token
tem audience `wallet-api` e dura 300s.

| Cliente | Scopes | `provider_id` | Uso |
| --- | --- | --- | --- |
| `provider-a` | `wagering` | `provider-a` | provedor |
| `provider-b` | `wagering` | `provider-b` | provedor |
| `wallet-internal` | `wallets`, `wagering:read` | nenhum | serviço interno: carteiras e leitura de qualquer transação |
| `short-lived` | `wagering` | `provider-a` | token de 2s, para testar expiração |
| `no-audience` | `wagering` | `provider-a` | token sem a audience `wallet-api`, para testar rejeição |

`scripts/token.sh CLIENTE` imprime o access token (o issuer dos tokens é `http://localhost:8080/realms/wallet`, então
o token deve ser pedido nesse endereço). Exemplos pelo nginx:

```bash
BASE=http://localhost:8000
INTERNAL=$(scripts/token.sh wallet-internal)
PROVIDER=$(scripts/token.sh provider-a)
PLAYER=$(python3 -c 'import uuid; print(uuid.uuid4())')

# Abrir carteira (201). Com saldo > 0 cria também a operação OPENING e o crédito no ledger.
curl -s -X POST $BASE/wallets -H "Authorization: Bearer $INTERNAL" -H 'Content-Type: application/json' \
  -d "{\"playerId\":\"$PLAYER\",\"initialBalance\":{\"amount\":\"100.00\",\"currency\":\"BRL\"}}"
WALLET=<id da resposta>

# Consultar a carteira, o ledger (limit de 1 a 200, padrão 50; cursor opaco da resposta) e reconciliar
curl -s $BASE/wallets/$WALLET -H "Authorization: Bearer $INTERNAL"
curl -s "$BASE/wallets/$WALLET/ledger?limit=50" -H "Authorization: Bearer $INTERNAL"
curl -s -X POST $BASE/wallets/$WALLET/reconciliation -H "Authorization: Bearer $INTERNAL"

# Enviar uma operação (o providerId do corpo deve ser o do token; Idempotency-Key é obrigatório)
EXT=$(python3 -c 'import uuid; print(uuid.uuid4())')
curl -s -X POST $BASE/wagering/transactions -H "Authorization: Bearer $PROVIDER" \
  -H 'Content-Type: application/json' -H "Idempotency-Key: provider-a:$EXT" \
  -d "{\"providerId\":\"provider-a\",\"externalTransactionId\":\"$EXT\",\"playerId\":\"$PLAYER\",\"walletId\":\"$WALLET\",\"roundId\":\"r1\",\"gameId\":\"g1\",\"kind\":\"BET\",\"money\":{\"amount\":\"25.00\",\"currency\":\"BRL\"}}"
# Repetir o mesmo comando devolve o mesmo resultado com "idempotentReplay": true.

# Consultar por id interno (provedor: só as próprias; wallet-internal: todas) e pela rota do provedor
curl -s $BASE/wagering/transactions/<transactionId> -H "Authorization: Bearer $PROVIDER"
curl -s $BASE/providers/provider-a/wagering/transactions/$EXT -H "Authorization: Bearer $PROVIDER"
```

Respostas de `POST /wagering/transactions`: 200 (`PROCESSED`), 202 (`PENDING_REFERENCE`), 400 (entrada inválida, com
`details`), 401, 403, 409 (`IDEMPOTENCY_KEY_CONFLICT` ou `EXTERNAL_TRANSACTION_CONFLICT`), 422 (`REJECTED`, com
`failureCode`), 500 (`FAILED`) e 503 (`TEMPORARILY_UNAVAILABLE`, com `Retry-After`). A tabela completa de códigos está
no [ARCHITECTURE.md](ARCHITECTURE.md#12-failure-codes).

### Envio por SQS

`scripts/sqs-send.sh PERFIL MESSAGE_GROUP_ID 'JSON'` envia uma mensagem para `wager-transactions.fifo` com a credencial
do provedor (`provider-a` ou `provider-b`), usando a imagem do AWS CLI do compose e o arquivo de credenciais
provisionado. A stack precisa ter subido ao menos uma vez.

```bash
EXT=$(python3 -c 'import uuid; print(uuid.uuid4())')
scripts/sqs-send.sh provider-a $WALLET "{\"messageId\":\"msg-$EXT\",\"type\":\"WagerTransactionRequested\",\"occurredAt\":\"2026-09-08T12:00:00.000Z\",\"data\":{\"providerId\":\"provider-a\",\"externalTransactionId\":\"$EXT\",\"idempotencyKey\":\"provider-a:$EXT\",\"playerId\":\"$PLAYER\",\"walletId\":\"$WALLET\",\"roundId\":\"r1\",\"gameId\":\"g1\",\"kind\":\"BET\",\"money\":{\"amount\":\"10.00\",\"currency\":\"BRL\"}}}"
```

O envelope tem `messageId`, `type` (sempre `WagerTransactionRequested`), `occurredAt` (RFC 3339), `correlationId`
(opcional) e `data`, com os mesmos campos do HTTP mais o `idempotencyKey`. Contrato do produtor: `MessageGroupId` é o
`walletId` e `MessageDeduplicationId` é o `messageId` (o script faz isso). O `providerId` de `data` deve ser o da conta
remetente; caso contrário a mensagem vai para a DLQ com `PROVIDER_IDENTITY_MISMATCH`.

## Testes

```bash
go test ./...                          # tudo: unitários, integração e E2E (exige a infra de teste)
go test -race ./...                    # idem, com o detector de corridas
go test -race -tags=unit ./...         # só unitários, sem infra
go test -race -tags=integration ./...  # só integração
go test -race -tags=e2e ./...          # só E2E multi-instância e falhas
go vet ./...
```

As tags são filtros de exclusão: sem tag roda tudo; com uma tag, só aquela camada. A infra de teste fica no profile
`test` do compose, em portas próprias (PostgreSQL 5433, Keycloak 8081, MiniStack 4567) e sem afetar a stack de
desenvolvimento:

```bash
docker compose --profile test up -d --wait postgres_test keycloak_test ministack_test
docker compose --profile test run --rm migrate_test
docker compose --profile test run --rm ministack_test_init
```

Testes que precisam da infra falham com uma mensagem dizendo qual comando rodar; nunca são pulados em silêncio.

| Camada | O que cobre |
| --- | --- |
| Unitários | Money (parsing, escala, overflow, moedas), Wallet e LedgerEntry, máquina de estados, regras dos 5 tipos, hash canônico, config, parsing das mensagens SQS, validação do token, regras dos casos de uso que não dependem do banco, e o dashboard (só usa métricas que o código registra) |
| Integração | migrations up/down/up, constraints e triggers (ledger imutável), repositórios, atomicidade, handlers HTTP com tokens reais do Keycloak, inbox, consumidor e DLQ no MiniStack, outbox com dois publishers, composição Fx e ordem de parada |
| E2E (`e2e/`) | o binário real em vários processos do SO, cada teste com banco (`pgtest.FreshDatabase`) e filas (`sqstest.NewQueues`) próprios |

Cenários E2E (enunciado, seção 13), em `e2e/concurrency_test.go` e `e2e/recovery_test.go`:

1. 50 BETs idênticas em paralelo nas 3 instâncias: um só débito (`TestSameBetFiftyTimesAcrossInstances`).
2. 80 + 80 sobre saldo 100, com o vencedor segurando a transação aberta: um `PROCESSED`, um `INSUFFICIENT_FUNDS`, saldo
   20 (`TestTwoBetsOfEightyOnOneHundred`).
3. Carteiras distintas em paralelo (`TestDistinctWalletsInParallel`).
4. Cenários sobre as 3 instâncias, incluindo o cruzamento HTTP e SQS e as recepções repetidas
   (`TestSameOperationOverHTTPAndSQS`, `TestRepeatedSQSReceptionsAreDeduplicated`).
5. Queda depois do commit e antes do delete: reentrega sem duplicar (`TestCrashAfterCommitBeforeDelete`).
6. Dois publishers e queda depois de publicar e antes de marcar: republicação com o mesmo `eventId`
   (`TestCrashAfterPublishBeforeMark`).
7. REFUND e ROLLBACK antes da referência: resolução pelo worker e expiração em `REFERENCE_NOT_FOUND`
   (`TestReversalBeforeReference`).
8. Reinício (`kill -9` e SIGTERM) preserva idempotência, pendências e consistência
   (`TestRestartPreservesIdempotencyAndPendings`).

Ao final de cada cenário, o saldo é conferido contra o ledger pela reconciliação.

O pacote `internal/platform/faultinject` é interno: o `TestMain` do E2E compila o binário com `-tags faultinject` e o
usuário não precisa passar a tag. Os valores de `FAULT` são `crash_after_commit_before_delete` e
`crash_after_publish_before_mark`. O E2E usa a credencial root do MiniStack de teste (`test`/`test`), enquanto o compose
de desenvolvimento usa os perfis IAM `wallet-consumer` e `wallet-publisher`.

## Multi-instância e falhas

- O `docker-compose.yml` sobe 3 réplicas do `wallet` (`deploy.replicas: 3`) atrás do nginx. Todas rodam todos os
  componentes: HTTP, consumidor SQS, relay da outbox e worker de referências. Os trabalhos são seguros entre instâncias
  (`SKIP LOCKED`, lease e fencing na outbox, idempotência no banco).
- Para processos dedicados use os toggles `HTTP_ENABLED`, `CONSUMER_ENABLED`, `OUTBOX_ENABLED` e `REFWORKER_ENABLED`.
  O compose não define serviços dedicados; o E2E e a execução no host usam os toggles. Sem HTTP o processo não expõe
  `/metrics` nem `/health`.
- `stop_grace_period: 90s` no `wallet`. No desligamento o HTTP para primeiro, depois o consumidor e os workers drenam,
  e por último o pool do banco fecha. O orçamento do Fx (`StopTimeout`) com todos os componentes é 77s (10s de margem
  + 15s HTTP + 20s consumidor + 12s de abort do consumidor + 10s outbox + 10s worker de referências); o padrão de 10s do
  Docker mataria o processo no meio da drenagem.
- Durante a drenagem o `/health/ready` responde 503, mas não há espera antes de parar de aceitar conexões: o nginx
  tenta outra réplica quando a conexão é recusada, e um POST repetido pelo cliente é seguro por causa da
  `Idempotency-Key`.
- Para simular falhas à mão, compile o binário com a tag e rode-o no host com `FAULT` (o processo sai com código 86 no
  ponto escolhido; sem a tag, `FAULT` não faz nada, e as imagens do compose nunca são compiladas com ela):

  ```bash
  go build -tags faultinject -o /tmp/wallet-fault ./cmd/wallet
  cp -n .env.example .env       # valores locais; ajuste se precisar
  set -a; . ./.env; set +a
  FAULT=crash_after_commit_before_delete /tmp/wallet-fault
  ```

  O jeito mais simples de reproduzir cada cenário é o E2E (`go test -race -tags=e2e ./e2e/...`).

### Como rodar os cenários manuais de concorrência

Com a stack no ar (`docker compose up --build -d`, 3 réplicas saudáveis) e com `curl` e `python3` instalados:

```bash
scripts/scenarios/run-all.sh                          # cenários 01..10 + db-check, com tabela-resumo (logs em .local/scenario-logs/)
scripts/scenarios/03-rajada-mesma-carteira.sh         # ou um cenário isolado
scripts/scenarios/db-check.sh                         # invariantes globais do banco
```

Cada cenário imprime finalidade, o que enviou, o esperado, o obtido e `OK`/`FALHOU` (código de saída diferente de zero em falha).
O cenário 10 mata e recria uma réplica do `wallet` (`docker kill` + `docker compose up -d --no-deps wallet`; o `--no-deps` evita rodar o `ministack_init`
de novo, que de todo modo mantém as chaves existentes). `scripts/sqs-send.sh` aceita um 4º argumento
opcional `DEDUP_ID` para reenviar a mesma mensagem além da deduplicação FIFO.

## Estrutura do repositório

```
cmd/wallet/            binário único (HTTP, consumidor SQS, relay da outbox, worker de referências, por toggles)
internal/domain/       domínio puro: money, wallet, wagering (estados, regras, failure codes, hash), events
internal/app/          casos de uso e portas (repositórios, TxManager, Clock, métricas)
internal/adapters/     postgres (GORM), httpapi (Gin), auth (go-oidc), sqsconsumer, awssqs, workers, jsonstrict
internal/platform/     config, logging, metrics, health, background, composition (Fx), faultinject
internal/testsupport/  helpers dos testes: pgtest, sqstest, kctest, apptest, authtest
migrations/            migrations do golang-migrate
deploy/                realm do Keycloak, provisionamento do MiniStack, nginx, Prometheus, Grafana, init das roles
scripts/               token.sh, sqs-send.sh, smoke.sh
e2e/                   testes de ponta a ponta com processos reais
docs/                  enunciado e design
```

# Nota do desenvolvedor

*nota escrita manualmente.*

Este projeto, obviamente, foi feito 100% com agentes de código. Porém, não foi apenas jogado para ele fazer, mas sim com processos, ferramentas e metodologias voltadas para o desenvolvimento seguro e pratico com IA. 

Acredito veemente que atualmente não é mais pratico escrever código na mão (salvo para fix que a IA iria demorar mais que eu pra achar), sendo cético a tecnologia por um bom tempo desde que ela se provou apta para escrever 100% do código, então o processo de desenvolvimento deste projeto reflete o meu desenvolvimento dentro da empresa ao qual  entrego este desafio. 

Essa nota serve para eu como desenvolvedor relatar o que utilizei, como utilizei e que processos realizei no desenvolvimento.

Começando, foi lido 100% do enunciado, criterios de avaliação, ferramentas obrigatórias e recomendas e o objetivo esperado. Pessoalmente, nunca desenvolvi um ledger nem sabia a fundo como desenvolver um a nivel de codigo e segurança, apenas conhecia o conceito, entao antes mesmo de ir para o projeto, fui atras de pesquisar como um ledger era desenvolvido na pratica com a linguagem Go, assim saberia me guiar corretamente com a spec do agente.  
Após entender completamente o enunciado, parti para o agente, neste caso o Claude utilizado dentro da IDE Orca, onde utilizo o plugin \`superpowers\` para SDD, TDD, geração de planos mais definidos e subagent-driven-development.

No prompt inicial, marquei o antigo README (que agora é o [docs/CHALLENGE.MD)](docs/CHALLENGE.MD) e adicionei algumas informações, como diretivas especificas para não sair do contexto, seguir schemas e recomendações de tecnologia, focar na cobertura dos criterios de aceite e testabilidade, utilização do Gin como HTTP-server, optei por utilização do GORM com sql escrito, utilização do Nginx para facilitar uso de replicas do serviço, utilizar Prometheus com Grafana com graficos provisionados (não apenas logs), entre outros.  
Com isso e o agente em \`manual mode\`, ele vai me fazendo perguntas para decisões e gaps no proprio enunciado, para eu ir tomando decisoes e ir gerando o design inicial, e com isso o spike dos planos separados em etapas. Com a geração do design, é feito a revisão do design e aprovado para a geração do primeiro plano, que segue o mesmo fluxo até o final:  
Geração do plano -&gt; revisão do plano -&gt; aplicação com subagent-driven-development que aplica e faz revisoes a cada task dentro do plano -&gt; testes -&gt; revisão final -&gt; next   
Com isso, tenho o projeto 100% pronto, com ambiente testado em etapas e pronto para testes manuais, que realizo lendo o proprio enunciado e com ajuda do proprio Claude me passando cenarios e criando scripts para pentest de concorrencia.