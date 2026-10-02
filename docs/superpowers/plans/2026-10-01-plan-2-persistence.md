# Plano 2 — Infra Local e Persistência: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Subir o PostgreSQL de dev e de teste via Docker Compose (com papéis `wallet_owner`/`wallet_app`), criar as
migrations versionadas com todas as constraints e triggers da spec e implementar o adapter de persistência com GORM:
TxManager, repositórios de carteira, transação, ledger e outbox, ports da aplicação e o módulo Fx.

**Architecture:** As migrations (golang-migrate, SQL puro, uma transação por arquivo) são a fonte das invariantes no
banco: CHECKs, índices únicos parciais e triggers de imutabilidade e de máquina de estados. A aplicação conecta como
`wallet_app`, que não tem `UPDATE`/`DELETE`/`TRUNCATE` no ledger.

O adapter `internal/adapters/postgres` usa GORM com cláusulas explícitas (`clause.Locking`, `clause.OnConflict`,
`WHERE version = ?`) e mapeia linhas ↔ domínio por structs de modelo próprias, reidratando via
`wallet.Rehydrate`/`wagering.Rehydrate`.

O `TxManager` guarda a transação GORM no `context` e aplica `lock_timeout`/`statement_timeout` por transação. Os erros
de banco viram sentinelas de `internal/app`: `ErrNotFound`, `ErrConflict`, `ErrVersionConflict` e `ErrTransient`.

**Tech Stack:** Go 1.25, `gorm.io/gorm` + `gorm.io/driver/postgres` (pgx v5), `github.com/jackc/pgx/v5/pgconn` (classificação de erros), `go.uber.org/fx`, `github.com/golang-migrate/migrate/v4` (CLI na imagem `migrate/migrate:v4.18.3` e lib nos testes), PostgreSQL 17, Docker Compose.

**Spec:** `docs/superpowers/specs/2026-10-01-wallet-service-design.md` (seções 2, 6, 7, 13, 15 e 16). Enunciado: `docs/CHALLENGE.md`.

## Roadmap (atualizado)

1. ~~Fundação e domínio puro~~ (concluído).
2. **Infra local e persistência** (este plano): Postgres no compose, migrations, adapter GORM, ports e módulo Fx de DB.
3. **Casos de uso, HTTP e auth:** Keycloak, casos de uso (abertura, processamento idempotente, FAILED, consultas,
   paginação do ledger, reconciliação), Gin, health, logging e métricas base.
4. **Mensageria:** MiniStack no compose (com init de filas, IAM e contas), consumer SQS com inbox, outbox publisher
   com lease, worker de referências, DLQ e shutdown. O MiniStack saiu do Plano 2 e foi para o 4, onde é usado pela
   primeira vez.
5. **Multi-instância, observabilidade e entrega:** Dockerfile, nginx com 3 réplicas, Prometheus/Grafana, E2E com 3
   processos e `faultinject`, README, ARCHITECTURE.md e .env.example.

## Global Constraints

- Módulo `github.com/brunopstephan/backend-challenge-go`, `go 1.25`. `internal/domain/**` **não muda** neste plano e
  continua importando só stdlib, uuid e `internal/domain`.
- Dinheiro persiste como `*_minor BIGINT` + `currency CHAR(3)`; nunca float.
- Toda migration fica em `migrations/NNNNNN_nome.up.sql` + `.down.sql` e é envolvida em `BEGIN; ... COMMIT;`. O
  AutoMigrate do GORM é proibido.
- Papéis: as migrations rodam como `wallet_owner`, a aplicação conecta como `wallet_app`. `wallet_app` tem só
  `SELECT, INSERT` em `wallet_ledger_entries` e `inbox_messages`, `SELECT, INSERT, UPDATE` nas demais tabelas, e
  nunca `DELETE`/`TRUNCATE`.
- Caminhos críticos com SQL explícito e verificável:
  - `SELECT ... FOR UPDATE` via `clause.Locking{Strength: "UPDATE"}`;
  - `INSERT ... ON CONFLICT DO NOTHING` via `clause.OnConflict{DoNothing: true}`;
  - `UPDATE ... WHERE id = ? AND version = ?` com `version = version + 1`.
- Tags de build:
  - teste unitário: `//go:build !integration && !e2e`;
  - teste de integração: `//go:build !unit && !e2e`.

  Sem tag, `go test ./...` roda tudo e **exige** `postgres_test` de pé. Os testes de integração falham com a mensagem
  "suba a infra de teste" se o banco não responder, nunca são pulados.
- Testes usam só `testing` (sem testify). Os testes de integração usam dados próprios (UUIDs novos) no banco
  compartilhado `wallet` do `postgres_test`: nunca truncam nem dependem de ordem.
- Containers do projeto vivem só no `docker-compose.yml`.
- Código formatado com `gofmt`, e `go vet ./...` limpo.
- Toda mensagem de commit termina exatamente com `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.
- Obrigações herdadas do Plano 1, a cumprir aqui:
  - os dois índices únicos parciais de reversão, com `reference_kind` denormalizado;
  - `HasProcessedReversal` com a semântica de `ProcessInput.ReferenceAlreadyReversed`;
  - lock pessimista da carteira;
  - guarda de versão.

## Review Focus

1. Duas transações debitando a mesma carteira ao mesmo tempo: a segunda espera o lock, vê o saldo atualizado e
   nenhuma atualização se perde. Teste na Task 7.
2. Dois `Insert` concorrentes da mesma transação (mesma chave de idempotência): exatamente um retorna `inserted=true`.
   Teste na Task 8.
3. Espera de lock além do `lock_timeout` vira `app.ErrTransient` em vez de travar a requisição. Teste na Task 6.
4. Timestamps lidos do Postgres voltam como UTC e o round-trip domínio → linha → domínio preserva o `State`. Teste
   nas Tasks 7 e 8.
5. `down` seguido de `up` das migrations reconstrói o schema completo e utilizável. Teste na Task 2.

## File Structure

```
docker-compose.yml                         postgres, migrate, postgres_test, migrate_test
deploy/postgres/initdb/01-roles.sh         cria wallet_owner, wallet_app e o banco wallet
migrations/
  000001_create_wallets.{up,down}.sql
  000002_create_wager_transactions.{up,down}.sql
  000003_create_wallet_ledger_entries.{up,down}.sql
  000004_create_inbox_messages.{up,down}.sql
  000005_create_outbox_events.{up,down}.sql
internal/testsupport/pgtest/pgtest.go      URLs de teste, conexão com falha clara, RequirePgError, WithDatabase
internal/app/
  errors.go                                ErrNotFound, ErrConflict, ErrVersionConflict, ErrTransient
  ports.go                                 TxManager e interfaces de repositório
internal/platform/config/
  config.go                                Config, Database, Load(getenv)
  module.go                                fx.Module "config"
internal/adapters/postgres/
  doc.go                                   documentação do pacote
  db.go                                    Open(config.Database) (*gorm.DB, error)
  errors.go                                mapError (pgconn → sentinelas de app)
  tx.go                                    TxManager, conn(ctx)
  models.go                                walletModel, transactionModel, ledgerEntryModel, outboxEventModel
  mapping.go                               domínio ↔ modelos
  wallet_repository.go
  transaction_repository.go
  ledger_repository.go
  outbox_repository.go
  module.go                                fx.Module "postgres" com lifecycle
  *_test.go                                testes unitários e de integração
```

---

### Task 1: Docker Compose do PostgreSQL com papéis

**Files:**
- Create: `docker-compose.yml`, `deploy/postgres/initdb/01-roles.sh`

**Interfaces:**
- Produces:
  - **Dev:** serviço `postgres` (porta `${POSTGRES_PORT:-5432}`, volume `pgdata`).
  - **Test:** serviço `postgres_test` (profile `test`, porta `${POSTGRES_TEST_PORT:-5433}`, tmpfs).
  - **Migrations:** serviços one-shot `migrate` e `migrate_test`.
  - **Banco e papéis:** banco `wallet`, papéis `wallet_owner` (senha `${WALLET_OWNER_PASSWORD:-wallet_owner}`,
    `CREATEDB`) e `wallet_app` (senha `${WALLET_APP_PASSWORD:-wallet_app}`).
  - **URLs de teste:**
    - `postgres://wallet_app:wallet_app@localhost:5433/wallet?sslmode=disable`
    - `postgres://wallet_owner:wallet_owner@localhost:5433/wallet?sslmode=disable`

- [ ] **Step 1: Criar o script de init**

`deploy/postgres/initdb/01-roles.sh`:

```sh
#!/bin/sh
# Runs once, on the first start of an empty data directory.
# Creates the migration owner, the least-privilege application role and the
# wallet database. Table privileges are granted by the migrations.
set -eu

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname "$POSTGRES_DB" <<EOSQL
CREATE ROLE wallet_owner LOGIN CREATEDB PASSWORD '${WALLET_OWNER_PASSWORD}';
CREATE ROLE wallet_app LOGIN PASSWORD '${WALLET_APP_PASSWORD}';
CREATE DATABASE wallet OWNER wallet_owner;
REVOKE ALL ON DATABASE wallet FROM PUBLIC;
GRANT CONNECT ON DATABASE wallet TO wallet_app;
EOSQL

psql -v ON_ERROR_STOP=1 --username "$POSTGRES_USER" --dbname wallet <<EOSQL
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO wallet_app;
EOSQL
```

Run: `chmod +x deploy/postgres/initdb/01-roles.sh`

- [ ] **Step 2: Criar o `docker-compose.yml`**

```yaml
name: wallet

x-postgres-env: &postgres-env
  POSTGRES_USER: postgres
  POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:-postgres}
  POSTGRES_DB: postgres
  WALLET_OWNER_PASSWORD: ${WALLET_OWNER_PASSWORD:-wallet_owner}
  WALLET_APP_PASSWORD: ${WALLET_APP_PASSWORD:-wallet_app}

x-postgres-healthcheck: &postgres-healthcheck
  test: ["CMD-SHELL", "pg_isready -h 127.0.0.1 -U postgres -d wallet"]
  interval: 2s
  timeout: 3s
  retries: 30

services:
  postgres:
    image: postgres:17-alpine
    environment: *postgres-env
    ports:
      - "${POSTGRES_PORT:-5432}:5432"
    volumes:
      - pgdata:/var/lib/postgresql/data
      - ./deploy/postgres/initdb:/docker-entrypoint-initdb.d:ro
    healthcheck: *postgres-healthcheck

  migrate:
    image: migrate/migrate:v4.18.3
    volumes:
      - ./migrations:/migrations:ro
    command:
      - -path=/migrations
      - -database=postgres://wallet_owner:${WALLET_OWNER_PASSWORD:-wallet_owner}@postgres:5432/wallet?sslmode=disable
      - up
    depends_on:
      postgres:
        condition: service_healthy
    restart: "no"

  postgres_test:
    image: postgres:17-alpine
    profiles: ["test"]
    environment: *postgres-env
    ports:
      - "${POSTGRES_TEST_PORT:-5433}:5432"
    tmpfs:
      - /var/lib/postgresql/data
    volumes:
      - ./deploy/postgres/initdb:/docker-entrypoint-initdb.d:ro
    healthcheck: *postgres-healthcheck

  migrate_test:
    image: migrate/migrate:v4.18.3
    profiles: ["test"]
    volumes:
      - ./migrations:/migrations:ro
    command:
      - -path=/migrations
      - -database=postgres://wallet_owner:${WALLET_OWNER_PASSWORD:-wallet_owner}@postgres_test:5432/wallet?sslmode=disable
      - up
    depends_on:
      postgres_test:
        condition: service_healthy
    restart: "no"

volumes:
  pgdata:
```

- [ ] **Step 3: Verificar o banco de teste e os papéis**

Run:

```bash
docker compose config --quiet && echo compose-ok
docker compose --profile test up -d --wait postgres_test
docker compose exec -T postgres_test psql -U postgres -d wallet -Atc \
  "SELECT rolname, rolcreatedb FROM pg_roles WHERE rolname IN ('wallet_owner','wallet_app') ORDER BY 1"
docker compose exec -T postgres_test psql -U postgres -Atc \
  "SELECT pg_get_userbyid(datdba) FROM pg_database WHERE datname = 'wallet'"
```

Expected: `compose-ok`. Em seguida as linhas `wallet_app|f` e `wallet_owner|t`, e depois `wallet_owner`.
(O `migrate_test` ainda não roda: não há migrations até a Task 2.)

- [ ] **Step 4: Commit**

```bash
git add docker-compose.yml deploy/postgres/initdb/01-roles.sh
git commit -m "chore(infra): postgres dev/test in docker compose with owner and app roles" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Suporte a testes de Postgres, migration de wallets e teste up/down/up

**Files:**
- Create: `internal/testsupport/pgtest/pgtest.go`, `internal/adapters/postgres/doc.go`, `migrations/000001_create_wallets.up.sql`, `migrations/000001_create_wallets.down.sql`
- Test: `internal/adapters/postgres/migrations_test.go`, `internal/adapters/postgres/schema_wallets_test.go`

**Interfaces:**
- Produces:
  - URLs: `pgtest.AppURL() string`, `pgtest.OwnerURL() string` (env `TEST_DATABASE_URL`/`TEST_DATABASE_OWNER_URL`,
    com padrões nas URLs da Task 1) e `pgtest.WithDatabase(rawURL, name string) string`.
  - Conexões: `pgtest.Open(t testing.TB, url string) *gorm.DB` (faz ping e falha com mensagem clara),
    `pgtest.AppDB(t) *gorm.DB` e `pgtest.OwnerDB(t) *gorm.DB`.
  - Asserção: `pgtest.RequirePgError(t testing.TB, err error, code, constraint string)`.
  - Constante `pgtest.MigrationsPath = "../../../migrations"`, relativa a `internal/adapters/postgres`.

- [ ] **Step 1: Adicionar dependências**

```bash
go get gorm.io/gorm@latest gorm.io/driver/postgres@latest github.com/golang-migrate/migrate/v4@latest
```

- [ ] **Step 2: Criar o pacote de suporte**

`internal/testsupport/pgtest/pgtest.go`:

```go
// Package pgtest connects integration tests to the postgres_test container
// started with `docker compose --profile test up -d --wait postgres_test`
// and migrated with `docker compose --profile test run --rm migrate_test`.
package pgtest

import (
	"errors"
	"net/url"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

const (
	defaultAppURL   = "postgres://wallet_app:wallet_app@localhost:5433/wallet?sslmode=disable"
	defaultOwnerURL = "postgres://wallet_owner:wallet_owner@localhost:5433/wallet?sslmode=disable"

	// MigrationsPath is the migrations directory relative to internal/adapters/postgres.
	MigrationsPath = "../../../migrations"
)

// AppURL is the least-privilege application connection string.
func AppURL() string { return envOr("TEST_DATABASE_URL", defaultAppURL) }

// OwnerURL is the migration-owner connection string.
func OwnerURL() string { return envOr("TEST_DATABASE_OWNER_URL", defaultOwnerURL) }

// WithDatabase returns rawURL pointing at database name.
func WithDatabase(rawURL, name string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		panic(err) // test configuration bug, never a runtime path
	}
	u.Path = "/" + name
	return u.String()
}

// AppDB opens a connection as wallet_app.
func AppDB(t testing.TB) *gorm.DB { return Open(t, AppURL()) }

// OwnerDB opens a connection as wallet_owner.
func OwnerDB(t testing.TB) *gorm.DB { return Open(t, OwnerURL()) }

// Open connects to url, pings it and closes it at test cleanup. It fails the
// test with setup instructions when the database is unreachable.
func Open(t testing.TB, rawURL string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(postgres.Open(rawURL), &gorm.Config{
		SkipDefaultTransaction: true,
		Logger:                 logger.Discard,
	})
	if err != nil {
		t.Fatalf("postgres de teste indisponível (%v). Suba a infra de teste: "+
			"`docker compose --profile test up -d --wait postgres_test && "+
			"docker compose --profile test run --rm migrate_test`", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

// RequirePgError fails unless err carries a postgres error with code and,
// when constraint is not empty, that constraint name.
func RequirePgError(t testing.TB, err error, code, constraint string) {
	t.Helper()
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("error = %v, want postgres error %s", err, code)
	}
	if pgErr.Code != code || (constraint != "" && pgErr.ConstraintName != constraint) {
		t.Fatalf("postgres error %s constraint %q (%s), want %s constraint %q",
			pgErr.Code, pgErr.ConstraintName, pgErr.Message, code, constraint)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
```

`internal/adapters/postgres/doc.go`:

```go
// Package postgres is the persistence adapter: GORM with explicit locking,
// conflict and version clauses, a context-bound transaction manager and
// mappers between table rows and domain aggregates. Invariants also live in
// the schema (migrations/), so they hold even if this code misbehaves.
package postgres
```

- [ ] **Step 3: Escrever os testes que falham**

`internal/adapters/postgres/migrations_test.go`:

```go
//go:build !unit && !e2e

package postgres

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

// expectedTables grows as later tasks add migrations.
var expectedTables = []string{"wallets"}

// expectedVersion is the latest migration number.
const expectedVersion = 1

func publicTables(t *testing.T, url string) []string {
	t.Helper()
	var names []string
	err := pgtest.Open(t, url).Raw(
		`SELECT table_name FROM information_schema.tables
		 WHERE table_schema = 'public' AND table_name <> 'schema_migrations' ORDER BY table_name`,
	).Scan(&names).Error
	if err != nil {
		t.Fatal(err)
	}
	return names
}

func TestMigrationsUpDownUp(t *testing.T) {
	owner := pgtest.OwnerDB(t)
	name := fmt.Sprintf("wallet_mig_%d", time.Now().UnixNano())
	if err := owner.Exec("CREATE DATABASE " + name).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { owner.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)") })

	url := pgtest.WithDatabase(pgtest.OwnerURL(), name)
	m, err := migrate.New("file://"+pgtest.MigrationsPath, strings.Replace(url, "postgres://", "pgx5://", 1))
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()

	if err := m.Up(); err != nil {
		t.Fatalf("up: %v", err)
	}
	if v, dirty, err := m.Version(); err != nil || dirty || v != expectedVersion {
		t.Fatalf("version = %d dirty %v err %v, want %d clean", v, dirty, err, expectedVersion)
	}
	want := slices.Sorted(slices.Values(expectedTables))
	if got := publicTables(t, url); !slices.Equal(got, want) {
		t.Fatalf("tables after up = %v, want %v", got, want)
	}

	if err := m.Down(); err != nil {
		t.Fatalf("down: %v", err)
	}
	if got := publicTables(t, url); len(got) != 0 {
		t.Fatalf("tables after down = %v, want none", got)
	}

	if err := m.Up(); err != nil {
		t.Fatalf("second up: %v", err)
	}
	if got := publicTables(t, url); !slices.Equal(got, want) {
		t.Fatalf("tables after second up = %v, want %v", got, want)
	}
}
```

`internal/adapters/postgres/schema_wallets_test.go`:

```go
//go:build !unit && !e2e

package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

var schemaNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func walletRow(playerID uuid.UUID, currency string, balance int64) map[string]any {
	return map[string]any{
		"id": uuid.New(), "player_id": playerID, "currency": currency,
		"balance_minor": balance, "version": 1, "created_at": schemaNow, "updated_at": schemaNow,
	}
}

func TestWalletsSchema(t *testing.T) {
	db := pgtest.AppDB(t)
	player := uuid.New()

	if err := db.Table("wallets").Create(walletRow(player, "BRL", 0)).Error; err != nil {
		t.Fatalf("valid wallet: %v", err)
	}
	if err := db.Table("wallets").Create(walletRow(player, "USD", 100)).Error; err != nil {
		t.Fatalf("same player, other currency: %v", err)
	}

	err := db.Table("wallets").Create(walletRow(player, "BRL", 100)).Error
	pgtest.RequirePgError(t, err, "23505", "wallets_player_currency_key")

	err = db.Table("wallets").Create(walletRow(uuid.New(), "BRL", -1)).Error
	pgtest.RequirePgError(t, err, "23514", "wallets_balance_minor_check")

	bad := walletRow(uuid.New(), "BRL", 0)
	bad["version"] = 0
	pgtest.RequirePgError(t, db.Table("wallets").Create(bad).Error, "23514", "wallets_version_check")

	bad = walletRow(uuid.New(), "brl", 0)
	pgtest.RequirePgError(t, db.Table("wallets").Create(bad).Error, "23514", "wallets_currency_check")

	err = db.Exec("DELETE FROM wallets WHERE player_id = ?", player).Error
	pgtest.RequirePgError(t, err, "42501", "")
}
```

- [ ] **Step 4: Rodar e confirmar que falham**

Run: `docker compose --profile test up -d --wait postgres_test && go test -tags=integration ./internal/adapters/postgres/...`
Expected: FAIL. O `TestMigrationsUpDownUp` falha com erro de source (diretório `migrations` inexistente ou vazio), e
o `TestWalletsSchema` falha com `relation "wallets" does not exist` (`42P01`).

- [ ] **Step 5: Criar a migration**

`migrations/000001_create_wallets.up.sql`:

```sql
BEGIN;

CREATE TABLE wallets (
    id            uuid        PRIMARY KEY,
    player_id     uuid        NOT NULL,
    currency      char(3)     NOT NULL CONSTRAINT wallets_currency_check CHECK (currency ~ '^[A-Z]{3}$'),
    balance_minor bigint      NOT NULL CONSTRAINT wallets_balance_minor_check CHECK (balance_minor >= 0),
    version       bigint      NOT NULL CONSTRAINT wallets_version_check CHECK (version >= 1),
    created_at    timestamptz NOT NULL,
    updated_at    timestamptz NOT NULL,
    CONSTRAINT wallets_player_currency_key UNIQUE (player_id, currency)
);

GRANT SELECT, INSERT, UPDATE ON wallets TO wallet_app;

COMMIT;
```

`migrations/000001_create_wallets.down.sql`:

```sql
BEGIN;

DROP TABLE wallets;

COMMIT;
```

- [ ] **Step 6: Aplicar no banco de teste e rodar os testes**

Run:

```bash
docker compose --profile test run --rm migrate_test
go test -race -tags=integration ./internal/adapters/postgres/...
```

Expected: o migrate imprime `1/u create_wallets`; os testes dão `ok`.

- [ ] **Step 7: Commit**

```bash
go mod tidy
git add go.mod go.sum migrations internal/testsupport internal/adapters/postgres
git commit -m "feat(db): wallets migration and postgres test support" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: Migration de wager_transactions — constraints, índices de reversão e trigger de estado

**Files:**
- Create: `migrations/000002_create_wager_transactions.up.sql`, `migrations/000002_create_wager_transactions.down.sql`
- Modify: `internal/adapters/postgres/migrations_test.go` (`expectedTables`, `expectedVersion`)
- Test: `internal/adapters/postgres/schema_transactions_test.go`

**Interfaces:**
- Consumes: `pgtest.*`, `schemaNow` (Task 2).
- Produces: a tabela `wager_transactions`. Nomes de constraint usados pelos testes e pelo Plano 3:
  - CHECKs: `wager_transactions_origin_shape`, `wager_transactions_status_shape`,
    `wager_transactions_failed_code`, `wager_transactions_kind_amount`,
    `wager_transactions_reversal_reference`, `wager_transactions_processed_reversal_resolved`;
  - unicidades: `wager_transactions_provider_idempotency_key`, `wager_transactions_provider_external_id`;
  - índices únicos: `wager_transactions_one_opening`, `wager_transactions_one_reversal_per_kind`,
    `wager_transactions_one_reversal_per_bet`;
  - trigger: `wager_transactions_guard_update`.

- [ ] **Step 1: Escrever o teste que falha**

Atualizar `internal/adapters/postgres/migrations_test.go`:

```go
var expectedTables = []string{"wallets", "wager_transactions"}

const expectedVersion = 2
```

`internal/adapters/postgres/schema_transactions_test.go`:

```go
//go:build !unit && !e2e

package postgres

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

// externalRow is a valid PENDING external operation.
func externalRow(walletID, playerID uuid.UUID, kind string, amount int64) map[string]any {
	ext := uuid.NewString()
	return map[string]any{
		"id": uuid.New(), "origin": "EXTERNAL", "kind": kind, "status": "PENDING",
		"wallet_id": walletID, "player_id": playerID, "amount_minor": amount, "currency": "BRL",
		"provider_id": "provider-a", "external_transaction_id": ext, "idempotency_key": "provider-a:" + ext,
		"payload_hash": strings.Repeat("a", 64), "round_id": "round-1", "game_id": "game-1",
		"attempts": 0, "created_at": schemaNow, "updated_at": schemaNow,
	}
}

func processed(row map[string]any, balance int64) map[string]any {
	row["status"] = "PROCESSED"
	row["result_balance_minor"] = balance
	row["processed_at"] = schemaNow
	return row
}

func reversal(walletID, playerID uuid.UUID, kind string, ref map[string]any) map[string]any {
	row := externalRow(walletID, playerID, kind, ref["amount_minor"].(int64))
	row["reference_external_transaction_id"] = ref["external_transaction_id"]
	row["reference_transaction_id"] = ref["id"]
	row["reference_kind"] = ref["kind"]
	return row
}

func insertTx(db *gorm.DB, row map[string]any) error {
	return db.Table("wager_transactions").Create(row).Error
}

func TestTransactionShapeConstraints(t *testing.T) {
	db := pgtest.AppDB(t)
	w, p := uuid.New(), uuid.New()

	if err := insertTx(db, processed(externalRow(w, p, "BET", 2500), 7500)); err != nil {
		t.Fatalf("valid processed bet: %v", err)
	}
	opening := map[string]any{
		"id": uuid.New(), "origin": "INTERNAL", "kind": "OPENING", "status": "PROCESSED",
		"wallet_id": w, "player_id": p, "amount_minor": int64(10000), "currency": "BRL",
		"result_balance_minor": int64(10000), "attempts": 0,
		"created_at": schemaNow, "updated_at": schemaNow, "processed_at": schemaNow,
	}
	if err := insertTx(db, opening); err != nil {
		t.Fatalf("valid opening: %v", err)
	}

	tests := []struct {
		name       string
		row        func() map[string]any
		code       string
		constraint string
	}{
		{"processed without result balance", func() map[string]any {
			r := processed(externalRow(w, p, "BET", 100), 0)
			delete(r, "result_balance_minor")
			return r
		}, "23514", "wager_transactions_status_shape"},
		{"rejected without failure code", func() map[string]any {
			r := externalRow(w, p, "BET", 100)
			r["status"], r["processed_at"] = "REJECTED", schemaNow
			return r
		}, "23514", "wager_transactions_status_shape"},
		{"pending reference without next attempt", func() map[string]any {
			r := externalRow(w, p, "REFUND", 100)
			r["reference_external_transaction_id"] = "x"
			r["status"] = "PENDING_REFERENCE"
			return r
		}, "23514", "wager_transactions_status_shape"},
		{"failed with business code", func() map[string]any {
			r := externalRow(w, p, "BET", 100)
			r["status"], r["failure_code"], r["processed_at"] = "FAILED", "INSUFFICIENT_FUNDS", schemaNow
			return r
		}, "23514", "wager_transactions_failed_code"},
		{"external without provider", func() map[string]any {
			r := externalRow(w, p, "BET", 100)
			delete(r, "provider_id")
			return r
		}, "23514", "wager_transactions_origin_shape"},
		{"internal with provider", func() map[string]any {
			r := externalRow(uuid.New(), p, "OPENING", 100)
			r["origin"] = "INTERNAL"
			return r
		}, "23514", "wager_transactions_origin_shape"},
		{"external opening", func() map[string]any {
			return externalRow(w, p, "OPENING", 100)
		}, "23514", "wager_transactions_origin_shape"},
		{"loss with amount", func() map[string]any {
			return externalRow(w, p, "LOSS", 100)
		}, "23514", "wager_transactions_kind_amount"},
		{"bet with zero", func() map[string]any {
			return externalRow(w, p, "BET", 0)
		}, "23514", "wager_transactions_kind_amount"},
		{"refund without reference", func() map[string]any {
			return externalRow(w, p, "REFUND", 100)
		}, "23514", "wager_transactions_reversal_reference"},
		{"processed refund unresolved", func() map[string]any {
			r := processed(externalRow(w, p, "REFUND", 100), 100)
			r["reference_external_transaction_id"] = "x"
			return r
		}, "23514", "wager_transactions_processed_reversal_resolved"},
		// Also fails status_shape; which violation is reported first is not
		// guaranteed, so only the code is asserted.
		{"unknown status", func() map[string]any {
			r := externalRow(w, p, "BET", 100)
			r["status"] = "DONE"
			return r
		}, "23514", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgtest.RequirePgError(t, insertTx(db, tt.row()), tt.code, tt.constraint)
		})
	}
}

func TestTransactionUniqueness(t *testing.T) {
	db := pgtest.AppDB(t)
	w, p := uuid.New(), uuid.New()

	base := externalRow(w, p, "BET", 100)
	if err := insertTx(db, base); err != nil {
		t.Fatal(err)
	}
	sameKey := externalRow(w, p, "BET", 100)
	sameKey["idempotency_key"] = base["idempotency_key"]
	pgtest.RequirePgError(t, insertTx(db, sameKey), "23505", "wager_transactions_provider_idempotency_key")

	sameExt := externalRow(w, p, "BET", 100)
	sameExt["external_transaction_id"] = base["external_transaction_id"]
	pgtest.RequirePgError(t, insertTx(db, sameExt), "23505", "wager_transactions_provider_external_id")

	otherProvider := externalRow(w, p, "BET", 100)
	otherProvider["provider_id"] = "provider-b"
	otherProvider["idempotency_key"] = base["idempotency_key"]
	otherProvider["external_transaction_id"] = base["external_transaction_id"]
	if err := insertTx(db, otherProvider); err != nil {
		t.Fatalf("keys are scoped by provider: %v", err)
	}

	open := func() map[string]any {
		return map[string]any{
			"id": uuid.New(), "origin": "INTERNAL", "kind": "OPENING", "status": "PROCESSED",
			"wallet_id": w, "player_id": p, "amount_minor": int64(100), "currency": "BRL",
			"result_balance_minor": int64(100), "attempts": 0,
			"created_at": schemaNow, "updated_at": schemaNow, "processed_at": schemaNow,
		}
	}
	if err := insertTx(db, open()); err != nil {
		t.Fatal(err)
	}
	pgtest.RequirePgError(t, insertTx(db, open()), "23505", "wager_transactions_one_opening")
}

func TestReversalUniqueness(t *testing.T) {
	db := pgtest.AppDB(t)
	w, p := uuid.New(), uuid.New()

	bet := processed(externalRow(w, p, "BET", 3000), 7000)
	win := processed(externalRow(w, p, "WIN", 5000), 12000)
	for _, r := range []map[string]any{bet, win} {
		if err := insertTx(db, r); err != nil {
			t.Fatal(err)
		}
	}

	rejected := reversal(w, p, "REFUND", bet)
	rejected["status"], rejected["failure_code"], rejected["processed_at"] = "REJECTED", "AMOUNT_MISMATCH", schemaNow
	if err := insertTx(db, rejected); err != nil {
		t.Fatalf("rejected reversals do not count: %v", err)
	}
	if err := insertTx(db, processed(reversal(w, p, "REFUND", bet), 10000)); err != nil {
		t.Fatalf("first refund: %v", err)
	}
	err := insertTx(db, processed(reversal(w, p, "ROLLBACK", bet), 13000))
	pgtest.RequirePgError(t, err, "23505", "wager_transactions_one_reversal_per_bet")

	if err := insertTx(db, processed(reversal(w, p, "ROLLBACK", win), 5000)); err != nil {
		t.Fatalf("first rollback of win: %v", err)
	}
	err = insertTx(db, processed(reversal(w, p, "ROLLBACK", win), 0))
	pgtest.RequirePgError(t, err, "23505", "wager_transactions_one_reversal_per_kind")
}

func TestTransactionUpdateGuard(t *testing.T) {
	db := pgtest.AppDB(t)
	w, p := uuid.New(), uuid.New()
	update := func(id any, values map[string]any) error {
		return db.Table("wager_transactions").Where("id = ?", id).Updates(values).Error
	}

	pending := externalRow(w, p, "REFUND", 100)
	pending["reference_external_transaction_id"] = "later"
	if err := insertTx(db, pending); err != nil {
		t.Fatal(err)
	}
	if err := update(pending["id"], map[string]any{"status": "PENDING_REFERENCE", "next_attempt_at": schemaNow, "attempts": 1}); err != nil {
		t.Fatalf("PENDING -> PENDING_REFERENCE: %v", err)
	}
	err := update(pending["id"], map[string]any{"status": "PENDING"})
	pgtest.RequirePgError(t, err, "23514", "")

	err = update(pending["id"], map[string]any{"amount_minor": int64(999)})
	pgtest.RequirePgError(t, err, "23514", "")

	done := processed(externalRow(w, p, "BET", 100), 0)
	if err := insertTx(db, done); err != nil {
		t.Fatal(err)
	}
	err = update(done["id"], map[string]any{"attempts": 5})
	pgtest.RequirePgError(t, err, "23514", "")

	err = db.Exec("DELETE FROM wager_transactions WHERE id = ?", done["id"]).Error
	pgtest.RequirePgError(t, err, "42501", "")
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test -tags=integration ./internal/adapters/postgres/...`
Expected: FAIL, `relation "wager_transactions" does not exist`; e o teste de migrations falha porque espera a versão 2.

- [ ] **Step 3: Criar a migration**

`migrations/000002_create_wager_transactions.up.sql`:

```sql
BEGIN;

CREATE TABLE wager_transactions (
    id                                uuid         PRIMARY KEY,
    origin                            text         NOT NULL CONSTRAINT wager_transactions_origin_check
                                                   CHECK (origin IN ('INTERNAL', 'EXTERNAL')),
    kind                              text         NOT NULL CONSTRAINT wager_transactions_kind_check
                                                   CHECK (kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK')),
    status                            text         NOT NULL CONSTRAINT wager_transactions_status_check
                                                   CHECK (status IN ('PENDING', 'PENDING_REFERENCE', 'PROCESSED', 'REJECTED', 'FAILED')),
    -- No FK: a WALLET_NOT_FOUND rejection keeps the requested wallet id for audit.
    wallet_id                         uuid         NOT NULL,
    player_id                         uuid         NOT NULL,
    amount_minor                      bigint       NOT NULL CONSTRAINT wager_transactions_amount_minor_check
                                                   CHECK (amount_minor >= 0),
    currency                          char(3)      NOT NULL CONSTRAINT wager_transactions_currency_check
                                                   CHECK (currency ~ '^[A-Z]{3}$'),
    provider_id                       varchar(255),
    external_transaction_id           varchar(255),
    idempotency_key                   varchar(255),
    payload_hash                      char(64),
    round_id                          varchar(255),
    game_id                           varchar(255),
    reference_external_transaction_id varchar(255),
    reference_transaction_id          uuid         REFERENCES wager_transactions (id),
    reference_kind                    text         CONSTRAINT wager_transactions_reference_kind_check
                                                   CHECK (reference_kind IN ('OPENING', 'BET', 'WIN', 'LOSS', 'REFUND', 'ROLLBACK')),
    failure_code                      text,
    result_balance_minor              bigint,
    attempts                          integer      NOT NULL DEFAULT 0 CONSTRAINT wager_transactions_attempts_check
                                                   CHECK (attempts >= 0),
    next_attempt_at                   timestamptz,
    created_at                        timestamptz  NOT NULL,
    updated_at                        timestamptz  NOT NULL,
    processed_at                      timestamptz,

    CONSTRAINT wager_transactions_origin_shape CHECK (
        (origin = 'INTERNAL' AND kind = 'OPENING'
            AND provider_id IS NULL AND external_transaction_id IS NULL AND idempotency_key IS NULL
            AND payload_hash IS NULL AND round_id IS NULL AND game_id IS NULL
            AND reference_external_transaction_id IS NULL)
        OR
        (origin = 'EXTERNAL' AND kind <> 'OPENING'
            AND provider_id IS NOT NULL AND external_transaction_id IS NOT NULL AND idempotency_key IS NOT NULL
            AND payload_hash IS NOT NULL AND round_id IS NOT NULL AND game_id IS NOT NULL)
    ),
    CONSTRAINT wager_transactions_status_shape CHECK (
        (status = 'PENDING' AND failure_code IS NULL)
        OR (status = 'PENDING_REFERENCE' AND failure_code IS NULL AND next_attempt_at IS NOT NULL)
        OR (status = 'PROCESSED' AND failure_code IS NULL AND result_balance_minor IS NOT NULL
            AND processed_at IS NOT NULL)
        OR (status IN ('REJECTED', 'FAILED') AND failure_code IS NOT NULL AND processed_at IS NOT NULL)
    ),
    CONSTRAINT wager_transactions_failed_code CHECK (
        status <> 'FAILED' OR failure_code = 'INFRASTRUCTURE_FAILURE'
    ),
    CONSTRAINT wager_transactions_kind_amount CHECK (
        (kind = 'LOSS' AND amount_minor = 0) OR (kind <> 'LOSS' AND amount_minor > 0)
    ),
    CONSTRAINT wager_transactions_reversal_reference CHECK (
        kind NOT IN ('REFUND', 'ROLLBACK') OR reference_external_transaction_id IS NOT NULL
    ),
    CONSTRAINT wager_transactions_processed_reversal_resolved CHECK (
        status <> 'PROCESSED' OR kind NOT IN ('REFUND', 'ROLLBACK')
        OR (reference_transaction_id IS NOT NULL AND reference_kind IS NOT NULL)
    ),
    CONSTRAINT wager_transactions_provider_idempotency_key UNIQUE (provider_id, idempotency_key),
    CONSTRAINT wager_transactions_provider_external_id UNIQUE (provider_id, external_transaction_id)
);

-- One OPENING credit per wallet.
CREATE UNIQUE INDEX wager_transactions_one_opening
    ON wager_transactions (wallet_id) WHERE kind = 'OPENING';

-- A reference never receives two successful reversals of the same kind.
CREATE UNIQUE INDEX wager_transactions_one_reversal_per_kind
    ON wager_transactions (reference_transaction_id, kind)
    WHERE status = 'PROCESSED' AND kind IN ('REFUND', 'ROLLBACK');

-- A BET is reversed (REFUND or ROLLBACK) at most once, so its debit is never returned twice.
CREATE UNIQUE INDEX wager_transactions_one_reversal_per_bet
    ON wager_transactions (reference_transaction_id)
    WHERE status = 'PROCESSED' AND kind IN ('REFUND', 'ROLLBACK') AND reference_kind = 'BET';

CREATE INDEX wager_transactions_pending_reference
    ON wager_transactions (next_attempt_at) WHERE status = 'PENDING_REFERENCE';

-- Enforces the state machine and the immutability of identity columns.
CREATE FUNCTION wager_transactions_guard_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF OLD.status IN ('PROCESSED', 'REJECTED', 'FAILED') THEN
        RAISE EXCEPTION 'wager transaction % is terminal (%)', OLD.id, OLD.status
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.status IS DISTINCT FROM OLD.status AND NOT (
           (OLD.status = 'PENDING' AND NEW.status IN ('PROCESSED', 'REJECTED', 'PENDING_REFERENCE', 'FAILED'))
        OR (OLD.status = 'PENDING_REFERENCE' AND NEW.status IN ('PROCESSED', 'REJECTED', 'FAILED'))
    ) THEN
        RAISE EXCEPTION 'invalid wager transaction transition % -> %', OLD.status, NEW.status
            USING ERRCODE = 'check_violation';
    END IF;
    IF (NEW.id, NEW.origin, NEW.kind, NEW.wallet_id, NEW.player_id, NEW.amount_minor, NEW.currency,
        NEW.provider_id, NEW.external_transaction_id, NEW.idempotency_key, NEW.payload_hash,
        NEW.round_id, NEW.game_id, NEW.reference_external_transaction_id, NEW.created_at)
       IS DISTINCT FROM
       (OLD.id, OLD.origin, OLD.kind, OLD.wallet_id, OLD.player_id, OLD.amount_minor, OLD.currency,
        OLD.provider_id, OLD.external_transaction_id, OLD.idempotency_key, OLD.payload_hash,
        OLD.round_id, OLD.game_id, OLD.reference_external_transaction_id, OLD.created_at)
    THEN
        RAISE EXCEPTION 'identity columns of wager transaction % are immutable', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER wager_transactions_guard_update
    BEFORE UPDATE ON wager_transactions
    FOR EACH ROW EXECUTE FUNCTION wager_transactions_guard_update();

GRANT SELECT, INSERT, UPDATE ON wager_transactions TO wallet_app;

COMMIT;
```

`migrations/000002_create_wager_transactions.down.sql`:

```sql
BEGIN;

DROP TABLE wager_transactions;
DROP FUNCTION wager_transactions_guard_update();

COMMIT;
```

- [ ] **Step 4: Aplicar e rodar os testes**

Run:

```bash
docker compose --profile test run --rm migrate_test
go test -race -tags=integration ./internal/adapters/postgres/...
```

Expected: `2/u create_wager_transactions`, e os testes dão `ok`.

- [ ] **Step 5: Commit**

```bash
git add migrations internal/adapters/postgres
git commit -m "feat(db): wager_transactions with shape checks, reversal uniqueness and state trigger" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Migration do ledger append-only

**Files:**
- Create: `migrations/000003_create_wallet_ledger_entries.up.sql`, `migrations/000003_create_wallet_ledger_entries.down.sql`
- Modify: `internal/adapters/postgres/migrations_test.go`
- Test: `internal/adapters/postgres/schema_ledger_test.go`

**Interfaces:**
- Consumes: `walletRow`, `externalRow`, `processed`, `insertTx` e `schemaNow` (Tasks 2–3).
- Produces: a tabela `wallet_ledger_entries`, com as constraints `wallet_ledger_entries_balance_math` e
  `wallet_ledger_entries_wallet_transaction_key`, o índice `wallet_ledger_entries_wallet_id_id (wallet_id, id)` e os
  triggers `wallet_ledger_entries_no_update_delete` e `wallet_ledger_entries_no_truncate`.

- [ ] **Step 1: Escrever o teste que falha**

Atualizar `internal/adapters/postgres/migrations_test.go`:

```go
var expectedTables = []string{"wallets", "wager_transactions", "wallet_ledger_entries"}

const expectedVersion = 3
```

`internal/adapters/postgres/schema_ledger_test.go`:

```go
//go:build !unit && !e2e

package postgres

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

// seedWalletAndBet inserts a wallet and a processed BET row and returns their ids.
func seedWalletAndBet(t *testing.T, db *gorm.DB) (walletID, txID uuid.UUID) {
	t.Helper()
	w := walletRow(uuid.New(), "BRL", 7500)
	if err := db.Table("wallets").Create(w).Error; err != nil {
		t.Fatal(err)
	}
	bet := processed(externalRow(w["id"].(uuid.UUID), w["player_id"].(uuid.UUID), "BET", 2500), 7500)
	if err := insertTx(db, bet); err != nil {
		t.Fatal(err)
	}
	return w["id"].(uuid.UUID), bet["id"].(uuid.UUID)
}

func ledgerRow(walletID, txID uuid.UUID, direction string, amount, before, after int64) map[string]any {
	return map[string]any{
		"id": uuid.New(), "wallet_id": walletID, "transaction_id": txID, "direction": direction,
		"amount_minor": amount, "currency": "BRL", "balance_before_minor": before,
		"balance_after_minor": after, "created_at": schemaNow,
	}
}

func TestLedgerConstraints(t *testing.T) {
	db := pgtest.AppDB(t)
	walletID, txID := seedWalletAndBet(t, db)

	entry := ledgerRow(walletID, txID, "DEBIT", 2500, 10000, 7500)
	if err := db.Table("wallet_ledger_entries").Create(entry).Error; err != nil {
		t.Fatalf("valid entry: %v", err)
	}
	dup := ledgerRow(walletID, txID, "DEBIT", 2500, 10000, 7500)
	pgtest.RequirePgError(t, db.Table("wallet_ledger_entries").Create(dup).Error,
		"23505", "wallet_ledger_entries_wallet_transaction_key")

	_, otherTx := seedWalletAndBet(t, db)
	tests := []struct {
		name       string
		row        map[string]any
		constraint string
	}{
		{"wrong debit math", ledgerRow(walletID, otherTx, "DEBIT", 2500, 10000, 8000), "wallet_ledger_entries_balance_math"},
		{"credit math on debit", ledgerRow(walletID, otherTx, "DEBIT", 2500, 10000, 12500), "wallet_ledger_entries_balance_math"},
		{"zero amount", ledgerRow(walletID, otherTx, "CREDIT", 0, 100, 100), "wallet_ledger_entries_amount_minor_check"},
		{"negative after", ledgerRow(walletID, otherTx, "DEBIT", 200, 100, -100), "wallet_ledger_entries_balance_after_minor_check"},
		// An unknown direction also fails balance_math; the first violated
		// constraint reported is not guaranteed, so only the code is asserted.
		{"bad direction", ledgerRow(walletID, otherTx, "SIDEWAYS", 100, 0, 100), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pgtest.RequirePgError(t, db.Table("wallet_ledger_entries").Create(tt.row).Error, "23514", tt.constraint)
		})
	}

	missing := ledgerRow(walletID, uuid.New(), "CREDIT", 100, 0, 100)
	pgtest.RequirePgError(t, db.Table("wallet_ledger_entries").Create(missing).Error,
		"23503", "wallet_ledger_entries_transaction_id_fkey")
}

func TestLedgerIsAppendOnly(t *testing.T) {
	app := pgtest.AppDB(t)
	walletID, txID := seedWalletAndBet(t, app)
	entry := ledgerRow(walletID, txID, "DEBIT", 2500, 10000, 7500)
	if err := app.Table("wallet_ledger_entries").Create(entry).Error; err != nil {
		t.Fatal(err)
	}
	id := entry["id"]

	pgtest.RequirePgError(t, app.Exec("UPDATE wallet_ledger_entries SET amount_minor = 1 WHERE id = ?", id).Error, "42501", "")
	pgtest.RequirePgError(t, app.Exec("DELETE FROM wallet_ledger_entries WHERE id = ?", id).Error, "42501", "")
	pgtest.RequirePgError(t, app.Exec("TRUNCATE wallet_ledger_entries").Error, "42501", "")

	owner := pgtest.OwnerDB(t)
	for name, sql := range map[string]string{
		"owner update":   "UPDATE wallet_ledger_entries SET amount_minor = 1 WHERE id = ?",
		"owner delete":   "DELETE FROM wallet_ledger_entries WHERE id = ?",
		"owner truncate": "TRUNCATE wallet_ledger_entries",
	} {
		t.Run(name, func(t *testing.T) {
			var err error
			if strings.Contains(sql, "?") {
				err = owner.Exec(sql, id).Error
			} else {
				err = owner.Exec(sql).Error
			}
			pgtest.RequirePgError(t, err, "P0001", "")
			if !strings.Contains(err.Error(), "append-only") {
				t.Fatalf("error = %v, want append-only message", err)
			}
		})
	}

	var count int64
	if err := app.Table("wallet_ledger_entries").Where("id = ?", id).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("entry must survive: count %d err %v", count, err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test -tags=integration ./internal/adapters/postgres/...`
Expected: FAIL, `relation "wallet_ledger_entries" does not exist`.

- [ ] **Step 3: Criar a migration**

`migrations/000003_create_wallet_ledger_entries.up.sql`:

```sql
BEGIN;

CREATE TABLE wallet_ledger_entries (
    id                   uuid        PRIMARY KEY,
    wallet_id            uuid        NOT NULL REFERENCES wallets (id),
    transaction_id       uuid        NOT NULL REFERENCES wager_transactions (id),
    direction            text        NOT NULL CONSTRAINT wallet_ledger_entries_direction_check
                                     CHECK (direction IN ('DEBIT', 'CREDIT')),
    amount_minor         bigint      NOT NULL CONSTRAINT wallet_ledger_entries_amount_minor_check
                                     CHECK (amount_minor > 0),
    currency             char(3)     NOT NULL,
    balance_before_minor bigint      NOT NULL CONSTRAINT wallet_ledger_entries_balance_before_minor_check
                                     CHECK (balance_before_minor >= 0),
    balance_after_minor  bigint      NOT NULL CONSTRAINT wallet_ledger_entries_balance_after_minor_check
                                     CHECK (balance_after_minor >= 0),
    created_at           timestamptz NOT NULL,
    CONSTRAINT wallet_ledger_entries_balance_math CHECK (
        (direction = 'CREDIT' AND balance_after_minor = balance_before_minor + amount_minor)
        OR (direction = 'DEBIT' AND balance_after_minor = balance_before_minor - amount_minor)
    ),
    CONSTRAINT wallet_ledger_entries_wallet_transaction_key UNIQUE (wallet_id, transaction_id)
);

-- Stable cursor pagination per wallet (ids are UUIDv7).
CREATE INDEX wallet_ledger_entries_wallet_id_id ON wallet_ledger_entries (wallet_id, id);

CREATE FUNCTION wallet_ledger_entries_forbid_change() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION 'wallet_ledger_entries is append-only: % is not allowed', TG_OP;
END;
$$;

CREATE TRIGGER wallet_ledger_entries_no_update_delete
    BEFORE UPDATE OR DELETE ON wallet_ledger_entries
    FOR EACH ROW EXECUTE FUNCTION wallet_ledger_entries_forbid_change();

CREATE TRIGGER wallet_ledger_entries_no_truncate
    BEFORE TRUNCATE ON wallet_ledger_entries
    FOR EACH STATEMENT EXECUTE FUNCTION wallet_ledger_entries_forbid_change();

GRANT SELECT, INSERT ON wallet_ledger_entries TO wallet_app;

COMMIT;
```

`migrations/000003_create_wallet_ledger_entries.down.sql`:

```sql
BEGIN;

DROP TABLE wallet_ledger_entries;
DROP FUNCTION wallet_ledger_entries_forbid_change();

COMMIT;
```

- [ ] **Step 4: Aplicar e rodar os testes**

Run:

```bash
docker compose --profile test run --rm migrate_test
go test -race -tags=integration ./internal/adapters/postgres/...
```

Expected: `3/u create_wallet_ledger_entries`, e os testes dão `ok`.

- [ ] **Step 5: Commit**

```bash
git add migrations internal/adapters/postgres
git commit -m "feat(db): append-only wallet ledger with balance math check and protection triggers" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Migrations de inbox e outbox

**Files:**
- Create: `migrations/000004_create_inbox_messages.{up,down}.sql`, `migrations/000005_create_outbox_events.{up,down}.sql`
- Modify: `internal/adapters/postgres/migrations_test.go`
- Test: `internal/adapters/postgres/schema_messaging_test.go`

**Interfaces:**
- Consumes: `schemaNow`, `pgtest.*`.
- Produces:
  - Tabela `inbox_messages`, com PK `inbox_messages_pkey (consumer_name, message_id)`.
  - Tabela `outbox_events`, com o índice parcial `outbox_events_unpublished (next_attempt_at) WHERE published_at IS NULL`
    e o trigger `outbox_events_guard_update`, que deixa imutáveis `id`, `aggregate_type`, `aggregate_id`,
    `event_type`, `payload` e `occurred_at`.

- [ ] **Step 1: Escrever o teste que falha**

Atualizar `internal/adapters/postgres/migrations_test.go`:

```go
var expectedTables = []string{"wallets", "wager_transactions", "wallet_ledger_entries", "inbox_messages", "outbox_events"}

const expectedVersion = 5
```

`internal/adapters/postgres/schema_messaging_test.go`:

```go
//go:build !unit && !e2e

package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func TestInboxSchema(t *testing.T) {
	db := pgtest.AppDB(t)
	row := func(messageID string) map[string]any {
		return map[string]any{
			"consumer_name": "wager-transactions", "message_id": messageID,
			"payload_hash": strings.Repeat("b", 64), "received_at": schemaNow, "processed_at": schemaNow,
		}
	}
	id := uuid.NewString()
	if err := db.Table("inbox_messages").Create(row(id)).Error; err != nil {
		t.Fatal(err)
	}
	pgtest.RequirePgError(t, db.Table("inbox_messages").Create(row(id)).Error, "23505", "inbox_messages_pkey")
	other := row(id)
	other["consumer_name"] = "another-consumer"
	if err := db.Table("inbox_messages").Create(other).Error; err != nil {
		t.Fatalf("same message id for another consumer: %v", err)
	}
	pgtest.RequirePgError(t, db.Exec("DELETE FROM inbox_messages WHERE message_id = ?", id).Error, "42501", "")
	pgtest.RequirePgError(t, db.Exec("UPDATE inbox_messages SET payload_hash = 'x' WHERE message_id = ?", id).Error, "42501", "")
}

func TestOutboxSchema(t *testing.T) {
	db := pgtest.AppDB(t)
	id := uuid.New()
	row := map[string]any{
		"id": id, "aggregate_type": "Wallet", "aggregate_id": uuid.New(), "event_type": "WalletBalanceChanged",
		"payload": `{"eventId":"x"}`, "occurred_at": schemaNow, "attempts": 0, "next_attempt_at": schemaNow,
	}
	if err := db.Table("outbox_events").Create(row).Error; err != nil {
		t.Fatal(err)
	}

	lease := map[string]any{"locked_by": "instance-1", "locked_until": schemaNow, "attempts": 1}
	if err := db.Table("outbox_events").Where("id = ?", id).Updates(lease).Error; err != nil {
		t.Fatalf("lease columns are mutable: %v", err)
	}
	published := map[string]any{"published_at": schemaNow, "last_error": nil}
	if err := db.Table("outbox_events").Where("id = ?", id).Updates(published).Error; err != nil {
		t.Fatalf("publication columns are mutable: %v", err)
	}

	for name, values := range map[string]map[string]any{
		"payload":    {"payload": `{"eventId":"y"}`},
		"event type": {"event_type": "Other"},
		"aggregate":  {"aggregate_id": uuid.New()},
		"occurred":   {"occurred_at": schemaNow.Add(time.Second)},
	} {
		t.Run(name, func(t *testing.T) {
			err := db.Table("outbox_events").Where("id = ?", id).Updates(values).Error
			pgtest.RequirePgError(t, err, "23514", "")
		})
	}
	pgtest.RequirePgError(t, db.Exec("DELETE FROM outbox_events WHERE id = ?", id).Error, "42501", "")
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test -tags=integration ./internal/adapters/postgres/...`
Expected: FAIL, `relation "inbox_messages" does not exist`.

- [ ] **Step 3: Criar as migrations**

`migrations/000004_create_inbox_messages.up.sql`:

```sql
BEGIN;

-- One row per (consumer, message) whose handling committed. It is written in
-- the same SQL transaction as the domain changes it caused.
CREATE TABLE inbox_messages (
    consumer_name varchar(100) NOT NULL,
    message_id    varchar(255) NOT NULL,
    payload_hash  char(64)     NOT NULL,
    received_at   timestamptz  NOT NULL,
    processed_at  timestamptz  NOT NULL,
    CONSTRAINT inbox_messages_pkey PRIMARY KEY (consumer_name, message_id)
);

GRANT SELECT, INSERT ON inbox_messages TO wallet_app;

COMMIT;
```

`migrations/000004_create_inbox_messages.down.sql`:

```sql
BEGIN;

DROP TABLE inbox_messages;

COMMIT;
```

`migrations/000005_create_outbox_events.up.sql`:

```sql
BEGIN;

CREATE TABLE outbox_events (
    id              uuid         PRIMARY KEY,
    aggregate_type  varchar(50)  NOT NULL,
    aggregate_id    uuid         NOT NULL,
    event_type      varchar(100) NOT NULL,
    payload         jsonb        NOT NULL,
    occurred_at     timestamptz  NOT NULL,
    attempts        integer      NOT NULL DEFAULT 0 CONSTRAINT outbox_events_attempts_check CHECK (attempts >= 0),
    next_attempt_at timestamptz  NOT NULL,
    locked_by       varchar(255),
    locked_until    timestamptz,
    published_at    timestamptz,
    last_error      text
);

CREATE INDEX outbox_events_unpublished ON outbox_events (next_attempt_at) WHERE published_at IS NULL;

-- The event snapshot is immutable; only delivery bookkeeping may change.
CREATE FUNCTION outbox_events_guard_update() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.id, NEW.aggregate_type, NEW.aggregate_id, NEW.event_type, NEW.payload, NEW.occurred_at)
       IS DISTINCT FROM
       (OLD.id, OLD.aggregate_type, OLD.aggregate_id, OLD.event_type, OLD.payload, OLD.occurred_at)
    THEN
        RAISE EXCEPTION 'outbox event % snapshot is immutable', OLD.id
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER outbox_events_guard_update
    BEFORE UPDATE ON outbox_events
    FOR EACH ROW EXECUTE FUNCTION outbox_events_guard_update();

GRANT SELECT, INSERT, UPDATE ON outbox_events TO wallet_app;

COMMIT;
```

`migrations/000005_create_outbox_events.down.sql`:

```sql
BEGIN;

DROP TABLE outbox_events;
DROP FUNCTION outbox_events_guard_update();

COMMIT;
```

- [ ] **Step 4: Aplicar e rodar os testes**

Run:

```bash
docker compose --profile test run --rm migrate_test
go test -race -tags=integration ./internal/adapters/postgres/...
```

Expected: `4/u create_inbox_messages`, `5/u create_outbox_events`, e os testes dão `ok`.

- [ ] **Step 5: Commit**

```bash
git add migrations internal/adapters/postgres
git commit -m "feat(db): inbox and outbox tables with immutable event snapshot" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Ports da aplicação, config, conexão, TxManager e classificação de erros

**Files:**
- Create: `internal/app/errors.go`, `internal/app/ports.go`, `internal/platform/config/config.go`, `internal/adapters/postgres/db.go`, `internal/adapters/postgres/errors.go`, `internal/adapters/postgres/tx.go`
- Test: `internal/platform/config/config_test.go` (unit), `internal/adapters/postgres/errors_test.go` (unit), `internal/adapters/postgres/tx_test.go` (integration)

**Interfaces:**
- Produces:
  - `app.ErrNotFound`, `app.ErrConflict`, `app.ErrVersionConflict` e `app.ErrTransient`, todos classificáveis com
    `errors.Is`.
  - Interfaces:
    - `app.TxManager{ WithinTx(ctx context.Context, fn func(ctx context.Context) error) error }`
    - `app.WalletRepository`
    - `app.TransactionRepository`
    - `app.LedgerRepository`
    - `app.OutboxRepository`
    (assinaturas abaixo)
  - Config: `config.Config{Database Database}` e
    `config.Database{URL string; MaxOpenConns int; LockTimeout, StatementTimeout time.Duration}`, carregados por
    `config.Load(getenv func(string) string) (Config, error)`. Variáveis de ambiente: `DATABASE_URL` (obrigatória),
    `DB_MAX_OPEN_CONNS` (padrão 20), `DB_LOCK_TIMEOUT` (padrão 5s) e `DB_STATEMENT_TIMEOUT` (padrão 10s).
  - Adapter:
    - `postgres.Open(cfg config.Database) (*gorm.DB, error)`
    - `postgres.NewTxManager(db *gorm.DB, cfg config.Config) *TxManager`
    - `(*TxManager).WithinTx`
    - os unexported `conn(ctx context.Context, db *gorm.DB) *gorm.DB`, `txFrom(ctx context.Context) (*gorm.DB, bool)`
      e `mapError(err error) error`.

- [ ] **Step 1: Adicionar dependências**

```bash
go get github.com/jackc/pgx/v5@latest go.uber.org/fx@latest
```

- [ ] **Step 2: Escrever os testes unitários que falham**

`internal/platform/config/config_test.go`:

```go
//go:build !integration && !e2e

package config

import (
	"strings"
	"testing"
	"time"
)

func env(values map[string]string) func(string) string {
	return func(k string) string { return values[k] }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	want := Database{URL: "postgres://x", MaxOpenConns: 20, LockTimeout: 5 * time.Second, StatementTimeout: 10 * time.Second}
	if cfg.Database != want {
		t.Fatalf("Database = %+v, want %+v", cfg.Database, want)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://y", "DB_MAX_OPEN_CONNS": "7",
		"DB_LOCK_TIMEOUT": "250ms", "DB_STATEMENT_TIMEOUT": "3s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Database.MaxOpenConns != 7 || cfg.Database.LockTimeout != 250*time.Millisecond ||
		cfg.Database.StatementTimeout != 3*time.Second {
		t.Fatalf("Database = %+v", cfg.Database)
	}
}

func TestLoadInvalid(t *testing.T) {
	_, err := Load(env(map[string]string{
		"DB_MAX_OPEN_CONNS": "zero", "DB_LOCK_TIMEOUT": "-1s", "DB_STATEMENT_TIMEOUT": "soon",
	}))
	if err == nil {
		t.Fatal("Load succeeded, want error")
	}
	for _, key := range []string{"DATABASE_URL", "DB_MAX_OPEN_CONNS", "DB_LOCK_TIMEOUT", "DB_STATEMENT_TIMEOUT"} {
		if !strings.Contains(err.Error(), key) {
			t.Errorf("error %q does not mention %s", err, key)
		}
	}
}
```

`internal/adapters/postgres/errors_test.go`:

```go
//go:build !integration && !e2e

package postgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
)

func TestMapError(t *testing.T) {
	pg := func(code string) error {
		return fmt.Errorf("wrapped: %w", &pgconn.PgError{Code: code, ConstraintName: "some_constraint"})
	}
	tests := []struct {
		name string
		in   error
		want error
	}{
		{"not found", gorm.ErrRecordNotFound, app.ErrNotFound},
		{"unique violation", pg("23505"), app.ErrConflict},
		{"lock timeout", pg("55P03"), app.ErrTransient},
		{"serialization", pg("40001"), app.ErrTransient},
		{"deadlock", pg("40P01"), app.ErrTransient},
		{"statement timeout", pg("57014"), app.ErrTransient},
		{"admin shutdown", pg("57P01"), app.ErrTransient},
		{"too many connections", pg("53300"), app.ErrTransient},
		{"connection exception class", pg("08006"), app.ErrTransient},
		{"bad conn", driver.ErrBadConn, app.ErrTransient},
		{"deadline", context.DeadlineExceeded, app.ErrTransient},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapError(tt.in)
			if !errors.Is(got, tt.want) {
				t.Fatalf("mapError(%v) = %v, want errors.Is %v", tt.in, got, tt.want)
			}
			if !errors.Is(got, tt.in) {
				t.Fatalf("mapError must keep the original error in the chain: %v", got)
			}
		})
	}

	for _, permanent := range []error{pg("23514"), pg("42501"), pg("P0001"), errors.New("boom"), context.Canceled} {
		got := mapError(permanent)
		if errors.Is(got, app.ErrTransient) || errors.Is(got, app.ErrConflict) || errors.Is(got, app.ErrNotFound) {
			t.Errorf("mapError(%v) = %v, want unclassified", permanent, got)
		}
	}
	if mapError(nil) != nil {
		t.Fatal("mapError(nil) must be nil")
	}
	if got := mapError(pg("23505")); !strings.Contains(got.Error(), "some_constraint") {
		t.Fatalf("conflict must name the constraint: %v", got)
	}
}
```

- [ ] **Step 3: Rodar e confirmar que falham**

Run: `go test -tags=unit ./internal/platform/... ./internal/adapters/postgres/...`
Expected: FAIL de compilação, `undefined: Load` e `undefined: mapError`.

- [ ] **Step 4: Implementar ports, config e erros**

`internal/app/errors.go`:

```go
// Package app holds the application use cases (Plan 3) and the ports they
// depend on. Adapters translate their failures into the errors below.
package app

import "errors"

var (
	// ErrNotFound reports a missing row.
	ErrNotFound = errors.New("app: not found")
	// ErrConflict reports a uniqueness violation; the message names the constraint.
	ErrConflict = errors.New("app: conflict")
	// ErrVersionConflict reports a lost race on an optimistic version guard.
	ErrVersionConflict = errors.New("app: concurrent update")
	// ErrTransient reports a temporary infrastructure failure worth retrying
	// (connection loss, lock or statement timeout, serialization, deadlock).
	ErrTransient = errors.New("app: transient infrastructure failure")
)
```

`internal/app/ports.go`:

```go
package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

// TxManager runs fn inside one SQL transaction carried by ctx. Repositories
// called with that ctx join the transaction; a nested call joins the outer
// one. fn's error rolls everything back.
type TxManager interface {
	WithinTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// WalletRepository persists the Wallet aggregate.
type WalletRepository interface {
	// Create inserts a new wallet; ErrConflict if (playerId, currency) exists.
	Create(ctx context.Context, w *wallet.Wallet) error
	// Get reads a wallet without locking; ErrNotFound if missing.
	Get(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error)
	// GetForUpdate reads and row-locks a wallet; it requires a transaction.
	GetForUpdate(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error)
	// Update writes balance and version, guarded by expectedVersion;
	// ErrVersionConflict if another writer moved the wallet.
	Update(ctx context.Context, w *wallet.Wallet, expectedVersion int64) error
}

// TransactionRepository persists WagerTransactions.
type TransactionRepository interface {
	// Insert adds t unless a row with the same id, idempotency key, external
	// id or OPENING wallet exists; inserted reports which happened.
	Insert(ctx context.Context, t *wagering.WagerTransaction) (inserted bool, err error)
	// Update writes the mutable state of t (status, reference, result, retry).
	Update(ctx context.Context, t *wagering.WagerTransaction) error
	Get(ctx context.Context, id uuid.UUID) (*wagering.WagerTransaction, error)
	FindByIdempotencyKey(ctx context.Context, providerID, key string) (*wagering.WagerTransaction, error)
	FindByExternalID(ctx context.Context, providerID, externalTransactionID string) (*wagering.WagerTransaction, error)
	// HasProcessedReversal reports whether referenceID already has a PROCESSED
	// reversal of kind or, when referenceKind is BET, any PROCESSED REFUND or
	// ROLLBACK — the value of ProcessInput.ReferenceAlreadyReversed.
	HasProcessedReversal(ctx context.Context, referenceID uuid.UUID, kind, referenceKind wagering.Kind) (bool, error)
}

// LedgerRepository appends immutable ledger entries.
type LedgerRepository interface {
	Append(ctx context.Context, e wallet.LedgerEntry) error
}

// OutboxRepository stores integration events in the same transaction as the
// changes that produced them.
type OutboxRepository interface {
	Append(ctx context.Context, envs ...events.Envelope) error
}
```

`internal/platform/config/config.go`:

```go
// Package config loads and validates the service configuration from the
// environment. Invalid configuration stops the process before it starts.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Config is the whole service configuration. Later plans add sections.
type Config struct {
	Database Database
}

// Database configures the PostgreSQL connection pool and per-transaction limits.
type Database struct {
	URL              string
	MaxOpenConns     int
	LockTimeout      time.Duration
	StatementTimeout time.Duration
}

// Load reads the configuration through getenv (os.Getenv in production).
// Every invalid variable is reported at once.
func Load(getenv func(string) string) (Config, error) {
	var errs []error
	db := Database{
		URL:              getenv("DATABASE_URL"),
		MaxOpenConns:     positiveInt(getenv, "DB_MAX_OPEN_CONNS", 20, &errs),
		LockTimeout:      positiveDuration(getenv, "DB_LOCK_TIMEOUT", 5*time.Second, &errs),
		StatementTimeout: positiveDuration(getenv, "DB_STATEMENT_TIMEOUT", 10*time.Second, &errs),
	}
	if db.URL == "" {
		errs = append(errs, errors.New("DATABASE_URL is required"))
	}
	if len(errs) > 0 {
		return Config{}, fmt.Errorf("config: %w", errors.Join(errs...))
	}
	return Config{Database: db}, nil
}

func positiveInt(getenv func(string) string, key string, fallback int, errs *[]error) int {
	raw := getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a positive integer, got %q", key, raw))
		return 0
	}
	return v
}

func positiveDuration(getenv func(string) string, key string, fallback time.Duration, errs *[]error) time.Duration {
	raw := getenv(key)
	if raw == "" {
		return fallback
	}
	v, err := time.ParseDuration(raw)
	if err != nil || v <= 0 {
		*errs = append(*errs, fmt.Errorf("%s must be a positive duration, got %q", key, raw))
		return 0
	}
	return v
}
```

`internal/adapters/postgres/errors.go`:

```go
package postgres

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
)

// transientCodes are SQLSTATEs worth retrying: serialization failure,
// deadlock, lock_timeout, statement_timeout/cancel, server shutdown and
// connection pressure. Class 08 (connection exception) is matched by prefix.
var transientCodes = map[string]bool{
	"40001": true, "40P01": true, "55P03": true, "57014": true,
	"57P01": true, "57P02": true, "57P03": true, "53300": true,
}

// mapError classifies database errors into app sentinels while keeping the
// original error in the chain. Unknown errors are returned unchanged and are
// treated as permanent by callers.
func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: %w", app.ErrNotFound, err)
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch {
		case pgErr.Code == "23505":
			return fmt.Errorf("%w: %s: %w", app.ErrConflict, pgErr.ConstraintName, err)
		case transientCodes[pgErr.Code] || strings.HasPrefix(pgErr.Code, "08"):
			return fmt.Errorf("%w: %w", app.ErrTransient, err)
		default:
			return err
		}
	}
	var connErr *pgconn.ConnectError
	var netErr net.Error
	if errors.As(err, &connErr) || errors.As(err, &netErr) ||
		errors.Is(err, driver.ErrBadConn) || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: %w", app.ErrTransient, err)
	}
	return err
}
```

- [ ] **Step 5: Rodar os testes unitários**

Run: `go test -race -tags=unit ./internal/platform/... ./internal/adapters/postgres/...`
Expected: `ok` nos dois pacotes.

- [ ] **Step 6: Escrever o teste de integração do TxManager que falha**

`internal/adapters/postgres/tx_test.go`:

```go
//go:build !unit && !e2e

package postgres

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	pgtest.AppDB(t) // fails fast with setup instructions when unreachable
	db, err := Open(config.Database{URL: pgtest.AppURL(), MaxOpenConns: 10, LockTimeout: time.Second, StatementTimeout: 5 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func testTxManager(db *gorm.DB, lockTimeout time.Duration) *TxManager {
	return NewTxManager(db, config.Config{Database: config.Database{LockTimeout: lockTimeout, StatementTimeout: 5 * time.Second}})
}

func walletExists(t *testing.T, db *gorm.DB, id any) bool {
	t.Helper()
	var n int64
	if err := db.Table("wallets").Where("id = ?", id).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestWithinTxCommitsAndRollsBack(t *testing.T) {
	db := newTestDB(t)
	tm := testTxManager(db, time.Second)
	ctx := context.Background()

	committed := walletRow(uuid.New(), "BRL", 0)
	err := tm.WithinTx(ctx, func(ctx context.Context) error {
		return conn(ctx, db).Table("wallets").Create(committed).Error
	})
	if err != nil || !walletExists(t, db, committed["id"]) {
		t.Fatalf("commit: err %v", err)
	}

	rolledBack := walletRow(uuid.New(), "BRL", 0)
	boom := errors.New("boom")
	err = tm.WithinTx(ctx, func(ctx context.Context) error {
		if err := conn(ctx, db).Table("wallets").Create(rolledBack).Error; err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || walletExists(t, db, rolledBack["id"]) {
		t.Fatalf("rollback: err %v, row must not exist", err)
	}
}

func TestWithinTxNestedJoinsOuter(t *testing.T) {
	db := newTestDB(t)
	tm := testTxManager(db, time.Second)
	inner := walletRow(uuid.New(), "BRL", 0)
	boom := errors.New("outer fails")
	err := tm.WithinTx(context.Background(), func(ctx context.Context) error {
		if err := tm.WithinTx(ctx, func(ctx context.Context) error {
			return conn(ctx, db).Table("wallets").Create(inner).Error
		}); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) || walletExists(t, db, inner["id"]) {
		t.Fatalf("nested work must roll back with the outer transaction: err %v", err)
	}
}

func TestWithinTxAppliesTimeouts(t *testing.T) {
	db := newTestDB(t)
	tm := NewTxManager(db, config.Config{Database: config.Database{LockTimeout: 1500 * time.Millisecond, StatementTimeout: 7 * time.Second}})
	err := tm.WithinTx(context.Background(), func(ctx context.Context) error {
		var lock, stmt string
		if err := conn(ctx, db).Raw("SHOW lock_timeout").Scan(&lock).Error; err != nil {
			return err
		}
		if err := conn(ctx, db).Raw("SHOW statement_timeout").Scan(&stmt).Error; err != nil {
			return err
		}
		if lock != "1500ms" || stmt != "7s" {
			t.Errorf("lock_timeout %q statement_timeout %q, want 1500ms and 7s", lock, stmt)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var outside string
	if err := db.Raw("SHOW lock_timeout").Scan(&outside).Error; err != nil || outside == "1500ms" {
		t.Fatalf("timeouts must be transaction-local, got %q (%v)", outside, err)
	}
}

func TestLockTimeoutIsTransient(t *testing.T) {
	db := newTestDB(t)
	row := walletRow(uuid.New(), "BRL", 0)
	if err := db.Table("wallets").Create(row).Error; err != nil {
		t.Fatal(err)
	}
	holder := testTxManager(db, 5*time.Second)
	waiter := testTxManager(db, 200*time.Millisecond)

	locked, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- holder.WithinTx(context.Background(), func(ctx context.Context) error {
			if err := conn(ctx, db).Exec("SELECT 1 FROM wallets WHERE id = ? FOR UPDATE", row["id"]).Error; err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	start := time.Now()
	err := waiter.WithinTx(context.Background(), func(ctx context.Context) error {
		return conn(ctx, db).Exec("SELECT 1 FROM wallets WHERE id = ? FOR UPDATE", row["id"]).Error
	})
	close(release)
	if !errors.Is(err, app.ErrTransient) {
		t.Fatalf("error = %v, want app.ErrTransient (lock_timeout)", err)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("waited %s, lock_timeout not applied", elapsed)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 7: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/adapters/postgres/ -run 'WithinTx|LockTimeout'`
Expected: FAIL de compilação, `undefined: Open`, `undefined: NewTxManager` e `undefined: conn`.

- [ ] **Step 8: Implementar conexão e TxManager**

`internal/adapters/postgres/db.go`:

```go
package postgres

import (
	"fmt"
	"time"

	gormpg "gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

// Open builds the GORM handle over pgx. It does not connect: connectivity is
// verified by the Fx OnStart hook (Ping). GORM's implicit per-write
// transactions are disabled; transactions are always explicit (TxManager).
func Open(cfg config.Database) (*gorm.DB, error) {
	db, err := gorm.Open(gormpg.Open(cfg.URL), &gorm.Config{
		SkipDefaultTransaction: true,
		DisableAutomaticPing:   true,
		Logger:                 logger.Discard,
		NowFunc:                func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("postgres: pool: %w", err)
	}
	sqlDB.SetMaxOpenConns(cfg.MaxOpenConns)
	sqlDB.SetMaxIdleConns(cfg.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	return db, nil
}
```

`internal/adapters/postgres/tx.go`:

```go
package postgres

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

var _ app.TxManager = (*TxManager)(nil)

type txKey struct{}

// TxManager delimits SQL transactions for use cases. The GORM transaction
// travels in the context; repositories pick it up through conn.
type TxManager struct {
	db               *gorm.DB
	lockTimeout      time.Duration
	statementTimeout time.Duration
}

// NewTxManager builds a TxManager with the per-transaction timeouts of cfg.
func NewTxManager(db *gorm.DB, cfg config.Config) *TxManager {
	return &TxManager{db: db, lockTimeout: cfg.Database.LockTimeout, statementTimeout: cfg.Database.StatementTimeout}
}

// WithinTx runs fn in a READ COMMITTED transaction with transaction-local
// lock_timeout and statement_timeout. A call inside an existing transaction
// joins it. Errors are classified with mapError.
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFrom(ctx); ok {
		return fn(ctx)
	}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Exec("SELECT set_config('lock_timeout', ?, true), set_config('statement_timeout', ?, true)",
			millis(m.lockTimeout), millis(m.statementTimeout)).Error
		if err != nil {
			return err
		}
		return fn(context.WithValue(ctx, txKey{}, tx))
	})
	return mapError(err)
}

func millis(d time.Duration) string { return fmt.Sprintf("%dms", d.Milliseconds()) }

// txFrom returns the transaction carried by ctx, if any.
func txFrom(ctx context.Context) (*gorm.DB, bool) {
	tx, ok := ctx.Value(txKey{}).(*gorm.DB)
	return tx, ok
}

// conn returns the transaction in ctx or, outside a transaction, db bound to ctx.
func conn(ctx context.Context, db *gorm.DB) *gorm.DB {
	if tx, ok := txFrom(ctx); ok {
		return tx
	}
	return db.WithContext(ctx)
}
```

- [ ] **Step 9: Rodar os testes de integração**

Run: `go test -race -tags=integration ./internal/adapters/postgres/...`
Expected: `ok`.

- [ ] **Step 10: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/app internal/platform internal/adapters/postgres
git commit -m "feat(postgres): app ports, config, connection, transaction manager and error mapping" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: WalletRepository

**Files:**
- Create: `internal/adapters/postgres/models.go`, `internal/adapters/postgres/mapping.go`, `internal/adapters/postgres/wallet_repository.go`
- Test: `internal/adapters/postgres/wallet_repository_test.go` (integration)

**Interfaces:**
- Consumes: `conn`, `txFrom`, `mapError`, `newTestDB`, `testTxManager` (Task 6); `wallet.Open/Rehydrate`; `money.ParseCurrency/FromMinor`.
- Produces:
  - `walletModel` (tabela `wallets`), `walletToModel(*wallet.Wallet) walletModel` e
    `walletFromModel(walletModel) (*wallet.Wallet, error)`.
  - `postgres.NewWalletRepository(db *gorm.DB) *WalletRepository`, que implementa `app.WalletRepository`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/adapters/postgres/wallet_repository_test.go`:

```go
//go:build !unit && !e2e

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

func brlMoney(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func newWallet(t *testing.T, balance string) *wallet.Wallet {
	t.Helper()
	id, _ := uuid.NewV7()
	w, _, err := wallet.Open(wallet.OpenParams{
		ID: id, PlayerID: uuid.New(), InitialBalance: brlMoney(t, balance),
		OpeningTransactionID: uuid.New(), LedgerEntryID: uuid.New(),
		Now: time.Date(2026, 10, 1, 9, 0, 0, 123456000, time.FixedZone("BRT", -3*3600)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWalletCreateAndGet(t *testing.T) {
	db := newTestDB(t)
	repo := NewWalletRepository(db)
	ctx := context.Background()
	w := newWallet(t, "100.00")

	if err := repo.Create(ctx, w); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, w.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != w.ID() || got.PlayerID() != w.PlayerID() || !got.Balance().Equal(w.Balance()) ||
		got.Version() != 1 || !got.CreatedAt().Equal(w.CreatedAt()) || !got.UpdatedAt().Equal(w.UpdatedAt()) {
		t.Fatalf("round trip mismatch: got %+v want %+v", got, w)
	}
	if got.CreatedAt().Location() != time.UTC {
		t.Fatalf("CreatedAt location %v, want UTC", got.CreatedAt().Location())
	}

	if _, err := repo.Get(ctx, uuid.New()); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing wallet error = %v, want ErrNotFound", err)
	}

	dup, _, _ := wallet.Open(wallet.OpenParams{ID: uuid.New(), PlayerID: w.PlayerID(), InitialBalance: brlMoney(t, "0.00"), Now: time.Now()})
	if err := repo.Create(ctx, dup); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("duplicate (player, currency) error = %v, want ErrConflict", err)
	}
}

func TestWalletGetForUpdateRequiresTransaction(t *testing.T) {
	repo := NewWalletRepository(newTestDB(t))
	if _, err := repo.GetForUpdate(context.Background(), uuid.New()); err == nil {
		t.Fatal("GetForUpdate outside a transaction must fail")
	}
}

func TestWalletUpdateWithVersionGuard(t *testing.T) {
	db := newTestDB(t)
	repo := NewWalletRepository(db)
	tm := testTxManager(db, time.Second)
	ctx := context.Background()
	w := newWallet(t, "100.00")
	if err := repo.Create(ctx, w); err != nil {
		t.Fatal(err)
	}

	err := tm.WithinTx(ctx, func(ctx context.Context) error {
		locked, err := repo.GetForUpdate(ctx, w.ID())
		if err != nil {
			return err
		}
		before := locked.Version()
		if _, err := locked.Debit(uuid.New(), uuid.New(), brlMoney(t, "25.00"), time.Now()); err != nil {
			return err
		}
		return repo.Update(ctx, locked, before)
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(ctx, w.ID())
	if got.Balance().Amount() != "75.00" || got.Version() != 2 {
		t.Fatalf("after update: balance %s version %d", got.Balance(), got.Version())
	}

	// Two writers read version 2; the first commits, the second is stale.
	first, _ := repo.Get(ctx, w.ID())
	second, _ := repo.Get(ctx, w.ID())
	for _, copy := range []*wallet.Wallet{first, second} {
		if _, err := copy.Credit(uuid.New(), uuid.New(), brlMoney(t, "1.00"), time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.Update(ctx, first, 2); err != nil {
		t.Fatal(err)
	}
	if err := repo.Update(ctx, second, 2); !errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("stale version error = %v, want ErrVersionConflict", err)
	}
	if err := repo.Update(ctx, second, second.Version()); err == nil || errors.Is(err, app.ErrVersionConflict) {
		t.Fatalf("version must advance by exactly one: err %v", err)
	}
	final, _ := repo.Get(ctx, w.ID())
	if final.Balance().Amount() != "76.00" || final.Version() != 3 {
		t.Fatalf("final: balance %s version %d", final.Balance(), final.Version())
	}
}

// Two concurrent debits of 80.00 on 100.00: the lock serializes them, the
// second sees 20.00 and is refused by the domain; no update is lost.
func TestWalletConcurrentDebitsDoNotLoseUpdates(t *testing.T) {
	db := newTestDB(t)
	repo := NewWalletRepository(db)
	tm := testTxManager(db, 5*time.Second)
	ctx := context.Background()
	w := newWallet(t, "100.00")
	if err := repo.Create(ctx, w); err != nil {
		t.Fatal(err)
	}

	amount := brlMoney(t, "80.00")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- tm.WithinTx(ctx, func(ctx context.Context) error {
				locked, err := repo.GetForUpdate(ctx, w.ID())
				if err != nil {
					return err
				}
				before := locked.Version()
				if _, err := locked.Debit(uuid.New(), uuid.New(), amount, time.Now()); err != nil {
					return err
				}
				return repo.Update(ctx, locked, before)
			})
		}()
	}
	wg.Wait()
	close(results)

	var ok, insufficient int
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, wallet.ErrInsufficientFunds):
			insufficient++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	got, _ := repo.Get(ctx, w.ID())
	if ok != 1 || insufficient != 1 || got.Balance().Amount() != "20.00" || got.Version() != 2 {
		t.Fatalf("ok %d insufficient %d balance %s version %d", ok, insufficient, got.Balance(), got.Version())
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/adapters/postgres/ -run Wallet`
Expected: FAIL de compilação, `undefined: NewWalletRepository`.

- [ ] **Step 3: Implementar modelos, mapeamento e repositório**

`internal/adapters/postgres/models.go`:

```go
package postgres

import (
	"time"

	"github.com/google/uuid"
)

// Persistence models are separate from domain types: the domain stays free of
// GORM tags. Timestamps are owned by the domain (autoCreate/UpdateTime off).

type walletModel struct {
	ID           uuid.UUID `gorm:"column:id;primaryKey"`
	PlayerID     uuid.UUID `gorm:"column:player_id"`
	Currency     string    `gorm:"column:currency"`
	BalanceMinor int64     `gorm:"column:balance_minor"`
	Version      int64     `gorm:"column:version"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt    time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (walletModel) TableName() string { return "wallets" }
```

`internal/adapters/postgres/mapping.go`:

```go
package postgres

import (
	"fmt"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

func walletToModel(w *wallet.Wallet) walletModel {
	return walletModel{
		ID:           w.ID(),
		PlayerID:     w.PlayerID(),
		Currency:     w.Currency().Code(),
		BalanceMinor: w.Balance().MinorUnits(),
		Version:      w.Version(),
		CreatedAt:    w.CreatedAt(),
		UpdatedAt:    w.UpdatedAt(),
	}
}

func walletFromModel(m walletModel) (*wallet.Wallet, error) {
	balance, err := moneyFrom(m.BalanceMinor, m.Currency)
	if err != nil {
		return nil, fmt.Errorf("postgres: wallet %s: %w", m.ID, err)
	}
	return wallet.Rehydrate(wallet.RehydrateParams{
		ID:        m.ID,
		PlayerID:  m.PlayerID,
		Balance:   balance,
		Version:   m.Version,
		CreatedAt: m.CreatedAt,
		UpdatedAt: m.UpdatedAt,
	})
}

func moneyFrom(minor int64, currency string) (money.Money, error) {
	cur, err := money.ParseCurrency(currency)
	if err != nil {
		return money.Money{}, err
	}
	return money.FromMinor(minor, cur)
}
```

`internal/adapters/postgres/wallet_repository.go`:

```go
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

var _ app.WalletRepository = (*WalletRepository)(nil)

// errNoTransaction reports a locking read attempted outside WithinTx.
var errNoTransaction = errors.New("postgres: operation requires a transaction (TxManager.WithinTx)")

// WalletRepository persists wallets with pessimistic row locks and a version guard.
type WalletRepository struct {
	db *gorm.DB
}

// NewWalletRepository builds a WalletRepository.
func NewWalletRepository(db *gorm.DB) *WalletRepository { return &WalletRepository{db: db} }

// Create inserts w; a duplicate (playerId, currency) returns app.ErrConflict.
func (r *WalletRepository) Create(ctx context.Context, w *wallet.Wallet) error {
	m := walletToModel(w)
	return mapError(conn(ctx, r.db).Create(&m).Error)
}

// Get reads a wallet without locking.
func (r *WalletRepository) Get(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error) {
	var m walletModel
	if err := conn(ctx, r.db).Where("id = ?", id).Take(&m).Error; err != nil {
		return nil, mapError(err)
	}
	return walletFromModel(m)
}

// GetForUpdate reads a wallet with SELECT ... FOR UPDATE. Concurrent writers
// of the same wallet wait (up to lock_timeout); other wallets are unaffected.
func (r *WalletRepository) GetForUpdate(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error) {
	tx, ok := txFrom(ctx)
	if !ok {
		return nil, errNoTransaction
	}
	var m walletModel
	err := tx.Clauses(clause.Locking{Strength: clause.LockingStrengthUpdate}).Where("id = ?", id).Take(&m).Error
	if err != nil {
		return nil, mapError(err)
	}
	return walletFromModel(m)
}

// Update writes the new balance with
// UPDATE wallets SET ..., version = version + 1 WHERE id = ? AND version = ?.
// No matching row means another writer committed first: app.ErrVersionConflict.
func (r *WalletRepository) Update(ctx context.Context, w *wallet.Wallet, expectedVersion int64) error {
	if w.Version() != expectedVersion+1 {
		return fmt.Errorf("postgres: wallet %s version %d must be expected %d + 1", w.ID(), w.Version(), expectedVersion)
	}
	res := conn(ctx, r.db).Model(&walletModel{}).
		Where("id = ? AND version = ?", w.ID(), expectedVersion).
		Updates(map[string]any{
			"balance_minor": w.Balance().MinorUnits(),
			"version":       gorm.Expr("version + 1"),
			"updated_at":    w.UpdatedAt(),
		})
	if res.Error != nil {
		return mapError(res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: wallet %s at version %d", app.ErrVersionConflict, w.ID(), expectedVersion)
	}
	return nil
}
```

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=integration ./internal/adapters/postgres/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/postgres
git commit -m "feat(postgres): wallet repository with row lock and version guard" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: TransactionRepository

**Files:**
- Modify: `internal/adapters/postgres/models.go`, `internal/adapters/postgres/mapping.go`
- Create: `internal/adapters/postgres/transaction_repository.go`
- Test: `internal/adapters/postgres/mapping_test.go` (unit), `internal/adapters/postgres/transaction_repository_test.go` (integration)

**Interfaces:**
- Consumes: `wagering.State`, `wagering.Rehydrate`, `ParseCommand`, `NewExternal`, `NewOpening`, `Process` e
  `DefaultRetryPolicy`; `moneyFrom`; `newWallet`, `brlMoney` (Task 7).
- Produces:
  - `transactionModel` (tabela `wager_transactions`), `transactionToModel(*wagering.WagerTransaction) transactionModel`
    e `transactionFromModel(transactionModel) (*wagering.WagerTransaction, error)`.
  - `postgres.NewTransactionRepository(db *gorm.DB) *TransactionRepository`, que implementa
    `app.TransactionRepository`.

- [ ] **Step 1: Escrever o teste unitário de mapeamento que falha**

`internal/adapters/postgres/mapping_test.go`:

```go
//go:build !integration && !e2e

package postgres

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

func unitMoney(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func command(t *testing.T, w *wallet.Wallet, kind, amount, ref string) wagering.Command {
	t.Helper()
	ext := uuid.NewString()
	cmd, err := wagering.ParseCommand(wagering.RawCommand{
		ProviderID: "provider-a", ExternalTransactionID: ext, IdempotencyKey: "provider-a:" + ext,
		PlayerID: w.PlayerID().String(), WalletID: w.ID().String(), RoundID: "round-1", GameID: "game-1",
		Kind: kind, Amount: amount, Currency: "BRL", ReferenceExternalTransactionID: ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

func roundTrip(t *testing.T, tx *wagering.WagerTransaction) {
	t.Helper()
	back, err := transactionFromModel(transactionToModel(tx))
	if err != nil {
		t.Fatal(err)
	}
	if back.State() != tx.State() {
		t.Fatalf("mapping round trip\n got %+v\nwant %+v", back.State(), tx.State())
	}
}

func TestTransactionMappingRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	w, entry, err := wallet.Open(wallet.OpenParams{
		ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: unitMoney(t, "100.00"),
		OpeningTransactionID: uuid.New(), LedgerEntryID: uuid.New(), Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}

	opening, _ := wagering.NewOpening(entry.TransactionID(), w.ID(), w.PlayerID(), unitMoney(t, "100.00"), now)
	if err := opening.CompleteOpening(w, *entry, now); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, opening)

	bet, _ := wagering.NewExternal(uuid.New(), command(t, w, "BET", "30.00", ""), now)
	if _, err := bet.Process(wagering.ProcessInput{Wallet: w, LedgerEntryID: uuid.New(), RetryPolicy: wagering.DefaultRetryPolicy(), Now: now}); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, bet)

	pending, _ := wagering.NewExternal(uuid.New(), command(t, w, "REFUND", "30.00", "not-yet"), now)
	if _, err := pending.Process(wagering.ProcessInput{Wallet: w, LedgerEntryID: uuid.New(), RetryPolicy: wagering.DefaultRetryPolicy(), Now: now}); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, pending)

	ref := bet.AsReference()
	refund, _ := wagering.NewExternal(uuid.New(), command(t, w, "REFUND", "30.00", bet.ExternalTransactionID()), now)
	if _, err := refund.Process(wagering.ProcessInput{Wallet: w, Reference: &ref, LedgerEntryID: uuid.New(), RetryPolicy: wagering.DefaultRetryPolicy(), Now: now}); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, refund)

	rejected, _ := wagering.NewExternal(uuid.New(), command(t, w, "BET", "999.00", ""), now)
	if _, err := rejected.Process(wagering.ProcessInput{Wallet: w, LedgerEntryID: uuid.New(), RetryPolicy: wagering.DefaultRetryPolicy(), Now: now}); err != nil {
		t.Fatal(err)
	}
	roundTrip(t, rejected)
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=unit ./internal/adapters/postgres/...`
Expected: FAIL de compilação, `undefined: transactionFromModel`.

- [ ] **Step 3: Implementar modelo e mapeamento**

Acrescentar a `internal/adapters/postgres/models.go`:

```go
type transactionModel struct {
	ID                             uuid.UUID  `gorm:"column:id;primaryKey"`
	Origin                         string     `gorm:"column:origin"`
	Kind                           string     `gorm:"column:kind"`
	Status                         string     `gorm:"column:status"`
	WalletID                       uuid.UUID  `gorm:"column:wallet_id"`
	PlayerID                       uuid.UUID  `gorm:"column:player_id"`
	AmountMinor                    int64      `gorm:"column:amount_minor"`
	Currency                       string     `gorm:"column:currency"`
	ProviderID                     *string    `gorm:"column:provider_id"`
	ExternalTransactionID          *string    `gorm:"column:external_transaction_id"`
	IdempotencyKey                 *string    `gorm:"column:idempotency_key"`
	PayloadHash                    *string    `gorm:"column:payload_hash"`
	RoundID                        *string    `gorm:"column:round_id"`
	GameID                         *string    `gorm:"column:game_id"`
	ReferenceExternalTransactionID *string    `gorm:"column:reference_external_transaction_id"`
	ReferenceTransactionID         *uuid.UUID `gorm:"column:reference_transaction_id"`
	ReferenceKind                  *string    `gorm:"column:reference_kind"`
	FailureCode                    *string    `gorm:"column:failure_code"`
	ResultBalanceMinor             *int64     `gorm:"column:result_balance_minor"`
	Attempts                       int        `gorm:"column:attempts"`
	NextAttemptAt                  *time.Time `gorm:"column:next_attempt_at"`
	CreatedAt                      time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt                      time.Time  `gorm:"column:updated_at;autoUpdateTime:false"`
	ProcessedAt                    *time.Time `gorm:"column:processed_at"`
}

func (transactionModel) TableName() string { return "wager_transactions" }
```

Acrescentar a `internal/adapters/postgres/mapping.go` (com os imports `time`, `github.com/google/uuid` e o pacote
`wagering`):

```go
func transactionToModel(t *wagering.WagerTransaction) transactionModel {
	s := t.State()
	m := transactionModel{
		ID:                             s.ID,
		Origin:                         string(s.Origin),
		Kind:                           string(s.Kind),
		Status:                         string(s.Status),
		WalletID:                       s.WalletID,
		PlayerID:                       s.PlayerID,
		AmountMinor:                    s.Money.MinorUnits(),
		Currency:                       s.Money.Currency().Code(),
		ProviderID:                     optString(s.ProviderID),
		ExternalTransactionID:          optString(s.ExternalTransactionID),
		IdempotencyKey:                 optString(s.IdempotencyKey),
		PayloadHash:                    optString(s.PayloadHash),
		RoundID:                        optString(s.RoundID),
		GameID:                         optString(s.GameID),
		ReferenceExternalTransactionID: optString(s.ReferenceExternalTransactionID),
		ReferenceKind:                  optString(string(s.ReferenceKind)),
		FailureCode:                    optString(string(s.FailureCode)),
		Attempts:                       s.Attempts,
		NextAttemptAt:                  optTime(s.NextAttemptAt),
		CreatedAt:                      s.CreatedAt,
		UpdatedAt:                      s.UpdatedAt,
		ProcessedAt:                    optTime(s.ProcessedAt),
	}
	if s.ReferenceTransactionID != uuid.Nil {
		ref := s.ReferenceTransactionID
		m.ReferenceTransactionID = &ref
	}
	if s.ResultBalance.IsValid() {
		minor := s.ResultBalance.MinorUnits()
		m.ResultBalanceMinor = &minor
	}
	return m
}

func transactionFromModel(m transactionModel) (*wagering.WagerTransaction, error) {
	amount, err := moneyFrom(m.AmountMinor, m.Currency)
	if err != nil {
		return nil, fmt.Errorf("postgres: transaction %s: %w", m.ID, err)
	}
	s := wagering.State{
		ID:                             m.ID,
		Origin:                         wagering.Origin(m.Origin),
		Kind:                           wagering.Kind(m.Kind),
		Status:                         wagering.Status(m.Status),
		WalletID:                       m.WalletID,
		PlayerID:                       m.PlayerID,
		Money:                          amount,
		ProviderID:                     deref(m.ProviderID),
		ExternalTransactionID:          deref(m.ExternalTransactionID),
		IdempotencyKey:                 deref(m.IdempotencyKey),
		PayloadHash:                    deref(m.PayloadHash),
		RoundID:                        deref(m.RoundID),
		GameID:                         deref(m.GameID),
		ReferenceExternalTransactionID: deref(m.ReferenceExternalTransactionID),
		ReferenceKind:                  wagering.Kind(deref(m.ReferenceKind)),
		FailureCode:                    wagering.FailureCode(deref(m.FailureCode)),
		Attempts:                       m.Attempts,
		CreatedAt:                      m.CreatedAt,
		UpdatedAt:                      m.UpdatedAt,
	}
	if m.ReferenceTransactionID != nil {
		s.ReferenceTransactionID = *m.ReferenceTransactionID
	}
	if m.ResultBalanceMinor != nil {
		if s.ResultBalance, err = moneyFrom(*m.ResultBalanceMinor, m.Currency); err != nil {
			return nil, fmt.Errorf("postgres: transaction %s result balance: %w", m.ID, err)
		}
	}
	if m.NextAttemptAt != nil {
		s.NextAttemptAt = *m.NextAttemptAt
	}
	if m.ProcessedAt != nil {
		s.ProcessedAt = *m.ProcessedAt
	}
	t, err := wagering.Rehydrate(s)
	if err != nil {
		return nil, fmt.Errorf("postgres: transaction %s: %w", m.ID, err)
	}
	return t, nil
}

func optString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

func optTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}
```

- [ ] **Step 4: Rodar o teste unitário**

Run: `go test -race -tags=unit ./internal/adapters/postgres/...`
Expected: `ok`.

- [ ] **Step 5: Escrever o teste de integração que falha**

`internal/adapters/postgres/transaction_repository_test.go`:

```go
//go:build !unit && !e2e

package postgres

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

func externalCommand(t *testing.T, w *wallet.Wallet, kind, amount, ref string) wagering.Command {
	t.Helper()
	ext := uuid.NewString()
	cmd, err := wagering.ParseCommand(wagering.RawCommand{
		ProviderID: "provider-a", ExternalTransactionID: ext, IdempotencyKey: "provider-a:" + ext,
		PlayerID: w.PlayerID().String(), WalletID: w.ID().String(), RoundID: "round-1", GameID: "game-1",
		Kind: kind, Amount: amount, Currency: "BRL", ReferenceExternalTransactionID: ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

// nowUS matches PostgreSQL's microsecond timestamp precision, so State
// round trips compare equal.
func nowUS() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

func process(t *testing.T, tx *wagering.WagerTransaction, w *wallet.Wallet, ref *wagering.Reference) {
	t.Helper()
	_, err := tx.Process(wagering.ProcessInput{
		Wallet: w, Reference: ref, LedgerEntryID: uuid.New(),
		RetryPolicy: wagering.DefaultRetryPolicy(), Now: nowUS(),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTransactionInsertGetAndFind(t *testing.T) {
	repo := NewTransactionRepository(newTestDB(t))
	ctx := context.Background()
	w := newWallet(t, "100.00")
	cmd := externalCommand(t, w, "BET", "25.00", "")
	tx, _ := wagering.NewExternal(uuid.New(), cmd, nowUS())

	inserted, err := repo.Insert(ctx, tx)
	if err != nil || !inserted {
		t.Fatalf("Insert = %v, %v", inserted, err)
	}
	again, _ := wagering.NewExternal(uuid.New(), cmd, time.Now())
	if inserted, err := repo.Insert(ctx, again); err != nil || inserted {
		t.Fatalf("same idempotency key Insert = %v, %v; want false, nil", inserted, err)
	}

	for name, get := range map[string]func() (*wagering.WagerTransaction, error){
		"by id":  func() (*wagering.WagerTransaction, error) { return repo.Get(ctx, tx.ID()) },
		"by key": func() (*wagering.WagerTransaction, error) { return repo.FindByIdempotencyKey(ctx, "provider-a", cmd.IdempotencyKey) },
		"by ext": func() (*wagering.WagerTransaction, error) {
			return repo.FindByExternalID(ctx, "provider-a", cmd.ExternalTransactionID)
		},
	} {
		got, err := get()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got.State() != tx.State() {
			t.Fatalf("%s round trip\n got %+v\nwant %+v", name, got.State(), tx.State())
		}
	}

	if _, err := repo.FindByIdempotencyKey(ctx, "provider-b", cmd.IdempotencyKey); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("keys are scoped by provider: err %v", err)
	}
	if _, err := repo.Get(ctx, uuid.New()); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing: err %v", err)
	}
}

func TestTransactionConcurrentInsertSameKey(t *testing.T) {
	repo := NewTransactionRepository(newTestDB(t))
	w := newWallet(t, "100.00")
	cmd := externalCommand(t, w, "BET", "25.00", "")

	var wg sync.WaitGroup
	results := make(chan bool, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tx, _ := wagering.NewExternal(uuid.New(), cmd, time.Now())
			inserted, err := repo.Insert(context.Background(), tx)
			if err != nil {
				t.Error(err)
			}
			results <- inserted
		}()
	}
	wg.Wait()
	close(results)
	count := 0
	for inserted := range results {
		if inserted {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("inserted %d times, want exactly 1", count)
	}
}

func TestTransactionUpdateAndReversalLookup(t *testing.T) {
	repo := NewTransactionRepository(newTestDB(t))
	ctx := context.Background()
	w := newWallet(t, "100.00")

	bet, _ := wagering.NewExternal(uuid.New(), externalCommand(t, w, "BET", "30.00", ""), time.Now())
	if _, err := repo.Insert(ctx, bet); err != nil {
		t.Fatal(err)
	}
	process(t, bet, w, nil)
	if err := repo.Update(ctx, bet); err != nil {
		t.Fatal(err)
	}
	stored, _ := repo.Get(ctx, bet.ID())
	if stored.Status() != wagering.StatusProcessed || stored.ResultBalance().Amount() != "70.00" {
		t.Fatalf("stored = %s %s", stored.Status(), stored.ResultBalance())
	}

	for _, tt := range []struct {
		kind wagering.Kind
		want bool
	}{{wagering.KindRefund, false}, {wagering.KindRollback, false}} {
		if got, err := repo.HasProcessedReversal(ctx, bet.ID(), tt.kind, wagering.KindBet); err != nil || got != tt.want {
			t.Fatalf("before refund %s: %v %v", tt.kind, got, err)
		}
	}

	ref := bet.AsReference()
	refund, _ := wagering.NewExternal(uuid.New(), externalCommand(t, w, "REFUND", "30.00", bet.ExternalTransactionID()), time.Now())
	process(t, refund, w, &ref)
	if _, err := repo.Insert(ctx, refund); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.HasProcessedReversal(ctx, bet.ID(), wagering.KindRefund, wagering.KindBet); !got {
		t.Fatal("refund of the bet must be found")
	}
	if got, _ := repo.HasProcessedReversal(ctx, bet.ID(), wagering.KindRollback, wagering.KindBet); !got {
		t.Fatal("any reversal of a BET blocks a ROLLBACK of it")
	}
	if got, _ := repo.HasProcessedReversal(ctx, refund.ID(), wagering.KindRollback, wagering.KindRefund); got {
		t.Fatal("the refund itself has no reversal yet")
	}

	if err := repo.Update(ctx, bet); err == nil {
		t.Fatal("updating a terminal transaction must be refused by the database")
	}
	missing, _ := wagering.NewExternal(uuid.New(), externalCommand(t, w, "BET", "1.00", ""), time.Now())
	if err := repo.Update(ctx, missing); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("update of missing row: err %v", err)
	}
}
```

- [ ] **Step 6: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/adapters/postgres/ -run Transaction`
Expected: FAIL de compilação, `undefined: NewTransactionRepository`.

- [ ] **Step 7: Implementar o repositório**

`internal/adapters/postgres/transaction_repository.go`:

```go
package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

var _ app.TransactionRepository = (*TransactionRepository)(nil)

// TransactionRepository persists wager transactions. Idempotency relies on
// the unique constraints: Insert uses INSERT ... ON CONFLICT DO NOTHING, so a
// concurrent duplicate waits for the winner's commit and then inserts nothing.
type TransactionRepository struct {
	db *gorm.DB
}

// NewTransactionRepository builds a TransactionRepository.
func NewTransactionRepository(db *gorm.DB) *TransactionRepository {
	return &TransactionRepository{db: db}
}

// Insert adds t unless any unique key already exists.
func (r *TransactionRepository) Insert(ctx context.Context, t *wagering.WagerTransaction) (bool, error) {
	m := transactionToModel(t)
	res := conn(ctx, r.db).Clauses(clause.OnConflict{DoNothing: true}).Create(&m)
	if res.Error != nil {
		return false, mapError(res.Error)
	}
	return res.RowsAffected == 1, nil
}

// Update writes the mutable columns of t. The database trigger refuses
// changes to terminal rows and invalid transitions.
func (r *TransactionRepository) Update(ctx context.Context, t *wagering.WagerTransaction) error {
	m := transactionToModel(t)
	res := conn(ctx, r.db).Model(&transactionModel{}).Where("id = ?", m.ID).Updates(map[string]any{
		"status":                   m.Status,
		"reference_transaction_id": m.ReferenceTransactionID,
		"reference_kind":           m.ReferenceKind,
		"failure_code":             m.FailureCode,
		"result_balance_minor":     m.ResultBalanceMinor,
		"attempts":                 m.Attempts,
		"next_attempt_at":          m.NextAttemptAt,
		"updated_at":               m.UpdatedAt,
		"processed_at":             m.ProcessedAt,
	})
	if res.Error != nil {
		return mapError(res.Error)
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%w: transaction %s", app.ErrNotFound, m.ID)
	}
	return nil
}

// Get reads a transaction by internal id.
func (r *TransactionRepository) Get(ctx context.Context, id uuid.UUID) (*wagering.WagerTransaction, error) {
	return r.findOne(ctx, "id = ?", id)
}

// FindByIdempotencyKey reads the provider's transaction for key.
func (r *TransactionRepository) FindByIdempotencyKey(ctx context.Context, providerID, key string) (*wagering.WagerTransaction, error) {
	return r.findOne(ctx, "provider_id = ? AND idempotency_key = ?", providerID, key)
}

// FindByExternalID reads the provider's transaction for its external id.
func (r *TransactionRepository) FindByExternalID(ctx context.Context, providerID, externalTransactionID string) (*wagering.WagerTransaction, error) {
	return r.findOne(ctx, "provider_id = ? AND external_transaction_id = ?", providerID, externalTransactionID)
}

// HasProcessedReversal implements the ReferenceAlreadyReversed rule; the
// partial unique indexes enforce the same rule if two writers race.
func (r *TransactionRepository) HasProcessedReversal(ctx context.Context, referenceID uuid.UUID, kind, referenceKind wagering.Kind) (bool, error) {
	var exists bool
	err := conn(ctx, r.db).Raw(`
		SELECT EXISTS (
			SELECT 1 FROM wager_transactions
			WHERE reference_transaction_id = ?
			  AND status = 'PROCESSED'
			  AND kind IN ('REFUND', 'ROLLBACK')
			  AND (kind = ? OR ?)
		)`, referenceID, string(kind), referenceKind == wagering.KindBet).Scan(&exists).Error
	return exists, mapError(err)
}

func (r *TransactionRepository) findOne(ctx context.Context, query string, args ...any) (*wagering.WagerTransaction, error) {
	var m transactionModel
	if err := conn(ctx, r.db).Where(query, args...).Take(&m).Error; err != nil {
		return nil, mapError(err)
	}
	return transactionFromModel(m)
}
```

- [ ] **Step 8: Rodar os testes**

Run: `go test -race -tags=integration ./internal/adapters/postgres/... && go test -race -tags=unit ./internal/adapters/postgres/...`
Expected: `ok` nos dois.

- [ ] **Step 9: Commit**

```bash
git add internal/adapters/postgres
git commit -m "feat(postgres): transaction repository with idempotent insert and reversal lookup" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: LedgerRepository e OutboxRepository

**Files:**
- Modify: `internal/adapters/postgres/models.go`
- Create: `internal/adapters/postgres/ledger_repository.go`, `internal/adapters/postgres/outbox_repository.go`
- Test: `internal/adapters/postgres/ledger_outbox_repository_test.go` (integration)

**Interfaces:**
- Consumes: `NewWalletRepository`, `NewTransactionRepository`, `newWallet`, `brlMoney`, `externalCommand`, `process`, `newTestDB`, `testTxManager`; `events.NewEnvelope`.
- Produces:
  - `ledgerEntryModel` (tabela `wallet_ledger_entries`) e `outboxEventModel` (tabela `outbox_events`).
  - `postgres.NewLedgerRepository(db *gorm.DB) *LedgerRepository`, que implementa `app.LedgerRepository`.
  - `postgres.NewOutboxRepository(db *gorm.DB) *OutboxRepository`, que implementa `app.OutboxRepository`.
  - `postgres.OutboxAggregateWallet = "Wallet"`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/adapters/postgres/ledger_outbox_repository_test.go`:

```go
//go:build !unit && !e2e

package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// A BET persisted atomically: transaction, balance, ledger and outbox in one commit.
func TestBetPersistsAtomically(t *testing.T) {
	db := newTestDB(t)
	tm := testTxManager(db, time.Second)
	wallets, txs := NewWalletRepository(db), NewTransactionRepository(db)
	ledger, outbox := NewLedgerRepository(db), NewOutboxRepository(db)
	ctx := context.Background()

	w := newWallet(t, "100.00")
	if err := wallets.Create(ctx, w); err != nil {
		t.Fatal(err)
	}
	bet, _ := wagering.NewExternal(uuid.New(), externalCommand(t, w, "BET", "25.00", ""), time.Now())

	var envs []events.Envelope
	err := tm.WithinTx(ctx, func(ctx context.Context) error {
		if _, err := txs.Insert(ctx, bet); err != nil {
			return err
		}
		locked, err := wallets.GetForUpdate(ctx, w.ID())
		if err != nil {
			return err
		}
		before := locked.Version()
		entry, err := bet.Process(wagering.ProcessInput{
			Wallet: locked, LedgerEntryID: uuid.New(), RetryPolicy: wagering.DefaultRetryPolicy(), Now: time.Now(),
		})
		if err != nil {
			return err
		}
		if err := wallets.Update(ctx, locked, before); err != nil {
			return err
		}
		if err := txs.Update(ctx, bet); err != nil {
			return err
		}
		if err := ledger.Append(ctx, *entry); err != nil {
			return err
		}
		for _, data := range bet.PullEvents() {
			env, err := events.NewEnvelope(uuid.New(), data, "corr-1", "", time.Now())
			if err != nil {
				return err
			}
			envs = append(envs, env)
		}
		return outbox.Append(ctx, envs...)
	})
	if err != nil {
		t.Fatal(err)
	}

	var ledgerCount int64
	db.Table("wallet_ledger_entries").Where("transaction_id = ?", bet.ID()).Count(&ledgerCount)
	if ledgerCount != 1 {
		t.Fatalf("ledger entries = %d, want 1", ledgerCount)
	}
	for _, env := range envs {
		var row outboxEventModel
		if err := db.Where("id = ?", env.EventID).Take(&row).Error; err != nil {
			t.Fatalf("outbox %s: %v", env.EventType, err)
		}
		want, _ := json.Marshal(env)
		var gotJSON, wantJSON map[string]any
		_ = json.Unmarshal(row.Payload, &gotJSON)
		_ = json.Unmarshal(want, &wantJSON)
		if !reflect.DeepEqual(gotJSON, wantJSON) || row.EventType != env.EventType ||
			row.AggregateID != env.AggregateID || row.AggregateType != OutboxAggregateWallet ||
			row.PublishedAt != nil || row.Attempts != 0 || !row.NextAttemptAt.Equal(row.OccurredAt) {
			t.Fatalf("outbox row %+v does not match envelope %+v", row, env)
		}
	}
	if len(envs) != 2 {
		t.Fatalf("events = %d, want Processed + BalanceChanged", len(envs))
	}

	if err := outbox.Append(ctx, envs[0]); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("republishing the same eventId into the outbox: err %v, want ErrConflict", err)
	}
}

func TestLedgerAppendRollsBackWithTransaction(t *testing.T) {
	db := newTestDB(t)
	tm := testTxManager(db, time.Second)
	wallets, txs, ledger := NewWalletRepository(db), NewTransactionRepository(db), NewLedgerRepository(db)
	ctx := context.Background()

	w := newWallet(t, "100.00")
	bet, _ := wagering.NewExternal(uuid.New(), externalCommand(t, w, "BET", "10.00", ""), time.Now())
	boom := errors.New("crash before commit")
	err := tm.WithinTx(ctx, func(ctx context.Context) error {
		if err := wallets.Create(ctx, w); err != nil {
			return err
		}
		if _, err := txs.Insert(ctx, bet); err != nil {
			return err
		}
		entry, err := bet.Process(wagering.ProcessInput{Wallet: w, LedgerEntryID: uuid.New(), RetryPolicy: wagering.DefaultRetryPolicy(), Now: time.Now()})
		if err != nil {
			return err
		}
		if err := ledger.Append(ctx, *entry); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatal(err)
	}
	if _, err := wallets.Get(ctx, w.ID()); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("wallet must not exist after rollback: %v", err)
	}
	var n int64
	db.Table("wallet_ledger_entries").Where("transaction_id = ?", bet.ID()).Count(&n)
	if n != 0 {
		t.Fatal("ledger entry must not survive the rollback")
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/adapters/postgres/ -run 'Bet|Ledger'`
Expected: FAIL de compilação, `undefined: NewLedgerRepository`.

- [ ] **Step 3: Implementar**

Acrescentar a `internal/adapters/postgres/models.go`:

```go
type ledgerEntryModel struct {
	ID                 uuid.UUID `gorm:"column:id;primaryKey"`
	WalletID           uuid.UUID `gorm:"column:wallet_id"`
	TransactionID      uuid.UUID `gorm:"column:transaction_id"`
	Direction          string    `gorm:"column:direction"`
	AmountMinor        int64     `gorm:"column:amount_minor"`
	Currency           string    `gorm:"column:currency"`
	BalanceBeforeMinor int64     `gorm:"column:balance_before_minor"`
	BalanceAfterMinor  int64     `gorm:"column:balance_after_minor"`
	CreatedAt          time.Time `gorm:"column:created_at;autoCreateTime:false"`
}

func (ledgerEntryModel) TableName() string { return "wallet_ledger_entries" }

type outboxEventModel struct {
	ID            uuid.UUID  `gorm:"column:id;primaryKey"`
	AggregateType string     `gorm:"column:aggregate_type"`
	AggregateID   uuid.UUID  `gorm:"column:aggregate_id"`
	EventType     string     `gorm:"column:event_type"`
	Payload       []byte     `gorm:"column:payload;type:jsonb"`
	OccurredAt    time.Time  `gorm:"column:occurred_at"`
	Attempts      int        `gorm:"column:attempts"`
	NextAttemptAt time.Time  `gorm:"column:next_attempt_at"`
	LockedBy      *string    `gorm:"column:locked_by"`
	LockedUntil   *time.Time `gorm:"column:locked_until"`
	PublishedAt   *time.Time `gorm:"column:published_at"`
	LastError     *string    `gorm:"column:last_error"`
}

func (outboxEventModel) TableName() string { return "outbox_events" }
```

`internal/adapters/postgres/ledger_repository.go`:

```go
package postgres

import (
	"context"

	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

var _ app.LedgerRepository = (*LedgerRepository)(nil)

// LedgerRepository appends ledger entries. The table is append-only: the
// application role cannot update or delete, and triggers block the owner too.
type LedgerRepository struct {
	db *gorm.DB
}

// NewLedgerRepository builds a LedgerRepository.
func NewLedgerRepository(db *gorm.DB) *LedgerRepository { return &LedgerRepository{db: db} }

// Append inserts e; a second entry for the same (wallet, transaction) is app.ErrConflict.
func (r *LedgerRepository) Append(ctx context.Context, e wallet.LedgerEntry) error {
	m := ledgerEntryModel{
		ID:                 e.ID(),
		WalletID:           e.WalletID(),
		TransactionID:      e.TransactionID(),
		Direction:          string(e.Direction()),
		AmountMinor:        e.Amount().MinorUnits(),
		Currency:           e.Amount().Currency().Code(),
		BalanceBeforeMinor: e.BalanceBefore().MinorUnits(),
		BalanceAfterMinor:  e.BalanceAfter().MinorUnits(),
		CreatedAt:          e.CreatedAt(),
	}
	return mapError(conn(ctx, r.db).Create(&m).Error)
}
```

`internal/adapters/postgres/outbox_repository.go`:

```go
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
)

var _ app.OutboxRepository = (*OutboxRepository)(nil)

// OutboxAggregateWallet is the aggregate type of every event (aggregateId = walletId).
const OutboxAggregateWallet = "Wallet"

// OutboxRepository writes event snapshots in the caller's transaction, so an
// event exists only if the change that produced it committed.
type OutboxRepository struct {
	db *gorm.DB
}

// NewOutboxRepository builds an OutboxRepository.
func NewOutboxRepository(db *gorm.DB) *OutboxRepository { return &OutboxRepository{db: db} }

// Append stores envs as pending events ready to publish at their occurrence time.
func (r *OutboxRepository) Append(ctx context.Context, envs ...events.Envelope) error {
	if len(envs) == 0 {
		return nil
	}
	rows := make([]outboxEventModel, 0, len(envs))
	for _, env := range envs {
		payload, err := json.Marshal(env)
		if err != nil {
			return fmt.Errorf("postgres: outbox %s: %w", env.EventID, err)
		}
		occurredAt, err := time.Parse(time.RFC3339Nano, env.OccurredAt)
		if err != nil {
			return fmt.Errorf("postgres: outbox %s occurredAt: %w", env.EventID, err)
		}
		rows = append(rows, outboxEventModel{
			ID:            env.EventID,
			AggregateType: OutboxAggregateWallet,
			AggregateID:   env.AggregateID,
			EventType:     env.EventType,
			Payload:       payload,
			OccurredAt:    occurredAt,
			NextAttemptAt: occurredAt,
		})
	}
	return mapError(conn(ctx, r.db).Create(&rows).Error)
}
```

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=integration ./internal/adapters/postgres/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/adapters/postgres
git commit -m "feat(postgres): ledger and outbox repositories in the caller's transaction" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 10: Módulos Fx de config e Postgres com ciclo de vida

**Files:**
- Create: `internal/platform/config/module.go`, `internal/adapters/postgres/module.go`
- Test: `internal/adapters/postgres/module_test.go` (integration, pacote externo `postgres_test`)

**Interfaces:**
- Consumes: `config.Load`, `postgres.Open`, os construtores de repositório e `app.*`.
- Produces:
  - `config.Module` (fornece `config.Config` a partir de `os.Getenv`).
  - `postgres.Module`, que fornece `*gorm.DB` com lifecycle (OnStart faz o ping e OnStop fecha o pool), além de
    `app.TxManager`, `app.WalletRepository`, `app.TransactionRepository`, `app.LedgerRepository` e
    `app.OutboxRepository`.
  - `postgres.NewDB(lc fx.Lifecycle, cfg config.Config) (*gorm.DB, error)`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/adapters/postgres/module_test.go`:

```go
//go:build !unit && !e2e

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/postgres"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func TestModuleGraphIsValid(t *testing.T) {
	t.Setenv("DATABASE_URL", pgtest.AppURL())
	if err := fx.ValidateApp(fx.NopLogger, config.Module, postgres.Module,
		fx.Invoke(func(app.TxManager, app.WalletRepository, app.TransactionRepository, app.LedgerRepository, app.OutboxRepository) {}),
	); err != nil {
		t.Fatal(err)
	}
}

func TestModuleLifecycle(t *testing.T) {
	pgtest.AppDB(t) // clear failure when the test database is down
	t.Setenv("DATABASE_URL", pgtest.AppURL())

	var (
		db      *gorm.DB
		tm      app.TxManager
		wallets app.WalletRepository
	)
	fxApp := fxtest.New(t, fx.NopLogger, config.Module, postgres.Module, fx.Populate(&db, &tm, &wallets))
	fxApp.RequireStart()

	brl, _ := money.Parse("10.00", "BRL")
	w, _, err := wallet.Open(wallet.OpenParams{
		ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl,
		OpeningTransactionID: uuid.New(), LedgerEntryID: uuid.New(), Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	err = tm.WithinTx(context.Background(), func(ctx context.Context) error { return wallets.Create(ctx, w) })
	if err != nil {
		t.Fatalf("repositories must work after start: %v", err)
	}

	fxApp.RequireStop()
	sqlDB, _ := db.DB()
	if err := sqlDB.PingContext(context.Background()); err == nil {
		t.Fatal("the pool must be closed after stop")
	}
}

func TestModuleStartFailsWithoutDatabase(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://wallet_app:wallet_app@127.0.0.1:1/wallet?sslmode=disable&connect_timeout=1")
	fxApp := fx.New(fx.NopLogger, config.Module, postgres.Module, fx.Invoke(func(*gorm.DB) {}))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fxApp.Start(ctx); err == nil {
		_ = fxApp.Stop(ctx)
		t.Fatal("start must fail when postgres is unreachable")
	}
}

func TestModuleRejectsInvalidConfig(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	fxApp := fx.New(fx.NopLogger, config.Module, postgres.Module, fx.Invoke(func(*gorm.DB) {}))
	if fxApp.Err() == nil {
		t.Fatal("missing DATABASE_URL must fail before start")
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/adapters/postgres/ -run Module`
Expected: FAIL de compilação, `undefined: config.Module` e `undefined: postgres.Module`.

- [ ] **Step 3: Implementar**

`internal/platform/config/module.go`:

```go
package config

import (
	"os"

	"go.uber.org/fx"
)

// Module provides Config from the process environment. Invalid configuration
// fails the Fx graph before any component starts.
var Module = fx.Module("config",
	fx.Provide(func() (Config, error) { return Load(os.Getenv) }),
)
```

`internal/adapters/postgres/module.go`:

```go
package postgres

import (
	"context"
	"fmt"

	"go.uber.org/fx"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

// Module wires the database handle, the transaction manager and the
// repositories, exposed as application ports.
var Module = fx.Module("postgres",
	fx.Provide(
		NewDB,
		fx.Annotate(NewTxManager, fx.As(new(app.TxManager))),
		fx.Annotate(NewWalletRepository, fx.As(new(app.WalletRepository))),
		fx.Annotate(NewTransactionRepository, fx.As(new(app.TransactionRepository))),
		fx.Annotate(NewLedgerRepository, fx.As(new(app.LedgerRepository))),
		fx.Annotate(NewOutboxRepository, fx.As(new(app.OutboxRepository))),
	),
)

// NewDB opens the pool and binds it to the Fx lifecycle: OnStart verifies
// connectivity (the process does not start without PostgreSQL), OnStop closes
// the pool. Fx stops components in reverse order, so everything that uses the
// pool has stopped before it closes.
func NewDB(lc fx.Lifecycle, cfg config.Config) (*gorm.DB, error) {
	db, err := Open(cfg.Database)
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("postgres: pool: %w", err)
	}
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			if err := sqlDB.PingContext(ctx); err != nil {
				return fmt.Errorf("postgres: ping: %w", err)
			}
			return nil
		},
		OnStop: func(context.Context) error { return sqlDB.Close() },
	})
	return db, nil
}
```

- [ ] **Step 4: Verificação final do plano**

Run:

```bash
docker compose --profile test up -d --wait postgres_test
docker compose --profile test run --rm migrate_test
gofmt -l . && go vet ./... && go test -race ./... && go test -race -tags=unit ./... && go test -race -tags=integration ./...
go list -f '{{join .Imports "\n"}}' ./internal/domain/... | sort -u
```

Expected:
- `gofmt -l` sem saída e `go vet` limpo.
- Os três `go test` com `ok`: o `-tags=unit` roda sem precisar de infra, e o sem tag roda tudo.
- Os imports do domínio continuam só stdlib, uuid e `internal/domain`.

Validar também o caminho dev:

```bash
docker compose up -d --wait postgres && docker compose run --rm migrate
docker compose exec -T postgres psql -U postgres -d wallet -Atc "SELECT version FROM schema_migrations"
```

Expected: `5`.

- [ ] **Step 5: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/platform/config internal/adapters/postgres
git commit -m "feat(postgres): fx modules for config and database lifecycle" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
