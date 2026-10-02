# Plano 3 — Casos de Uso: Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implementar a camada de aplicação (`internal/app`) sobre os ports do Plano 2:
- abertura de carteira com OPENING;
- processamento idempotente de operações externas (replay, conflitos, referências, PENDING_REFERENCE, REJECTED,
  FAILED e erros transitórios);
- consultas com escopo por provedor e paginação do ledger por cursor opaco;
- reconciliação em snapshot consistente;
- módulo Fx.

Tudo é testado contra o Postgres real, incluindo os cenários de concorrência da seção 13 do enunciado.

**Architecture:** Os casos de uso dependem só dos ports de `internal/app` (TxManager e repositórios), de um `Clock`, de
um port `Metrics` e de `*slog.Logger`. O domínio (`internal/domain`) decide as regras, e os casos de uso delimitam
a transação e orquestram:

```
lookup idempotente → INSERT PENDING → lock da carteira → resolve referência → Process → persiste → outbox
```

Erros transitórios nunca viram FAILED. Uma falha permanente desfaz a transação principal e grava FAILED numa transação
separada. `Process` aceita um gancho `alongside(ctx)` executado **na mesma transação SQL** do resultado. O consumidor
SQS (Plano 5) o usa para gravar a inbox atomicamente, sem transações aninhadas.

**Tech Stack:** Go 1.25.11, GORM/pgx (via adapter do Plano 2), `go.uber.org/fx`, `log/slog`, `testing`.

**Spec:** `docs/superpowers/specs/2026-10-01-wallet-service-design.md` (seções 4.3, 5, 7, 8, 10 e 13). Enunciado: `docs/CHALLENGE.md`.

## Roadmap (reorganizado)

1. ~~Fundação e domínio puro~~ (concluído).
2. ~~Infra local e persistência~~ (concluído).
3. **Casos de uso** (este plano).
4. **HTTP e auth:** Keycloak no compose (dev e test) com realm importado, middleware go-oidc com scopes e
   `provider_id`, handlers Gin, mapeamento de erros e status, health, logger slog JSON, métricas Prometheus (que
   implementam `app.Metrics`), `cmd/wallet` com Fx e testes com tokens reais.
5. **Mensageria:** MiniStack (filas, IAM e contas), consumer SQS com inbox via `alongside`, outbox publisher com lease,
   worker de referências e DLQ.
6. **Entrega:** Dockerfile, nginx com 3 réplicas, Prometheus e Grafana, E2E com 3 processos e `faultinject`,
   README, ARCHITECTURE.md e .env.example.

## Global Constraints

- Módulo `github.com/brunopstephan/backend-challenge-go`, diretiva `go 1.25.11`. `internal/domain/**` **não muda**.
- `internal/app` não importa Gin, GORM, AWS nem `internal/adapters/**`. Pode importar o domínio, `uuid`, `log/slog`
  e, só em `module.go`, `go.uber.org/fx` e `internal/platform/config`.
- Dinheiro nunca passa por float.
- **Transitório nunca vira FAILED.** Isso inclui `app.ErrTransient`, `ErrVersionConflict`, `ErrLockTimeout`,
  cancelamento, prazo e conflito único dentro do processamento.
- `WageringService.Process` **não pode ser chamado dentro de uma transação**: o trabalho atômico do chamador vai no
  `alongside`.
- Logs (slog) incluem `correlationId`, `transactionId`, `walletId`, `providerId` e `messageId` quando disponíveis.
  Nunca registram tokens nem payloads financeiros completos.
- Tags de build:
  - teste unitário: `//go:build !integration && !e2e`;
  - teste de integração: `//go:build !unit && !e2e`.

  Os testes de integração usam o `postgres_test` (`docker compose --profile test up -d --wait postgres_test &&
  docker compose --profile test run --rm migrate_test`), dados próprios (UUIDs novos), sem truncar.
- Testes só com `testing`. Nunca chamar `t.Fatal` fora da goroutine do teste.
- Código formatado com `gofmt`, e `go vet ./...` limpo.
- Toda mensagem de commit termina exatamente com `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

## Review Focus

1. A mesma aposta enviada 50 vezes em paralelo produz exatamente um débito. As outras 49 respostas são replays.
   Teste na Task 6.
2. O replay devolve o saldo do processamento original mesmo depois de outras movimentações. Teste na Task 3.
3. Uma requisição cancelada pelo cliente não deixa linha FAILED nem nenhum efeito. Teste na Task 5.
4. Um REFUND que chega antes da BET fica `PENDING_REFERENCE`, com evento e com o agendamento persistidos. Teste na
   Task 4.
5. A reconciliação reporta divergência sem alterar o saldo armazenado. Teste na Task 8.

## File Structure

```
internal/app/
  errors.go                 (modify) + ErrLockTimeout, ErrWalletAlreadyExists, ErrIdempotencyKeyConflict,
                            ErrExternalTransactionConflict, ErrInvalidCursor, ErrInvalidLimit
  ports.go                  (modify) + TxManager.WithinSnapshot, LedgerRepository.List/Totals
  app.go                    Clock, SystemClock, Channel, Meta, Viewer, Deps, newID, publish, run
  metrics.go                Metrics port, NopMetrics, conflict reasons
  wallet_service.go         OpenWalletCommand, ParseOpenWallet, WalletService
  wagering_service.go       TransactionResult, WageringService (Process, lookup, apply, resolveReference, failure, recordFailure)
  query_service.go          QueryService, LedgerPage, cursor
  reconciliation_service.go Reconciliation, ReconciliationService
  module.go                 fx.Module "app"
internal/adapters/postgres/
  errors.go, tx.go, ledger_repository.go, mapping.go  (modify)
internal/platform/config/config.go                   (modify) + Wagering section
internal/testsupport/apptest/apptest.go               Harness, Clock, RecordingMetrics, Command, OpenWallet, OutboxRows
```

---

### Task 1: Extensões de persistência — ErrLockTimeout, snapshot read-only e consultas do ledger

**Files:**
- Modify: `internal/app/errors.go`, `internal/app/ports.go`, `internal/adapters/postgres/errors.go`, `internal/adapters/postgres/tx.go`, `internal/adapters/postgres/ledger_repository.go`, `internal/adapters/postgres/mapping.go`
- Test: `internal/app/errors_test.go` (unit), `internal/adapters/postgres/errors_test.go` (unit), `internal/adapters/postgres/tx_test.go` (integration), `internal/adapters/postgres/ledger_query_test.go` (integration)

**Interfaces:**
- Consumes: `newTestDB`, `testTxManager`, `walletRow`, `newWallet`, `externalCommand` e `nowUS`, que são helpers
  de teste existentes no pacote `postgres`.
- Produces:
  - `app.ErrLockTimeout`, que também casa com `app.ErrTransient`.
  - `app.TxManager.WithinSnapshot(ctx, fn) error`.
  - `app.LedgerRepository.List(ctx, walletID, after uuid.UUID, limit int) ([]wallet.LedgerEntry, error)`.
  - `app.LedgerRepository.Totals(ctx, walletID uuid.UUID) (net int64, count int64, err error)`.
  - `postgres.ledgerEntryFromModel`.

- [ ] **Step 1: Escrever os testes que falham**

Acrescentar a `internal/app/errors_test.go`:

```go
func TestLockTimeoutIsTransient(t *testing.T) {
	if !errors.Is(ErrLockTimeout, ErrTransient) {
		t.Fatal("ErrLockTimeout must match ErrTransient")
	}
	wrapped := fmt.Errorf("op: %w", ErrLockTimeout)
	if !errors.Is(wrapped, ErrLockTimeout) || !errors.Is(wrapped, ErrTransient) {
		t.Fatal("wrapped ErrLockTimeout must match both sentinels")
	}
	if errors.Is(ErrTransient, ErrLockTimeout) || errors.Is(ErrVersionConflict, ErrLockTimeout) {
		t.Fatal("ErrLockTimeout must stay a distinct sentinel")
	}
}
```

(garanta os imports `errors`, `fmt`, `testing` no arquivo).

Acrescentar a `internal/adapters/postgres/errors_test.go`:

```go
func TestMapErrorLockTimeout(t *testing.T) {
	got := mapError(fmt.Errorf("query: %w", &pgconn.PgError{Code: "55P03"}))
	if !errors.Is(got, app.ErrLockTimeout) || !errors.Is(got, app.ErrTransient) {
		t.Fatalf("mapError(55P03) = %v, want ErrLockTimeout (and ErrTransient)", got)
	}
	if got := mapError(fmt.Errorf("query: %w", &pgconn.PgError{Code: "40P01"})); errors.Is(got, app.ErrLockTimeout) {
		t.Fatalf("deadlock must not be reported as lock timeout: %v", got)
	}
}
```

Acrescentar a `internal/adapters/postgres/tx_test.go`:

```go
func TestWithinSnapshotSeesOneConsistentSnapshot(t *testing.T) {
	db := newTestDB(t)
	tm := testTxManager(db, time.Second)
	ctx := context.Background()
	player := uuid.New()
	if err := db.Table("wallets").Create(walletRow(player, "BRL", 0)).Error; err != nil {
		t.Fatal(err)
	}

	err := tm.WithinSnapshot(ctx, func(ctx context.Context) error {
		var before, after int64
		if err := conn(ctx, db).Table("wallets").Where("player_id = ?", player).Count(&before).Error; err != nil {
			return err
		}
		// Committed by another transaction after the snapshot started: invisible inside it.
		if err := db.Table("wallets").Create(walletRow(player, "USD", 0)).Error; err != nil {
			return err
		}
		if err := conn(ctx, db).Table("wallets").Where("player_id = ?", player).Count(&after).Error; err != nil {
			return err
		}
		if before != 1 || after != 1 {
			t.Errorf("snapshot saw %d then %d wallets, want 1 and 1", before, after)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWithinSnapshotIsReadOnly(t *testing.T) {
	db := newTestDB(t)
	tm := testTxManager(db, time.Second)
	err := tm.WithinSnapshot(context.Background(), func(ctx context.Context) error {
		return conn(ctx, db).Table("wallets").Create(walletRow(uuid.New(), "BRL", 0)).Error
	})
	pgtest.RequirePgError(t, err, "25006", "") // read_only_sql_transaction
}

func TestWithinSnapshotRefusesToNest(t *testing.T) {
	db := newTestDB(t)
	tm := testTxManager(db, time.Second)
	err := tm.WithinTx(context.Background(), func(ctx context.Context) error {
		return tm.WithinSnapshot(ctx, func(context.Context) error { return nil })
	})
	if err == nil {
		t.Fatal("WithinSnapshot inside WithinTx must fail")
	}
}
```

Criar `internal/adapters/postgres/ledger_query_test.go`:

```go
//go:build !unit && !e2e

package postgres

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

// seedLedger persists a wallet opened with 100.00 and four 1.00 bets: five
// ledger entries, net 96.00. It returns the wallet id and the entry ids.
func seedLedger(t *testing.T) (uuid.UUID, []uuid.UUID) {
	t.Helper()
	db := newTestDB(t)
	ctx := context.Background()
	wallets, txs, ledger := NewWalletRepository(db), NewTransactionRepository(db), NewLedgerRepository(db)

	openingID, entryID := mustV7(t), mustV7(t)
	w, opening, err := wallet.Open(wallet.OpenParams{
		ID: mustV7(t), PlayerID: uuid.New(), InitialBalance: brlMoney(t, "100.00"),
		OpeningTransactionID: openingID, LedgerEntryID: entryID, Now: nowUS(),
	})
	if err != nil {
		t.Fatal(err)
	}
	openingTx, err := wagering.NewOpening(openingID, w.ID(), w.PlayerID(), brlMoney(t, "100.00"), nowUS())
	if err != nil {
		t.Fatal(err)
	}
	if err := openingTx.CompleteOpening(w, *opening, nowUS()); err != nil {
		t.Fatal(err)
	}
	entries := []wallet.LedgerEntry{*opening}
	persisted := []*wagering.WagerTransaction{openingTx}
	for range 4 {
		bet, err := wagering.NewExternal(mustV7(t), externalCommand(t, w, "BET", "1.00", ""), nowUS())
		if err != nil {
			t.Fatal(err)
		}
		entry, err := bet.Process(wagering.ProcessInput{
			Wallet: w, LedgerEntryID: mustV7(t), RetryPolicy: wagering.DefaultRetryPolicy(), Now: nowUS(),
		})
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, *entry)
		persisted = append(persisted, bet)
	}
	if err := wallets.Create(ctx, w); err != nil {
		t.Fatal(err)
	}
	for _, tx := range persisted {
		if _, err := txs.Insert(ctx, tx); err != nil {
			t.Fatal(err)
		}
	}
	ids := make([]uuid.UUID, 0, len(entries))
	for _, e := range entries {
		if err := ledger.Append(ctx, e); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, e.ID())
	}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return bytes.Compare(a[:], b[:]) })
	return w.ID(), ids
}

func mustV7(t *testing.T) uuid.UUID {
	t.Helper()
	id, err := uuid.NewV7()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func entryIDs(entries []wallet.LedgerEntry) []uuid.UUID {
	ids := make([]uuid.UUID, len(entries))
	for i, e := range entries {
		ids[i] = e.ID()
	}
	return ids
}

func TestLedgerListPagesInIDOrder(t *testing.T) {
	walletID, want := seedLedger(t)
	ledger := NewLedgerRepository(newTestDB(t))
	ctx := context.Background()

	var got []uuid.UUID
	after := uuid.Nil
	for page := 0; ; page++ {
		entries, err := ledger.List(ctx, walletID, after, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) == 0 {
			break
		}
		if len(entries) > 2 {
			t.Fatalf("page %d has %d entries, limit 2", page, len(entries))
		}
		got = append(got, entryIDs(entries)...)
		after = entries[len(entries)-1].ID()
	}
	if !slices.Equal(got, want) {
		t.Fatalf("paged ids %v, want %v", got, want)
	}

	all, err := ledger.List(ctx, walletID, uuid.Nil, 50)
	if err != nil || len(all) != 5 || all[0].Direction() != wallet.DirectionCredit || all[0].Amount().Amount() != "100.00" {
		t.Fatalf("List all = %d entries, err %v", len(all), err)
	}
	empty, err := ledger.List(ctx, uuid.New(), uuid.Nil, 50)
	if err != nil || len(empty) != 0 {
		t.Fatalf("unknown wallet: %d entries, err %v", len(empty), err)
	}
}

func TestLedgerTotals(t *testing.T) {
	walletID, _ := seedLedger(t)
	ledger := NewLedgerRepository(newTestDB(t))
	net, count, err := ledger.Totals(context.Background(), walletID)
	if err != nil || net != 9600 || count != 5 {
		t.Fatalf("Totals = %d, %d, %v; want 9600, 5", net, count, err)
	}
	net, count, err = ledger.Totals(context.Background(), uuid.New())
	if err != nil || net != 0 || count != 0 {
		t.Fatalf("Totals(unknown) = %d, %d, %v; want 0, 0", net, count, err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test -tags=unit ./internal/app/... ./internal/adapters/postgres/... ; go test -tags=integration ./internal/adapters/postgres/...`
Expected: FAIL de compilação, `undefined: ErrLockTimeout`, `tm.WithinSnapshot undefined` e `ledger.List undefined`.

- [ ] **Step 3: Implementar**

Em `internal/app/errors.go`, dentro do bloco `var`:

```go
	// ErrLockTimeout reports a row lock not granted within lock_timeout. It
	// also matches ErrTransient.
	ErrLockTimeout = fmt.Errorf("app: lock timeout: %w", ErrTransient)
```

Em `internal/app/ports.go`, acrescentar a `TxManager`:

```go
	// WithinSnapshot runs fn in a read-only REPEATABLE READ transaction: every
	// read sees one consistent snapshot and writes are refused. It never joins
	// an outer transaction.
	WithinSnapshot(ctx context.Context, fn func(ctx context.Context) error) error
```

e a `LedgerRepository`:

```go
	// List returns up to limit entries of walletID with id greater than after
	// (uuid.Nil for the first page), ordered by id.
	List(ctx context.Context, walletID, after uuid.UUID, limit int) ([]wallet.LedgerEntry, error)
	// Totals returns credits minus debits in minor units and the entry count.
	Totals(ctx context.Context, walletID uuid.UUID) (net int64, count int64, err error)
```

Em `internal/adapters/postgres/errors.go`, no `switch` do `*pgconn.PgError`, **antes** do caso transitório:

```go
		case pgErr.Code == "55P03":
			return fmt.Errorf("%w: %w", app.ErrLockTimeout, err)
```

Substituir `WithinTx` em `internal/adapters/postgres/tx.go` e acrescentar `WithinSnapshot` (acrescente os imports
`database/sql` e `errors`):

```go
// errNestedSnapshot reports WithinSnapshot called inside another transaction.
var errNestedSnapshot = errors.New("postgres: WithinSnapshot cannot run inside another transaction")

// WithinTx runs fn in a READ COMMITTED transaction with transaction-local
// lock_timeout and statement_timeout. A call inside an existing transaction
// joins it. Errors are classified with mapError.
func (m *TxManager) WithinTx(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFrom(ctx); ok {
		return fn(ctx)
	}
	return m.run(ctx, nil, fn)
}

// WithinSnapshot runs fn in a REPEATABLE READ READ ONLY transaction, so all
// reads see the same snapshot (used by reconciliation). It never joins an
// existing transaction.
func (m *TxManager) WithinSnapshot(ctx context.Context, fn func(ctx context.Context) error) error {
	if _, ok := txFrom(ctx); ok {
		return errNestedSnapshot
	}
	return m.run(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true}, fn)
}

func (m *TxManager) run(ctx context.Context, opts *sql.TxOptions, fn func(ctx context.Context) error) error {
	var options []*sql.TxOptions
	if opts != nil {
		options = append(options, opts)
	}
	err := m.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		err := tx.Exec("SELECT set_config('lock_timeout', ?, true), set_config('statement_timeout', ?, true)",
			millis(m.lockTimeout), millis(m.statementTimeout)).Error
		if err != nil {
			return err
		}
		return fn(context.WithValue(ctx, txKey{}, tx))
	}, options...)
	return mapError(err)
}
```

Em `internal/adapters/postgres/mapping.go`:

```go
func ledgerEntryFromModel(m ledgerEntryModel) (wallet.LedgerEntry, error) {
	amount, err := moneyFrom(m.AmountMinor, m.Currency)
	if err != nil {
		return wallet.LedgerEntry{}, fmt.Errorf("postgres: ledger entry %s: %w", m.ID, err)
	}
	before, err := moneyFrom(m.BalanceBeforeMinor, m.Currency)
	if err != nil {
		return wallet.LedgerEntry{}, fmt.Errorf("postgres: ledger entry %s: %w", m.ID, err)
	}
	after, err := moneyFrom(m.BalanceAfterMinor, m.Currency)
	if err != nil {
		return wallet.LedgerEntry{}, fmt.Errorf("postgres: ledger entry %s: %w", m.ID, err)
	}
	return wallet.NewLedgerEntry(wallet.LedgerEntryParams{
		ID:            m.ID,
		WalletID:      m.WalletID,
		TransactionID: m.TransactionID,
		Direction:     wallet.Direction(m.Direction),
		Amount:        amount,
		BalanceBefore: before,
		BalanceAfter:  after,
		CreatedAt:     m.CreatedAt,
	})
}
```

Em `internal/adapters/postgres/ledger_repository.go` (acrescente o import `github.com/google/uuid`):

```go
// List returns a page of entries ordered by id (UUIDv7, stable).
func (r *LedgerRepository) List(ctx context.Context, walletID, after uuid.UUID, limit int) ([]wallet.LedgerEntry, error) {
	var rows []ledgerEntryModel
	err := conn(ctx, r.db).Where("wallet_id = ? AND id > ?", walletID, after).Order("id").Limit(limit).Find(&rows).Error
	if err != nil {
		return nil, mapError(err)
	}
	entries := make([]wallet.LedgerEntry, 0, len(rows))
	for _, m := range rows {
		e, err := ledgerEntryFromModel(m)
		if err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// Totals sums credits minus debits and counts the entries of walletID. The
// ::bigint cast fails loudly (22003) instead of overflowing silently.
func (r *LedgerRepository) Totals(ctx context.Context, walletID uuid.UUID) (int64, int64, error) {
	var row struct {
		Net   int64
		Count int64
	}
	err := conn(ctx, r.db).Raw(`
		SELECT COALESCE(SUM(CASE WHEN direction = 'CREDIT' THEN amount_minor ELSE -amount_minor END), 0)::bigint AS net,
		       COUNT(*) AS count
		FROM wallet_ledger_entries
		WHERE wallet_id = ?`, walletID).Scan(&row).Error
	if err != nil {
		return 0, 0, mapError(err)
	}
	return row.Net, row.Count, nil
}
```

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=unit ./... && go test -race ./...`
Expected: `ok` em todos os pacotes.

- [ ] **Step 5: Commit**

```bash
git add internal/app internal/adapters/postgres
git commit -m "feat(postgres): lock-timeout sentinel, read-only snapshot transactions and ledger queries" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 2: Fundamentos da aplicação e abertura de carteira

**Files:**
- Create: `internal/app/app.go`, `internal/app/metrics.go`, `internal/app/wallet_service.go`, `internal/testsupport/apptest/apptest.go`
- Modify: `internal/app/errors.go`
- Test: `internal/app/wallet_service_unit_test.go` (unit), `internal/app/wallet_service_test.go` (integration, package `app_test`)

**Interfaces:**
- Consumes: os ports, `postgres.Open`, `NewTxManager`, os repositórios, `config.Database`, `pgtest.AppURL`/`AppDB`;
  e `wallet.Open`, `wagering.NewOpening`, `CompleteOpening`, `events.NewEnvelope` e `money.Parse`.
- Produces:
  - Tempo: `app.Clock` (`func() time.Time`) e `app.SystemClock()`.
  - Canais: `app.Channel` (`ChannelHTTP`, `ChannelSQS`, `ChannelWorker`).
  - Contexto da chamada: `app.Meta{CorrelationID, CausationID string; Channel Channel}` e
    `app.Viewer{ProviderID string; Internal bool}`.
  - Dependências: `app.Deps{Tx TxManager; Wallets WalletRepository; Transactions TransactionRepository;
    Ledger LedgerRepository; Outbox OutboxRepository; Clock Clock; Metrics Metrics; Log *slog.Logger}`.
  - Métricas: `app.Metrics` (`TransactionCompleted`, `IdempotentReplay`, `ConcurrencyConflict`,
    `ProcessingDuration`, `ReconciliationDivergence`), `app.NopMetrics` e as constantes `ConflictLockTimeout`,
    `ConflictVersion`, `ConflictUnique` e `ConflictTransient`.
  - Erros: `app.ErrWalletAlreadyExists`, `ErrIdempotencyKeyConflict`, `ErrExternalTransactionConflict`,
    `ErrInvalidCursor` e `ErrInvalidLimit`.
  - Carteira: `app.OpenWalletCommand{PlayerID uuid.UUID; InitialBalance money.Money}`,
    `app.ParseOpenWallet(playerID, amount, currency string) (OpenWalletCommand, error)`,
    `app.NewWalletService(d Deps) *WalletService` e `(*WalletService).Open(ctx, cmd, meta) (*wallet.Wallet, error)`.
  - Unexported: `newID()`, `run(ctx, fn)`, `publish(ctx, outbox, now, data, meta)` e `(Meta).normalized()`.
  - Testes (`apptest`):
    - `apptest.New(t) *Harness`, com os campos `DB`, `Deps`, `Tx`, `Wallets`, `Transactions`, `Ledger`, `Outbox`
      e `Metrics *RecordingMetrics`.
    - `apptest.Clock()` e `apptest.NewRecordingMetrics()`, com `(*RecordingMetrics).Count(key string) int`.
    - `(*Harness).OpenWallet(t, balance string) *wallet.Wallet`.
    - `(*Harness).OutboxRows(t, walletID) []OutboxRow`, onde
      `OutboxRow{EventType, CorrelationID, CausationID string; Data map[string]any}`.
    - `apptest.Command(t, w *wallet.Wallet, provider, kind, amount, ref string) wagering.Command`.

- [ ] **Step 1: Escrever o teste unitário que falha**

`internal/app/wallet_service_unit_test.go`:

```go
//go:build !integration && !e2e

package app

import (
	"errors"
	"testing"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

func TestParseOpenWallet(t *testing.T) {
	cmd, err := ParseOpenWallet("0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "1000.00", "BRL")
	if err != nil || cmd.InitialBalance.Amount() != "1000.00" || cmd.PlayerID.String() != "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1" {
		t.Fatalf("ParseOpenWallet = %+v, %v", cmd, err)
	}
	if _, err := ParseOpenWallet("0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "0.00", "BRL"); err != nil {
		t.Fatalf("zero initial balance is allowed: %v", err)
	}

	tests := []struct {
		name, player, amount, currency string
		code                           wagering.FailureCode
		field                          string
	}{
		{"missing player", "", "1.00", "BRL", wagering.FailureMissingField, "playerId"},
		{"uppercase player", "0192F28F-5DC0-7D58-BDB2-814AD6A0F4A1", "1.00", "BRL", wagering.FailureInvalidID, "playerId"},
		{"nil player", "00000000-0000-0000-0000-000000000000", "1.00", "BRL", wagering.FailureInvalidID, "playerId"},
		{"missing amount", "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "", "BRL", wagering.FailureMissingField, "initialBalance.amount"},
		{"missing currency", "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "1.00", "", wagering.FailureMissingField, "initialBalance.currency"},
		{"negative", "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "-1.00", "BRL", wagering.FailureInvalidMoney, "initialBalance.amount"},
		{"bad scale", "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "1.0", "BRL", wagering.FailureInvalidMoney, "initialBalance.amount"},
		{"bad currency", "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "1.00", "JPY", wagering.FailureUnsupportedCurrency, "initialBalance.currency"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseOpenWallet(tt.player, tt.amount, tt.currency)
			var ie *wagering.InputError
			if !errors.As(err, &ie) {
				t.Fatalf("error = %v, want *wagering.InputError", err)
			}
			for _, v := range ie.Violations {
				if v.Code == tt.code && v.Field == tt.field {
					return
				}
			}
			t.Fatalf("violations %+v lack %s on %s", ie.Violations, tt.code, tt.field)
		})
	}
}

func TestMetaNormalized(t *testing.T) {
	m := Meta{}.normalized()
	if m.CorrelationID == "" || m.Channel != ChannelHTTP {
		t.Fatalf("normalized = %+v", m)
	}
	kept := Meta{CorrelationID: "c", CausationID: "m", Channel: ChannelSQS}.normalized()
	if kept != (Meta{CorrelationID: "c", CausationID: "m", Channel: ChannelSQS}) {
		t.Fatalf("normalized changed explicit values: %+v", kept)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=unit ./internal/app/...`
Expected: FAIL de compilação, `undefined: ParseOpenWallet`.

- [ ] **Step 3: Implementar os fundamentos**

Acrescentar a `internal/app/errors.go`, no bloco `var`:

```go
	// ErrWalletAlreadyExists reports a second wallet for the same (player, currency).
	ErrWalletAlreadyExists = errors.New("app: wallet already exists for player and currency")
	// ErrIdempotencyKeyConflict reports a key reused with a different business payload.
	ErrIdempotencyKeyConflict = errors.New("app: idempotency key reused with a different payload")
	// ErrExternalTransactionConflict reports an external transaction already
	// registered under another idempotency key.
	ErrExternalTransactionConflict = errors.New("app: external transaction already registered under another idempotency key")
	// ErrInvalidCursor reports an undecodable ledger cursor.
	ErrInvalidCursor = errors.New("app: invalid ledger cursor")
	// ErrInvalidLimit reports a ledger page size outside 1..MaxLedgerLimit.
	ErrInvalidLimit = errors.New("app: invalid ledger page size")
```

`internal/app/app.go`:

```go
package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/events"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// Clock returns the current instant in UTC. It is injected so tests control time.
type Clock func() time.Time

// SystemClock is the production Clock.
func SystemClock() time.Time { return time.Now().UTC() }

// Channel identifies how an operation entered the service.
type Channel string

// Channels.
const (
	ChannelHTTP   Channel = "http"
	ChannelSQS    Channel = "sqs"
	ChannelWorker Channel = "worker"
)

// Meta carries the tracing identifiers of one request or message into
// events and logs. CausationID is the SQS messageId (empty over HTTP).
type Meta struct {
	CorrelationID string
	CausationID   string
	Channel       Channel
}

// normalized fills a missing correlation id and channel.
func (m Meta) normalized() Meta {
	if m.CorrelationID == "" {
		m.CorrelationID = newID().String()
	}
	if m.Channel == "" {
		m.Channel = ChannelHTTP
	}
	return m
}

// Viewer is who reads a transaction: a provider sees only its own
// operations; the internal service sees everything.
type Viewer struct {
	ProviderID string
	Internal   bool
}

func (v Viewer) canSee(t *wagering.WagerTransaction) bool {
	return v.Internal || (v.ProviderID != "" && t.ProviderID() == v.ProviderID)
}

// Deps are the ports and services shared by the use cases.
type Deps struct {
	Tx           TxManager
	Wallets      WalletRepository
	Transactions TransactionRepository
	Ledger       LedgerRepository
	Outbox       OutboxRepository
	Clock        Clock
	Metrics      Metrics
	Log          *slog.Logger
}

// newID returns a time-ordered UUIDv7. It only panics if the system random
// source fails, which is not a business condition.
func newID() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// run calls fn when it is set.
func run(ctx context.Context, fn func(ctx context.Context) error) error {
	if fn == nil {
		return nil
	}
	return fn(ctx)
}

// publish wraps recorded domain events in envelopes and writes them to the
// outbox inside the caller's transaction.
func publish(ctx context.Context, outbox OutboxRepository, now time.Time, data []events.Data, meta Meta) error {
	if len(data) == 0 {
		return nil
	}
	envs := make([]events.Envelope, 0, len(data))
	for _, d := range data {
		env, err := events.NewEnvelope(newID(), d, meta.CorrelationID, meta.CausationID, now)
		if err != nil {
			return err
		}
		envs = append(envs, env)
	}
	return outbox.Append(ctx, envs...)
}
```

`internal/app/metrics.go`:

```go
package app

import (
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// Concurrency conflict reasons reported to Metrics.
const (
	ConflictLockTimeout = "lock_timeout"
	ConflictVersion     = "version"
	ConflictUnique      = "unique"
	ConflictTransient   = "transient"
)

// Metrics records business and operational measurements. The HTTP plan
// implements it with Prometheus; NopMetrics discards everything.
type Metrics interface {
	TransactionCompleted(kind wagering.Kind, status wagering.Status, channel Channel)
	IdempotentReplay(channel Channel)
	ConcurrencyConflict(reason string)
	ProcessingDuration(channel Channel, d time.Duration)
	ReconciliationDivergence()
}

// NopMetrics is a Metrics that records nothing.
type NopMetrics struct{}

var _ Metrics = NopMetrics{}

// TransactionCompleted implements Metrics.
func (NopMetrics) TransactionCompleted(wagering.Kind, wagering.Status, Channel) {}

// IdempotentReplay implements Metrics.
func (NopMetrics) IdempotentReplay(Channel) {}

// ConcurrencyConflict implements Metrics.
func (NopMetrics) ConcurrencyConflict(string) {}

// ProcessingDuration implements Metrics.
func (NopMetrics) ProcessingDuration(Channel, time.Duration) {}

// ReconciliationDivergence implements Metrics.
func (NopMetrics) ReconciliationDivergence() {}
```

`internal/app/wallet_service.go`:

```go
package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

// OpenWalletCommand opens a wallet for a player in the currency of InitialBalance.
type OpenWalletCommand struct {
	PlayerID       uuid.UUID
	InitialBalance money.Money
}

// ParseOpenWallet validates raw input and returns *wagering.InputError with
// every violation. Zero is a valid initial balance.
func ParseOpenWallet(playerID, amount, currency string) (OpenWalletCommand, error) {
	var violations []wagering.Violation
	add := func(code wagering.FailureCode, field, reason string) {
		violations = append(violations, wagering.Violation{Code: code, Field: field, Reason: reason})
	}
	var cmd OpenWalletCommand
	id, err := uuid.Parse(playerID)
	switch {
	case playerID == "":
		add(wagering.FailureMissingField, "playerId", "is required")
	case err != nil || id.String() != playerID || id == uuid.Nil:
		add(wagering.FailureInvalidID, "playerId", "must be a non-nil lowercase canonical UUID")
	default:
		cmd.PlayerID = id
	}
	if amount == "" {
		add(wagering.FailureMissingField, "initialBalance.amount", "is required")
	}
	if currency == "" {
		add(wagering.FailureMissingField, "initialBalance.currency", "is required")
	}
	if amount != "" && currency != "" {
		m, err := money.Parse(amount, currency)
		switch {
		case err == nil:
			cmd.InitialBalance = m
		case errors.Is(err, money.ErrUnsupportedCurrency):
			add(wagering.FailureUnsupportedCurrency, "initialBalance.currency", "must be an ISO 4217 currency with 2 decimals")
		default:
			add(wagering.FailureInvalidMoney, "initialBalance.amount", err.Error())
		}
	}
	if len(violations) > 0 {
		return OpenWalletCommand{}, &wagering.InputError{Violations: violations}
	}
	return cmd, nil
}

// WalletService opens wallets.
type WalletService struct {
	d Deps
}

// NewWalletService builds a WalletService.
func NewWalletService(d Deps) *WalletService { return &WalletService{d: d} }

// Open creates the wallet at version 1. A positive initial balance also
// commits, in the same transaction, the PROCESSED OPENING transaction, its
// credit entry and the WagerTransactionProcessed and WalletBalanceChanged
// outbox events. A second wallet for (player, currency) is
// ErrWalletAlreadyExists.
func (s *WalletService) Open(ctx context.Context, cmd OpenWalletCommand, meta Meta) (*wallet.Wallet, error) {
	meta = meta.normalized()
	now := s.d.Clock()
	openingID := newID()
	w, entry, err := wallet.Open(wallet.OpenParams{
		ID: newID(), PlayerID: cmd.PlayerID, InitialBalance: cmd.InitialBalance,
		OpeningTransactionID: openingID, LedgerEntryID: newID(), Now: now,
	})
	if err != nil {
		return nil, err
	}
	err = s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		if err := s.d.Wallets.Create(ctx, w); err != nil {
			if errors.Is(err, ErrConflict) {
				return ErrWalletAlreadyExists
			}
			return err
		}
		if entry == nil {
			return nil
		}
		opening, err := wagering.NewOpening(openingID, w.ID(), w.PlayerID(), cmd.InitialBalance, now)
		if err != nil {
			return err
		}
		if err := opening.CompleteOpening(w, *entry, now); err != nil {
			return err
		}
		inserted, err := s.d.Transactions.Insert(ctx, opening)
		if err != nil {
			return err
		}
		if !inserted {
			return fmt.Errorf("app: OPENING of wallet %s already exists", w.ID())
		}
		if err := s.d.Ledger.Append(ctx, *entry); err != nil {
			return err
		}
		return publish(ctx, s.d.Outbox, now, opening.PullEvents(), meta)
	})
	if err != nil {
		return nil, err
	}
	s.d.Log.InfoContext(ctx, "wallet opened",
		"walletId", w.ID().String(), "correlationId", meta.CorrelationID)
	return w, nil
}
```

- [ ] **Step 4: Rodar o teste unitário**

Run: `go test -race -tags=unit ./internal/app/...`
Expected: `ok`.

- [ ] **Step 5: Criar o harness de teste**

`internal/testsupport/apptest/apptest.go`:

```go
// Package apptest wires the real PostgreSQL adapters to the application
// services for integration tests against postgres_test.
package apptest

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/postgres"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

// Harness holds real adapters bound to postgres_test plus recording metrics.
type Harness struct {
	DB           *gorm.DB
	Deps         app.Deps
	Tx           *postgres.TxManager
	Wallets      *postgres.WalletRepository
	Transactions *postgres.TransactionRepository
	Ledger       *postgres.LedgerRepository
	Outbox       *postgres.OutboxRepository
	Metrics      *RecordingMetrics
}

// Clock matches PostgreSQL's microsecond precision, so State round trips compare equal.
func Clock() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// New connects to postgres_test (failing fast with setup instructions) and
// builds the dependencies of the use cases.
func New(t testing.TB) *Harness {
	t.Helper()
	pgtest.AppDB(t)
	dbCfg := config.Database{URL: pgtest.AppURL(), MaxOpenConns: 40, LockTimeout: 5 * time.Second, StatementTimeout: 10 * time.Second}
	db, err := postgres.Open(dbCfg)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	h := &Harness{
		DB:           db,
		Tx:           postgres.NewTxManager(db, config.Config{Database: dbCfg}),
		Wallets:      postgres.NewWalletRepository(db),
		Transactions: postgres.NewTransactionRepository(db),
		Ledger:       postgres.NewLedgerRepository(db),
		Outbox:       postgres.NewOutboxRepository(db),
		Metrics:      NewRecordingMetrics(),
	}
	h.Deps = app.Deps{
		Tx: h.Tx, Wallets: h.Wallets, Transactions: h.Transactions, Ledger: h.Ledger, Outbox: h.Outbox,
		Clock: Clock, Metrics: h.Metrics, Log: slog.New(slog.DiscardHandler),
	}
	return h
}

// OpenWallet opens a BRL wallet with balance for a new player.
func (h *Harness) OpenWallet(t testing.TB, balance string) *wallet.Wallet {
	t.Helper()
	amount, err := money.Parse(balance, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	w, err := app.NewWalletService(h.Deps).Open(context.Background(),
		app.OpenWalletCommand{PlayerID: uuid.New(), InitialBalance: amount}, app.Meta{CorrelationID: "test-open"})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// OutboxRow is the decoded part of an outbox event the tests assert on.
type OutboxRow struct {
	EventType     string
	CorrelationID string
	CausationID   string
	Data          map[string]any
}

// OutboxRows returns the events stored for walletID in creation order.
func (h *Harness) OutboxRows(t testing.TB, walletID uuid.UUID) []OutboxRow {
	t.Helper()
	var raw []struct {
		EventType string
		Payload   []byte
	}
	err := h.DB.Raw(`SELECT event_type, payload FROM outbox_events WHERE aggregate_id = ? ORDER BY occurred_at, id`,
		walletID).Scan(&raw).Error
	if err != nil {
		t.Fatal(err)
	}
	rows := make([]OutboxRow, 0, len(raw))
	for _, r := range raw {
		var env struct {
			CorrelationID string         `json:"correlationId"`
			CausationID   string         `json:"causationId"`
			Data          map[string]any `json:"data"`
		}
		if err := json.Unmarshal(r.Payload, &env); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, OutboxRow{EventType: r.EventType, CorrelationID: env.CorrelationID, CausationID: env.CausationID, Data: env.Data})
	}
	return rows
}

// Command builds a valid external command for w with a fresh external id.
func Command(t testing.TB, w *wallet.Wallet, provider, kind, amount, ref string) wagering.Command {
	t.Helper()
	ext := uuid.NewString()
	cmd, err := wagering.ParseCommand(wagering.RawCommand{
		ProviderID: provider, ExternalTransactionID: ext, IdempotencyKey: provider + ":" + ext,
		PlayerID: w.PlayerID().String(), WalletID: w.ID().String(), RoundID: "round-1", GameID: "game-1",
		Kind: kind, Amount: amount, Currency: w.Currency().Code(), ReferenceExternalTransactionID: ref,
	})
	if err != nil {
		t.Fatal(err)
	}
	return cmd
}

// RecordingMetrics counts every Metrics call by a readable key.
type RecordingMetrics struct {
	mu     sync.Mutex
	counts map[string]int
}

var _ app.Metrics = (*RecordingMetrics)(nil)

// NewRecordingMetrics builds an empty RecordingMetrics.
func NewRecordingMetrics() *RecordingMetrics { return &RecordingMetrics{counts: map[string]int{}} }

// Count returns how many times key was recorded, e.g. "completed:BET:PROCESSED:http".
func (m *RecordingMetrics) Count(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counts[key]
}

func (m *RecordingMetrics) inc(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counts[key]++
}

// TransactionCompleted implements app.Metrics.
func (m *RecordingMetrics) TransactionCompleted(kind wagering.Kind, status wagering.Status, channel app.Channel) {
	m.inc("completed:" + string(kind) + ":" + string(status) + ":" + string(channel))
}

// IdempotentReplay implements app.Metrics.
func (m *RecordingMetrics) IdempotentReplay(channel app.Channel) { m.inc("replay:" + string(channel)) }

// ConcurrencyConflict implements app.Metrics.
func (m *RecordingMetrics) ConcurrencyConflict(reason string) { m.inc("conflict:" + reason) }

// ProcessingDuration implements app.Metrics.
func (m *RecordingMetrics) ProcessingDuration(channel app.Channel, _ time.Duration) {
	m.inc("duration:" + string(channel))
}

// ReconciliationDivergence implements app.Metrics.
func (m *RecordingMetrics) ReconciliationDivergence() { m.inc("divergence") }
```

- [ ] **Step 6: Escrever o teste de integração que falha e depois passa**

`internal/app/wallet_service_test.go`:

```go
//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

func eventTypes(rows []apptest.OutboxRow) []string {
	types := make([]string, len(rows))
	for i, r := range rows {
		types[i] = r.EventType
	}
	return types
}

func TestOpenWalletWithPositiveBalance(t *testing.T) {
	h := apptest.New(t)
	ctx := context.Background()
	cmd, err := app.ParseOpenWallet(uuid.NewString(), "1000.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	w, err := app.NewWalletService(h.Deps).Open(ctx, cmd, app.Meta{CorrelationID: "corr-open"})
	if err != nil {
		t.Fatal(err)
	}

	stored, err := h.Wallets.Get(ctx, w.ID())
	if err != nil || stored.Balance().Amount() != "1000.00" || stored.Version() != 1 {
		t.Fatalf("stored wallet %+v err %v", stored, err)
	}
	entries, err := h.Ledger.List(ctx, w.ID(), uuid.Nil, 10)
	if err != nil || len(entries) != 1 || entries[0].Direction() != wallet.DirectionCredit ||
		!entries[0].BalanceBefore().IsZero() || entries[0].BalanceAfter().Amount() != "1000.00" {
		t.Fatalf("ledger %+v err %v", entries, err)
	}
	opening, err := h.Transactions.Get(ctx, entries[0].TransactionID())
	if err != nil || opening.Kind() != wagering.KindOpening || opening.Status() != wagering.StatusProcessed {
		t.Fatalf("opening transaction %+v err %v", opening, err)
	}

	rows := h.OutboxRows(t, w.ID())
	if got := eventTypes(rows); !slices.Equal(got, []string{"WagerTransactionProcessed", "WalletBalanceChanged"}) {
		t.Fatalf("events = %v", got)
	}
	if rows[0].CorrelationID != "corr-open" || rows[0].Data["origin"] != "INTERNAL" || rows[0].Data["kind"] != "OPENING" {
		t.Fatalf("processed event %+v", rows[0])
	}
	if _, ok := rows[0].Data["providerId"]; ok {
		t.Fatal("OPENING event must not carry providerId")
	}
	if rows[1].Data["walletVersion"] != float64(1) {
		t.Fatalf("balance changed event walletVersion = %v, want 1", rows[1].Data["walletVersion"])
	}
}

func TestOpenWalletWithZeroBalance(t *testing.T) {
	h := apptest.New(t)
	ctx := context.Background()
	cmd, _ := app.ParseOpenWallet(uuid.NewString(), "0.00", "BRL")
	w, err := app.NewWalletService(h.Deps).Open(ctx, cmd, app.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	entries, err := h.Ledger.List(ctx, w.ID(), uuid.Nil, 10)
	if err != nil || len(entries) != 0 || len(h.OutboxRows(t, w.ID())) != 0 || w.Version() != 1 {
		t.Fatalf("zero opening must create no ledger and no events: entries %d err %v", len(entries), err)
	}
}

func TestOpenWalletDuplicate(t *testing.T) {
	h := apptest.New(t)
	ctx := context.Background()
	svc := app.NewWalletService(h.Deps)
	player := uuid.NewString()
	brl, _ := app.ParseOpenWallet(player, "10.00", "BRL")
	if _, err := svc.Open(ctx, brl, app.Meta{}); err != nil {
		t.Fatal(err)
	}
	again, _ := app.ParseOpenWallet(player, "99.00", "BRL")
	if _, err := svc.Open(ctx, again, app.Meta{}); !errors.Is(err, app.ErrWalletAlreadyExists) {
		t.Fatalf("duplicate error = %v, want ErrWalletAlreadyExists", err)
	}
	usd, _ := app.ParseOpenWallet(player, "5.00", "USD")
	if _, err := svc.Open(ctx, usd, app.Meta{}); err != nil {
		t.Fatalf("another currency is a different wallet: %v", err)
	}
	var wallets, openings int64
	if err := h.DB.Table("wallets").Where("player_id = ?", player).Count(&wallets).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.DB.Table("wager_transactions").Where("player_id = ? AND kind = 'OPENING'", player).Count(&openings).Error; err != nil {
		t.Fatal(err)
	}
	if wallets != 2 || openings != 2 {
		t.Fatalf("wallets %d openings %d, want 2 and 2 (duplicate rolled back)", wallets, openings)
	}
}
```

Run: `go test -race -tags=integration ./internal/app/...`
Expected: `ok`. Para confirmar o RED, rode antes de criar `wallet_service.go`: dá falha de compilação com
`undefined: app.NewWalletService`.

- [ ] **Step 7: Commit**

```bash
go vet ./... && gofmt -l .
git add internal/app internal/testsupport/apptest
git commit -m "feat(app): foundations, metrics port and wallet opening use case" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 3: WageringService — processamento idempotente (sem referências)

**Files:**
- Create: `internal/app/wagering_service.go`
- Test: `internal/app/wagering_service_test.go` (integration)

**Interfaces:**
- Consumes: `Deps`, `Meta`, `publish`, `run`, `newID` e os erros (Task 2); `wagering.NewExternal`, `Process`,
  `ProcessInput` e `RetryPolicy`; o `apptest` completo.
- Produces:
  - `app.TransactionResult{Transaction *wagering.WagerTransaction; Replay bool}`.
  - `app.NewWageringService(d Deps, policy wagering.RetryPolicy) (*WageringService, error)`, que valida a política.
  - `(*WageringService).Process(ctx, cmd wagering.Command, meta Meta, alongside func(ctx context.Context) error) (TransactionResult, error)`.
  - Unexported: `lookup`, `apply` e `failure`.
  - Tipo `alongsideError`, que envolve erros do gancho para que nunca virem FAILED.

- [ ] **Step 1: Escrever o teste que falha**

`internal/app/wagering_service_test.go`:

```go
//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

func newWagering(t *testing.T, h *apptest.Harness) *app.WageringService {
	t.Helper()
	svc, err := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return svc
}

func process(t *testing.T, svc *app.WageringService, cmd wagering.Command) app.TransactionResult {
	t.Helper()
	res, err := svc.Process(context.Background(), cmd, app.Meta{CorrelationID: "corr-" + cmd.ExternalTransactionID}, nil)
	if err != nil {
		t.Fatalf("Process(%s %s): %v", cmd.Kind, cmd.Money, err)
	}
	return res
}

func balance(t *testing.T, h *apptest.Harness, id uuid.UUID) string {
	t.Helper()
	w, err := h.Wallets.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return w.Balance().Amount()
}

func ledgerLen(t *testing.T, h *apptest.Harness, id uuid.UUID) int {
	t.Helper()
	entries, err := h.Ledger.List(context.Background(), id, uuid.Nil, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func TestProcessBet(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "25.00", "")

	res, err := svc.Process(context.Background(), cmd,
		app.Meta{CorrelationID: "corr-1", CausationID: "msg-1", Channel: app.ChannelSQS}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tx := res.Transaction
	if res.Replay || tx.Status() != wagering.StatusProcessed || tx.ResultBalance().Amount() != "75.00" {
		t.Fatalf("result replay %v status %s balance %s", res.Replay, tx.Status(), tx.ResultBalance())
	}
	stored, err := h.Wallets.Get(context.Background(), w.ID())
	if err != nil || stored.Balance().Amount() != "75.00" || stored.Version() != 2 {
		t.Fatalf("wallet %+v err %v", stored, err)
	}
	if ledgerLen(t, h, w.ID()) != 2 {
		t.Fatal("want opening credit + bet debit")
	}
	persisted, err := h.Transactions.Get(context.Background(), tx.ID())
	if err != nil || persisted.State() != tx.State() {
		t.Fatalf("persisted state differs: %v", err)
	}
	rows := h.OutboxRows(t, w.ID())
	if got := eventTypes(rows[2:]); !slices.Equal(got, []string{"WagerTransactionProcessed", "WalletBalanceChanged"}) {
		t.Fatalf("bet events = %v", got)
	}
	if rows[2].CorrelationID != "corr-1" || rows[2].CausationID != "msg-1" {
		t.Fatalf("event metadata %+v", rows[2])
	}
	if h.Metrics.Count("completed:BET:PROCESSED:sqs") != 1 || h.Metrics.Count("duration:sqs") != 1 {
		t.Fatal("metrics not recorded")
	}
}

func TestProcessLossDoesNotMoveTheWallet(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	res := process(t, svc, apptest.Command(t, w, "provider-a", "LOSS", "0.00", ""))
	stored, _ := h.Wallets.Get(context.Background(), w.ID())
	if res.Transaction.Status() != wagering.StatusProcessed || stored.Version() != 1 || ledgerLen(t, h, w.ID()) != 1 {
		t.Fatalf("LOSS moved the wallet: status %s version %d", res.Transaction.Status(), stored.Version())
	}
	if got := eventTypes(h.OutboxRows(t, w.ID())[2:]); !slices.Equal(got, []string{"WagerTransactionProcessed"}) {
		t.Fatalf("loss events = %v", got)
	}
}

func TestProcessRejections(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")

	res := process(t, svc, apptest.Command(t, w, "provider-a", "BET", "150.00", ""))
	if res.Transaction.Status() != wagering.StatusRejected || res.Transaction.FailureCode() != wagering.FailureInsufficientFunds {
		t.Fatalf("status %s code %s", res.Transaction.Status(), res.Transaction.FailureCode())
	}
	if balance(t, h, w.ID()) != "100.00" || ledgerLen(t, h, w.ID()) != 1 {
		t.Fatal("rejection moved the wallet")
	}
	if got := eventTypes(h.OutboxRows(t, w.ID())[2:]); !slices.Equal(got, []string{"WagerTransactionRejected"}) {
		t.Fatalf("rejection events = %v", got)
	}

	brl, _ := money.Parse("0.00", "BRL")
	ghost, _, err := wallet.Open(wallet.OpenParams{ID: uuid.New(), PlayerID: uuid.New(), InitialBalance: brl, Now: apptest.Clock()})
	if err != nil {
		t.Fatal(err)
	}
	missing := process(t, svc, apptest.Command(t, ghost, "provider-a", "BET", "1.00", ""))
	stored, err := h.Transactions.Get(context.Background(), missing.Transaction.ID())
	if err != nil || stored.Status() != wagering.StatusRejected || stored.FailureCode() != wagering.FailureWalletNotFound {
		t.Fatalf("wallet not found: %+v err %v", stored, err)
	}
}

func TestReplayReturnsTheOriginalResult(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	first := apptest.Command(t, w, "provider-a", "BET", "25.00", "")
	original := process(t, svc, first)
	process(t, svc, apptest.Command(t, w, "provider-a", "BET", "10.00", ""))

	replay := process(t, svc, first)
	if !replay.Replay || replay.Transaction.ID() != original.Transaction.ID() ||
		replay.Transaction.ResultBalance().Amount() != "75.00" {
		t.Fatalf("replay = %+v, want original transaction with balance 75.00", replay)
	}
	if balance(t, h, w.ID()) != "65.00" || ledgerLen(t, h, w.ID()) != 3 {
		t.Fatal("replay must not move the wallet")
	}
	if h.Metrics.Count("replay:http") != 1 {
		t.Fatal("replay metric not recorded")
	}
}

func TestIdempotencyConflicts(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "25.00", "")
	process(t, svc, cmd)

	changed := cmd
	changed.Money, _ = money.Parse("30.00", "BRL")
	if _, err := svc.Process(context.Background(), changed, app.Meta{}, nil); !errors.Is(err, app.ErrIdempotencyKeyConflict) {
		t.Fatalf("same key, other payload: err %v", err)
	}
	otherKey := cmd
	otherKey.IdempotencyKey = "provider-a:another-key"
	if _, err := svc.Process(context.Background(), otherKey, app.Meta{}, nil); !errors.Is(err, app.ErrExternalTransactionConflict) {
		t.Fatalf("same external id, other key: err %v", err)
	}
	otherProvider := cmd
	otherProvider.ProviderID = "provider-b"
	if res := process(t, svc, otherProvider); res.Replay || res.Transaction.ProviderID() != "provider-b" {
		t.Fatalf("keys are scoped by provider: %+v", res)
	}
	if balance(t, h, w.ID()) != "50.00" {
		t.Fatalf("balance %s, want 50.00 (two distinct bets)", balance(t, h, w.ID()))
	}
}

func TestAlongsideSharesTheTransaction(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	ctx := context.Background()

	calls := 0
	ok := func(context.Context) error { calls++; return nil }
	cmd := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
	if _, err := svc.Process(ctx, cmd, app.Meta{}, ok); err != nil || calls != 1 {
		t.Fatalf("hook on first processing: calls %d err %v", calls, err)
	}
	if res, err := svc.Process(ctx, cmd, app.Meta{}, ok); err != nil || !res.Replay || calls != 2 {
		t.Fatalf("hook on replay: calls %d err %v", calls, err)
	}

	boom := errors.New("inbox write failed")
	failing := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
	_, err := svc.Process(ctx, failing, app.Meta{}, func(context.Context) error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("hook error must be returned: %v", err)
	}
	if _, err := h.Transactions.FindByIdempotencyKey(ctx, "provider-a", failing.IdempotencyKey); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("hook failure must roll back the transaction row (and must not record FAILED): %v", err)
	}
	if balance(t, h, w.ID()) != "90.00" {
		t.Fatal("hook failure must roll back the debit")
	}
}

func TestNewWageringServiceValidatesPolicy(t *testing.T) {
	h := apptest.New(t)
	if _, err := app.NewWageringService(h.Deps, wagering.RetryPolicy{}); err == nil {
		t.Fatal("invalid retry policy must be rejected at construction")
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/app/...`
Expected: FAIL de compilação, `undefined: app.NewWageringService`.

- [ ] **Step 3: Implementar**

`internal/app/wagering_service.go`:

```go
package app

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// TransactionResult is the outcome of Process: the transaction (new or
// persisted) and whether it is an idempotent replay.
type TransactionResult struct {
	Transaction *wagering.WagerTransaction
	Replay      bool
}

// WageringService processes external operations (BET, WIN, LOSS, REFUND,
// ROLLBACK) for HTTP and SQS with the same idempotency guarantees.
type WageringService struct {
	d      Deps
	policy wagering.RetryPolicy
}

// NewWageringService validates the reference retry policy and builds the service.
func NewWageringService(d Deps, policy wagering.RetryPolicy) (*WageringService, error) {
	if err := policy.Validate(); err != nil {
		return nil, err
	}
	return &WageringService{d: d, policy: policy}, nil
}

// alongsideError marks an error returned by the caller's alongside hook: it
// rolls the transaction back but never becomes a FAILED transaction.
type alongsideError struct{ err error }

func (e *alongsideError) Error() string { return e.err.Error() }
func (e *alongsideError) Unwrap() error { return e.err }

// Process applies cmd exactly once per (provider, idempotency key):
//
//   - a key already used with the same payload returns the persisted result
//     (Replay=true); with another payload, ErrIdempotencyKeyConflict;
//   - an external id already registered under another key is
//     ErrExternalTransactionConflict;
//   - otherwise the operation is inserted PENDING, the wallet is row-locked,
//     the domain decides and everything (transaction, balance, ledger, outbox
//     and the caller's alongside work) commits in one SQL transaction.
//
// Transient failures return an error matching ErrTransient and leave no
// trace. alongside (may be nil) runs inside the transaction that commits the
// outcome, including replays. Process must not be called inside a transaction.
func (s *WageringService) Process(ctx context.Context, cmd wagering.Command, meta Meta, alongside func(ctx context.Context) error) (TransactionResult, error) {
	meta = meta.normalized()
	start := time.Now()
	defer func() { s.d.Metrics.ProcessingDuration(meta.Channel, time.Since(start)) }()

	if res, found, err := s.lookup(ctx, cmd, meta, alongside); found || err != nil {
		return res, err
	}
	t, err := wagering.NewExternal(newID(), cmd, s.d.Clock())
	if err != nil {
		return TransactionResult{}, err
	}
	raced := false
	err = s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		inserted, err := s.d.Transactions.Insert(ctx, t)
		if err != nil {
			return err
		}
		if !inserted {
			raced = true
			return nil
		}
		if err := s.apply(ctx, t, meta); err != nil {
			return err
		}
		if err := run(ctx, alongside); err != nil {
			return &alongsideError{err: err}
		}
		return nil
	})
	switch {
	case err != nil:
		return s.failure(ctx, cmd, meta, alongside, err)
	case raced:
		// A concurrent request with the same key or external id committed
		// first; answer from its persisted outcome.
		res, found, err := s.lookup(ctx, cmd, meta, alongside)
		if err == nil && !found {
			return TransactionResult{}, fmt.Errorf("%w: concurrent insert of %q not visible", ErrTransient, cmd.IdempotencyKey)
		}
		return res, err
	}
	s.completed(ctx, t, meta)
	return TransactionResult{Transaction: t}, nil
}

// lookup answers from persisted state: replay, key conflict or external-id
// conflict. found=false means the operation is new.
func (s *WageringService) lookup(ctx context.Context, cmd wagering.Command, meta Meta, alongside func(ctx context.Context) error) (TransactionResult, bool, error) {
	existing, err := s.d.Transactions.FindByIdempotencyKey(ctx, cmd.ProviderID, cmd.IdempotencyKey)
	switch {
	case err == nil:
		if existing.PayloadHash() != cmd.PayloadHash() {
			return TransactionResult{}, true, ErrIdempotencyKeyConflict
		}
		if alongside != nil {
			if err := s.d.Tx.WithinTx(ctx, alongside); err != nil {
				return TransactionResult{}, true, err
			}
		}
		s.d.Metrics.IdempotentReplay(meta.Channel)
		return TransactionResult{Transaction: existing, Replay: true}, true, nil
	case !errors.Is(err, ErrNotFound):
		return TransactionResult{}, false, err
	}
	_, err = s.d.Transactions.FindByExternalID(ctx, cmd.ProviderID, cmd.ExternalTransactionID)
	switch {
	case err == nil:
		return TransactionResult{}, true, ErrExternalTransactionConflict
	case errors.Is(err, ErrNotFound):
		return TransactionResult{}, false, nil
	default:
		return TransactionResult{}, false, err
	}
}

// apply locks the wallet, lets the domain decide and persists the outcome in
// the caller's transaction. Lock order is always wallet → transactions.
func (s *WageringService) apply(ctx context.Context, t *wagering.WagerTransaction, meta Meta) error {
	w, err := s.d.Wallets.GetForUpdate(ctx, t.WalletID())
	switch {
	case errors.Is(err, ErrNotFound):
		w = nil
	case err != nil:
		return err
	}
	var before int64
	if w != nil {
		before = w.Version()
	}
	entry, err := t.Process(wagering.ProcessInput{
		Wallet: w, LedgerEntryID: newID(), RetryPolicy: s.policy, Now: s.d.Clock(),
	})
	if err != nil {
		return err
	}
	if entry != nil {
		if err := s.d.Wallets.Update(ctx, w, before); err != nil {
			return err
		}
		if err := s.d.Ledger.Append(ctx, *entry); err != nil {
			return err
		}
	}
	if err := s.d.Transactions.Update(ctx, t); err != nil {
		return err
	}
	return publish(ctx, s.d.Outbox, s.d.Clock(), t.PullEvents(), meta)
}

// failure classifies an error of the processing transaction, which has
// already rolled back. Transient errors are returned for retry; nothing is
// recorded. (Task 5 records permanent failures as FAILED.)
func (s *WageringService) failure(ctx context.Context, cmd wagering.Command, meta Meta, alongside func(ctx context.Context) error, err error) (TransactionResult, error) {
	var hookErr *alongsideError
	switch {
	case errors.As(err, &hookErr):
		return TransactionResult{}, err
	case errors.Is(err, ErrLockTimeout):
		s.d.Metrics.ConcurrencyConflict(ConflictLockTimeout)
		return TransactionResult{}, err
	case errors.Is(err, ErrVersionConflict):
		s.d.Metrics.ConcurrencyConflict(ConflictVersion)
		return TransactionResult{}, err
	case errors.Is(err, ErrTransient):
		s.d.Metrics.ConcurrencyConflict(ConflictTransient)
		return TransactionResult{}, err
	case errors.Is(err, ErrConflict):
		// A unique index refused a write inside processing (a race the wallet
		// lock should prevent); retrying re-reads the winner's state.
		s.d.Metrics.ConcurrencyConflict(ConflictUnique)
		return TransactionResult{}, fmt.Errorf("%w: %w", ErrTransient, err)
	}
	return TransactionResult{}, err
}

func (s *WageringService) completed(ctx context.Context, t *wagering.WagerTransaction, meta Meta) {
	s.d.Metrics.TransactionCompleted(t.Kind(), t.Status(), meta.Channel)
	s.d.Log.InfoContext(ctx, "wager transaction completed",
		"transactionId", t.ID().String(), "walletId", t.WalletID().String(), "providerId", t.ProviderID(),
		"kind", string(t.Kind()), "status", string(t.Status()), "failureCode", string(t.FailureCode()),
		"correlationId", meta.CorrelationID, "messageId", meta.CausationID)
}
```

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=integration ./internal/app/... && go test -race -tags=unit ./...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
go vet ./... && gofmt -l .
git add internal/app
git commit -m "feat(app): idempotent wager processing with replay, conflicts and alongside hook" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 4: Referências — resolução, reversões e PENDING_REFERENCE

**Files:**
- Modify: `internal/app/wagering_service.go`
- Test: `internal/app/wagering_references_test.go` (integration)

**Interfaces:**
- Consumes: `apply` (Task 3); `TransactionRepository.FindByExternalID` e `HasProcessedReversal`;
  `(*WagerTransaction).AsReference`, `ReferenceExternalTransactionID`, `ProviderID` e `Kind().IsReversal()`.
- Produces: `(*WageringService).resolveReference(ctx, t, in *wagering.ProcessInput) error`, chamado por `apply`
  antes de `t.Process`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/app/wagering_references_test.go`:

```go
//go:build !unit && !e2e

package app_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

func assertStatus(t *testing.T, res wagering.Status, code wagering.FailureCode, tx *wagering.WagerTransaction) {
	t.Helper()
	if tx.Status() != res || tx.FailureCode() != code {
		t.Fatalf("status %s code %q, want %s %q", tx.Status(), tx.FailureCode(), res, code)
	}
}

func TestRefundAndRollbackResolveTheirReference(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")

	betCmd := apptest.Command(t, w, "provider-a", "BET", "30.00", "")
	bet := process(t, svc, betCmd).Transaction
	refund := process(t, svc, apptest.Command(t, w, "provider-a", "REFUND", "30.00", betCmd.ExternalTransactionID)).Transaction
	assertStatus(t, wagering.StatusProcessed, "", refund)
	if refund.ReferenceTransactionID() != bet.ID() || balance(t, h, w.ID()) != "100.00" {
		t.Fatalf("refund reference %s balance %s", refund.ReferenceTransactionID(), balance(t, h, w.ID()))
	}

	rollbackOfBet := process(t, svc, apptest.Command(t, w, "provider-a", "ROLLBACK", "30.00", betCmd.ExternalTransactionID)).Transaction
	assertStatus(t, wagering.StatusRejected, wagering.FailureReferenceAlreadyReversed, rollbackOfBet)
	secondRefund := process(t, svc, apptest.Command(t, w, "provider-a", "REFUND", "30.00", betCmd.ExternalTransactionID)).Transaction
	assertStatus(t, wagering.StatusRejected, wagering.FailureReferenceAlreadyReversed, secondRefund)

	refundExt := refund.ExternalTransactionID()
	rollbackOfRefund := process(t, svc, apptest.Command(t, w, "provider-a", "ROLLBACK", "30.00", refundExt)).Transaction
	assertStatus(t, wagering.StatusProcessed, "", rollbackOfRefund)
	if balance(t, h, w.ID()) != "70.00" {
		t.Fatalf("after rolling back the refund balance %s, want 70.00", balance(t, h, w.ID()))
	}
}

func TestWinWithReferenceAndReversalWithoutFunds(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "10.00")

	betCmd := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
	process(t, svc, betCmd)
	winCmd := apptest.Command(t, w, "provider-a", "WIN", "50.00", betCmd.ExternalTransactionID)
	win := process(t, svc, winCmd).Transaction
	assertStatus(t, wagering.StatusProcessed, "", win)
	if win.ReferenceTransactionID() == uuid.Nil {
		t.Fatal("WIN must persist the resolved BET")
	}
	process(t, svc, apptest.Command(t, w, "provider-a", "BET", "45.00", ""))
	rollback := process(t, svc, apptest.Command(t, w, "provider-a", "ROLLBACK", "50.00", winCmd.ExternalTransactionID)).Transaction
	assertStatus(t, wagering.StatusRejected, wagering.FailureInsufficientFundsForReversal, rollback)
	if balance(t, h, w.ID()) != "5.00" {
		t.Fatalf("balance %s, want 5.00", balance(t, h, w.ID()))
	}
}

func TestReversalBeforeItsReferenceWaits(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	before := time.Now()

	refund := process(t, svc, apptest.Command(t, w, "provider-a", "REFUND", "30.00", "bet-not-yet")).Transaction
	assertStatus(t, wagering.StatusPendingReference, "", refund)
	stored, err := h.Transactions.Get(context.Background(), refund.ID())
	if err != nil || stored.Status() != wagering.StatusPendingReference || stored.Attempts() != 1 ||
		stored.NextAttemptAt().Before(before.Add(time.Second)) {
		t.Fatalf("persisted pending %+v err %v", stored, err)
	}
	if got := eventTypes(h.OutboxRows(t, w.ID())[2:]); !slices.Equal(got, []string{"WagerTransactionPendingReference"}) {
		t.Fatalf("pending events = %v", got)
	}
	if balance(t, h, w.ID()) != "100.00" {
		t.Fatal("a pending reversal must not move the wallet")
	}
}

func TestReferencesAreScopedAndMustMatch(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	other := h.OpenWallet(t, "100.00")

	betCmd := apptest.Command(t, w, "provider-a", "BET", "20.00", "")
	process(t, svc, betCmd)

	// provider-b cannot see provider-a's bet: the reference is "not found yet".
	foreign := process(t, svc, apptest.Command(t, w, "provider-b", "REFUND", "20.00", betCmd.ExternalTransactionID)).Transaction
	assertStatus(t, wagering.StatusPendingReference, "", foreign)

	// Same provider, other wallet: the reference does not match.
	mismatch := process(t, svc, apptest.Command(t, other, "provider-a", "REFUND", "20.00", betCmd.ExternalTransactionID)).Transaction
	assertStatus(t, wagering.StatusRejected, wagering.FailureReferenceMismatch, mismatch)
	if balance(t, h, other.ID()) != "100.00" || balance(t, h, w.ID()) != "80.00" {
		t.Fatal("rejected reversals must not move wallets")
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/app/ -run 'Refund|Reversal|Reference|Win'`
Expected: FAIL. O REFUND fica `PENDING_REFERENCE` em vez de `PROCESSED`, porque a referência ainda não é resolvida.

- [ ] **Step 3: Implementar**

Em `apply`, substituir a construção de `ProcessInput` e a chamada a `t.Process` por:

```go
	in := wagering.ProcessInput{Wallet: w, LedgerEntryID: newID(), RetryPolicy: s.policy, Now: s.d.Clock()}
	if err := s.resolveReference(ctx, t, &in); err != nil {
		return err
	}
	entry, err := t.Process(in)
	if err != nil {
		return err
	}
```

Acrescentar:

```go
// resolveReference loads the referenced operation of the same provider and,
// for reversals, whether it was already reversed. The wallet row is already
// locked, so a reference on the same wallet cannot change underneath; a
// reference on another wallet is rejected by the domain (REFERENCE_MISMATCH).
func (s *WageringService) resolveReference(ctx context.Context, t *wagering.WagerTransaction, in *wagering.ProcessInput) error {
	refExt := t.ReferenceExternalTransactionID()
	if refExt == "" {
		return nil
	}
	ref, err := s.d.Transactions.FindByExternalID(ctx, t.ProviderID(), refExt)
	switch {
	case errors.Is(err, ErrNotFound):
		return nil
	case err != nil:
		return err
	}
	r := ref.AsReference()
	in.Reference = &r
	if t.Kind().IsReversal() {
		reversed, err := s.d.Transactions.HasProcessedReversal(ctx, r.ID, t.Kind(), r.Kind)
		if err != nil {
			return err
		}
		in.ReferenceAlreadyReversed = reversed
	}
	return nil
}
```

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=integration ./internal/app/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
go vet ./... && gofmt -l .
git add internal/app
git commit -m "feat(app): resolve references and reversals within the wallet lock" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 5: Falhas permanentes (FAILED) e transitórias

**Files:**
- Modify: `internal/app/wagering_service.go`
- Test: `internal/app/wagering_failures_test.go` (integration)

**Interfaces:**
- Consumes: `failure`, `lookup`, `completed` e `alongsideError` (Task 3); `(*WagerTransaction).MarkFailed`.
- Produces: `(*WageringService).recordFailure(ctx, cmd, meta, alongside, cause error) (TransactionResult, error)`.
  O caso padrão de `failure` passa a chamá-lo.

- [ ] **Step 1: Escrever o teste que falha**

`internal/app/wagering_failures_test.go`:

```go
//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

// faultyTransactions fails the next Update with err, once.
type faultyTransactions struct {
	app.TransactionRepository
	mu  sync.Mutex
	err error
}

func (f *faultyTransactions) failNextUpdate(err error) { f.mu.Lock(); f.err = err; f.mu.Unlock() }

func (f *faultyTransactions) Update(ctx context.Context, t *wagering.WagerTransaction) error {
	f.mu.Lock()
	err := f.err
	f.err = nil
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return f.TransactionRepository.Update(ctx, t)
}

// faultyWallets fails the next Update with err, once.
type faultyWallets struct {
	app.WalletRepository
	mu  sync.Mutex
	err error
}

func (f *faultyWallets) Update(ctx context.Context, w *wallet.Wallet, expected int64) error {
	f.mu.Lock()
	err := f.err
	f.err = nil
	f.mu.Unlock()
	if err != nil {
		return err
	}
	return f.WalletRepository.Update(ctx, w, expected)
}

func faultyService(t *testing.T, h *apptest.Harness) (*app.WageringService, *faultyTransactions, *faultyWallets) {
	t.Helper()
	txs := &faultyTransactions{TransactionRepository: h.Transactions}
	wallets := &faultyWallets{WalletRepository: h.Wallets}
	d := h.Deps
	d.Transactions, d.Wallets = txs, wallets
	svc, err := app.NewWageringService(d, wagering.DefaultRetryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	return svc, txs, wallets
}

func TestPermanentFailureIsRecordedAsFailed(t *testing.T) {
	h := apptest.New(t)
	svc, txs, _ := faultyService(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "25.00", "")

	txs.failNextUpdate(errors.New("disk on fire"))
	res, err := svc.Process(context.Background(), cmd, app.Meta{}, nil)
	if err != nil {
		t.Fatalf("a permanent failure is a recorded outcome, not an error: %v", err)
	}
	assertStatus(t, wagering.StatusFailed, wagering.FailureInfrastructure, res.Transaction)
	stored, err := h.Transactions.Get(context.Background(), res.Transaction.ID())
	if err != nil || stored.Status() != wagering.StatusFailed {
		t.Fatalf("FAILED must be persisted for audit: %+v %v", stored, err)
	}
	if balance(t, h, w.ID()) != "100.00" || ledgerLen(t, h, w.ID()) != 1 || len(h.OutboxRows(t, w.ID())) != 2 {
		t.Fatal("the failed attempt must leave no balance, ledger or event effects")
	}
	replay, err := svc.Process(context.Background(), cmd, app.Meta{}, nil)
	if err != nil || !replay.Replay || replay.Transaction.Status() != wagering.StatusFailed {
		t.Fatalf("replay of a FAILED operation = %+v, %v", replay, err)
	}
	if h.Metrics.Count("completed:BET:FAILED:http") != 1 {
		t.Fatal("FAILED outcome not counted")
	}
}

func TestTransientFailuresLeaveNoTrace(t *testing.T) {
	h := apptest.New(t)
	svc, txs, wallets := faultyService(t, h)
	w := h.OpenWallet(t, "100.00")
	ctx := context.Background()

	cases := []struct {
		name   string
		inject func()
		metric string
	}{
		{"transient", func() { txs.failNextUpdate(fmt.Errorf("db: %w", app.ErrTransient)) }, "conflict:transient"},
		{"lock timeout", func() { txs.failNextUpdate(fmt.Errorf("db: %w", app.ErrLockTimeout)) }, "conflict:lock_timeout"},
		{"version", func() { wallets.mu.Lock(); wallets.err = app.ErrVersionConflict; wallets.mu.Unlock() }, "conflict:version"},
		{"unique", func() { txs.failNextUpdate(fmt.Errorf("db: %w", app.ErrConflict)) }, "conflict:unique"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd := apptest.Command(t, w, "provider-a", "BET", "1.00", "")
			tc.inject()
			_, err := svc.Process(ctx, cmd, app.Meta{}, nil)
			if !errors.Is(err, app.ErrTransient) {
				t.Fatalf("error = %v, want ErrTransient", err)
			}
			if _, err := h.Transactions.FindByIdempotencyKey(ctx, "provider-a", cmd.IdempotencyKey); !errors.Is(err, app.ErrNotFound) {
				t.Fatalf("a transient failure must leave no row (no FAILED): %v", err)
			}
			if h.Metrics.Count(tc.metric) == 0 {
				t.Fatalf("metric %s not recorded", tc.metric)
			}
			res, err := svc.Process(ctx, cmd, app.Meta{}, nil)
			if err != nil || res.Replay || res.Transaction.Status() != wagering.StatusProcessed {
				t.Fatalf("retry after a transient failure = %+v, %v", res, err)
			}
		})
	}
	if balance(t, h, w.ID()) != "96.00" {
		t.Fatalf("balance %s, want 96.00 (each bet applied once)", balance(t, h, w.ID()))
	}
}

func TestCanceledRequestLeavesNoTrace(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "10.00", "")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.Process(ctx, cmd, app.Meta{}, nil); !errors.Is(err, app.ErrTransient) {
		t.Fatalf("canceled request error = %v, want ErrTransient", err)
	}
	if _, err := h.Transactions.FindByIdempotencyKey(context.Background(), "provider-a", cmd.IdempotencyKey); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("a canceled request must not be recorded as FAILED: %v", err)
	}
	if balance(t, h, w.ID()) != "100.00" {
		t.Fatal("a canceled request must not move the wallet")
	}
}

func TestAlongsideRunsInTheFailedTransaction(t *testing.T) {
	h := apptest.New(t)
	svc, txs, _ := faultyService(t, h)
	w := h.OpenWallet(t, "100.00")
	calls := 0
	txs.failNextUpdate(errors.New("permanent"))
	res, err := svc.Process(context.Background(), apptest.Command(t, w, "provider-a", "BET", "1.00", ""), app.Meta{},
		func(context.Context) error { calls++; return nil })
	if err != nil || res.Transaction.Status() != wagering.StatusFailed || calls != 1 {
		t.Fatalf("status %v calls %d err %v; the hook must run once, with the FAILED record", res.Transaction, calls, err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/app/ -run 'Failure|Failed|Transient|Canceled'`
Expected: `TestPermanentFailureIsRecordedAsFailed` e `TestAlongsideRunsInTheFailedTransaction` FAIL. O erro permanente
ainda é devolvido em vez de gravado como FAILED. Os testes de erro transitório e de cancelamento já passam.

- [ ] **Step 3: Implementar**

Em `failure`, substituir a última linha (`return TransactionResult{}, err`) por:

```go
	return s.recordFailure(ctx, cmd, meta, alongside, err)
```

Acrescentar:

```go
// recordFailure stores the operation as FAILED (INFRASTRUCTURE_FAILURE) in a
// new transaction, for audit, after the processing transaction rolled back.
// The caller's alongside work commits with the FAILED record. If even this
// write fails, both errors are returned (transient if either is).
func (s *WageringService) recordFailure(ctx context.Context, cmd wagering.Command, meta Meta, alongside func(ctx context.Context) error, cause error) (TransactionResult, error) {
	s.d.Log.ErrorContext(ctx, "permanent failure processing wager transaction",
		"providerId", cmd.ProviderID, "walletId", cmd.WalletID.String(), "correlationId", meta.CorrelationID,
		"messageId", meta.CausationID, "error", cause.Error())
	t, err := wagering.NewExternal(newID(), cmd, s.d.Clock())
	if err != nil {
		return TransactionResult{}, errors.Join(cause, err)
	}
	if err := t.MarkFailed(s.d.Clock()); err != nil {
		return TransactionResult{}, errors.Join(cause, err)
	}
	raced := false
	err = s.d.Tx.WithinTx(ctx, func(ctx context.Context) error {
		inserted, err := s.d.Transactions.Insert(ctx, t)
		if err != nil {
			return err
		}
		if !inserted {
			raced = true
			return nil
		}
		if err := run(ctx, alongside); err != nil {
			return &alongsideError{err: err}
		}
		return nil
	})
	if err != nil {
		return TransactionResult{}, errors.Join(cause, err)
	}
	if raced {
		res, found, err := s.lookup(ctx, cmd, meta, alongside)
		if err == nil && !found {
			return TransactionResult{}, fmt.Errorf("%w: concurrent insert of %q not visible", ErrTransient, cmd.IdempotencyKey)
		}
		return res, err
	}
	s.completed(ctx, t, meta)
	return TransactionResult{Transaction: t}, nil
}
```

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=integration ./internal/app/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
go vet ./... && gofmt -l .
git add internal/app
git commit -m "feat(app): record permanent failures as FAILED, never transient ones" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 6: Concorrência no nível da aplicação (seção 13)

**Files:**
- Test: `internal/app/concurrency_test.go` (integration)

**Interfaces:**
- Consumes: `newWagering`, `balance`, `ledgerLen` (Task 3), `assertStatus` (Task 4) e o `apptest`.
- Produces: só testes. Nenhuma mudança de código é esperada. Se algum teste falhar, é bug real: reporte com evidência
  em vez de afrouxar o teste.

- [ ] **Step 1: Escrever os testes**

`internal/app/concurrency_test.go`:

```go
//go:build !unit && !e2e

package app_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

type outcome struct {
	res app.TransactionResult
	err error
}

func processConcurrently(svc *app.WageringService, cmds []wagering.Command) []outcome {
	out := make([]outcome, len(cmds))
	var wg sync.WaitGroup
	for i, cmd := range cmds {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.Process(context.Background(), cmd, app.Meta{}, nil)
			out[i] = outcome{res: res, err: err}
		}()
	}
	wg.Wait()
	return out
}

func assertLedgerMatchesBalance(t *testing.T, h *apptest.Harness, walletID uuid.UUID) {
	t.Helper()
	w, err := h.Wallets.Get(context.Background(), walletID)
	if err != nil {
		t.Fatal(err)
	}
	net, _, err := h.Ledger.Totals(context.Background(), walletID)
	if err != nil || net != w.Balance().MinorUnits() {
		t.Fatalf("ledger net %d, stored balance %d (err %v)", net, w.Balance().MinorUnits(), err)
	}
}

func TestSameBetFiftyTimesInParallel(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
	cmds := make([]wagering.Command, 50)
	for i := range cmds {
		cmds[i] = cmd
	}

	originals, replays := 0, 0
	var id uuid.UUID
	for _, o := range processConcurrently(svc, cmds) {
		if o.err != nil {
			t.Fatalf("unexpected error: %v", o.err)
		}
		if o.res.Replay {
			replays++
		} else {
			originals++
		}
		if id == uuid.Nil {
			id = o.res.Transaction.ID()
		} else if o.res.Transaction.ID() != id {
			t.Fatal("every answer must be the same transaction")
		}
	}
	if originals != 1 || replays != 49 {
		t.Fatalf("originals %d replays %d, want 1 and 49", originals, replays)
	}
	if balance(t, h, w.ID()) != "90.00" || ledgerLen(t, h, w.ID()) != 2 {
		t.Fatalf("balance %s ledger %d, want one debit", balance(t, h, w.ID()), ledgerLen(t, h, w.ID()))
	}
	assertLedgerMatchesBalance(t, h, w.ID())
}

func TestTwoBetsOfEightyOnAHundred(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	cmds := []wagering.Command{
		apptest.Command(t, w, "provider-a", "BET", "80.00", ""),
		apptest.Command(t, w, "provider-a", "BET", "80.00", ""),
	}

	statuses := map[wagering.Status]int{}
	for _, o := range processConcurrently(svc, cmds) {
		if o.err != nil {
			t.Fatal(o.err)
		}
		statuses[o.res.Transaction.Status()]++
		if o.res.Transaction.Status() == wagering.StatusRejected && o.res.Transaction.FailureCode() != wagering.FailureInsufficientFunds {
			t.Fatalf("rejection code %s", o.res.Transaction.FailureCode())
		}
	}
	if statuses[wagering.StatusProcessed] != 1 || statuses[wagering.StatusRejected] != 1 {
		t.Fatalf("statuses %v, want one PROCESSED and one REJECTED", statuses)
	}
	if balance(t, h, w.ID()) != "20.00" || ledgerLen(t, h, w.ID()) != 2 {
		t.Fatalf("balance %s ledger %d", balance(t, h, w.ID()), ledgerLen(t, h, w.ID()))
	}

	for _, o := range processConcurrently(svc, cmds) {
		if o.err != nil || !o.res.Replay {
			t.Fatalf("resend must replay: %+v %v", o.res, o.err)
		}
	}
	if balance(t, h, w.ID()) != "20.00" || ledgerLen(t, h, w.ID()) != 2 {
		t.Fatal("resends changed the outcome")
	}
	assertLedgerMatchesBalance(t, h, w.ID())
}

func TestDistinctWalletsProceedWhileOneIsLocked(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	locked, free := h.OpenWallet(t, "100.00"), h.OpenWallet(t, "100.00")

	holding, release := make(chan struct{}), make(chan struct{})
	held := make(chan error, 1)
	go func() {
		held <- h.Tx.WithinTx(context.Background(), func(ctx context.Context) error {
			if _, err := h.Wallets.GetForUpdate(ctx, locked.ID()); err != nil {
				return err
			}
			close(holding)
			<-release
			return nil
		})
	}()
	select {
	case <-holding:
	case err := <-held:
		t.Fatalf("holder failed: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("holder never locked the wallet")
	}

	start := time.Now()
	res := process(t, svc, apptest.Command(t, free, "provider-a", "BET", "5.00", ""))
	if res.Transaction.Status() != wagering.StatusProcessed || time.Since(start) > 2*time.Second {
		t.Fatalf("a different wallet must not wait: %s after %s", res.Transaction.Status(), time.Since(start))
	}

	lockedCmd := apptest.Command(t, locked, "provider-a", "BET", "5.00", "")
	blocked := make(chan outcome, 1)
	go func() {
		r, err := svc.Process(context.Background(), lockedCmd, app.Meta{}, nil)
		blocked <- outcome{res: r, err: err}
	}()
	select {
	case o := <-blocked:
		t.Fatalf("the locked wallet must wait, got %+v", o)
	case <-time.After(300 * time.Millisecond):
	}
	close(release)
	select {
	case o := <-blocked:
		if o.err != nil || o.res.Transaction.Status() != wagering.StatusProcessed {
			t.Fatalf("after release: %+v %v", o.res, o.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the locked wallet never proceeded")
	}
	if err := <-held; err != nil {
		t.Fatal(err)
	}
}

func TestManyWalletsInParallel(t *testing.T) {
	h := apptest.New(t)
	svc := newWagering(t, h)
	var cmds []wagering.Command
	var ids []uuid.UUID
	for range 10 {
		w := h.OpenWallet(t, "100.00")
		ids = append(ids, w.ID())
		for range 5 {
			cmds = append(cmds, apptest.Command(t, w, "provider-a", "BET", "1.00", ""))
		}
	}
	for _, o := range processConcurrently(svc, cmds) {
		if o.err != nil || o.res.Transaction.Status() != wagering.StatusProcessed {
			t.Fatalf("%+v %v", o.res, o.err)
		}
	}
	for _, id := range ids {
		if balance(t, h, id) != "95.00" || ledgerLen(t, h, id) != 6 {
			t.Fatalf("wallet %s balance %s", id, balance(t, h, id))
		}
		assertLedgerMatchesBalance(t, h, id)
	}
}
```

- [ ] **Step 2: Rodar**

Run: `go test -race -count=1 -tags=integration ./internal/app/ -run 'Parallel|Eighty|Locked'`
Expected: `ok`. Se houver falha, investigue e reporte como BLOCKED com a saída, sem afrouxar asserções.

- [ ] **Step 3: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/app
git commit -m "test(app): concurrency guarantees for duplicates, contention and parallel wallets" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 7: QueryService — consultas com escopo e paginação do ledger

**Files:**
- Create: `internal/app/query_service.go`
- Test: `internal/app/query_service_unit_test.go` (unit), `internal/app/query_service_test.go` (integration)

**Interfaces:**
- Consumes: `Deps`, `Viewer.canSee`, `ErrInvalidCursor`, `ErrInvalidLimit` e `LedgerRepository.List`.
- Produces:
  - `app.NewQueryService(d Deps) *QueryService`.
  - Leitura de carteira e transações:
    - `(*QueryService).Wallet(ctx, id uuid.UUID) (*wallet.Wallet, error)`
    - `(*QueryService).Transaction(ctx, id uuid.UUID, v Viewer) (*wagering.WagerTransaction, error)`
    - `(*QueryService).TransactionByExternalID(ctx, providerID, externalTransactionID string) (*wagering.WagerTransaction, error)`
  - Ledger:
    - `(*QueryService).Ledger(ctx, walletID uuid.UUID, cursor string, limit int) (LedgerPage, error)`
    - `app.LedgerPage{Entries []wallet.LedgerEntry; NextCursor string}`
  - Constantes `app.DefaultLedgerLimit = 50` e `app.MaxLedgerLimit = 200`.
  - Cursor (unexported): `encodeCursor` e `decodeCursor`.

- [ ] **Step 1: Escrever os testes que falham**

`internal/app/query_service_unit_test.go`:

```go
//go:build !integration && !e2e

package app

import (
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestCursorRoundTrip(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	got, err := decodeCursor(encodeCursor(id))
	if err != nil || got != id {
		t.Fatalf("round trip = %s, %v", got, err)
	}
	if got, err := decodeCursor(""); err != nil || got != uuid.Nil {
		t.Fatalf("empty cursor = %s, %v; want first page", got, err)
	}
	for _, bad := range []string{"!!!", "AAAA", encodeCursor(id) + "x"} {
		if _, err := decodeCursor(bad); !errors.Is(err, ErrInvalidCursor) {
			t.Errorf("decodeCursor(%q) error = %v, want ErrInvalidCursor", bad, err)
		}
	}
}
```

`internal/app/query_service_test.go`:

```go
//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
)

func TestTransactionVisibility(t *testing.T) {
	h := apptest.New(t)
	q := app.NewQueryService(h.Deps)
	w := h.OpenWallet(t, "100.00")
	cmd := apptest.Command(t, w, "provider-a", "BET", "10.00", "")
	bet := process(t, newWagering(t, h), cmd).Transaction
	ctx := context.Background()

	if got, err := q.Transaction(ctx, bet.ID(), app.Viewer{ProviderID: "provider-a"}); err != nil || got.ID() != bet.ID() {
		t.Fatalf("owner: %v", err)
	}
	if _, err := q.Transaction(ctx, bet.ID(), app.Viewer{ProviderID: "provider-b"}); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("another provider must get ErrNotFound: %v", err)
	}
	if _, err := q.Transaction(ctx, bet.ID(), app.Viewer{Internal: true}); err != nil {
		t.Fatalf("internal: %v", err)
	}
	entries, _ := h.Ledger.List(ctx, w.ID(), uuid.Nil, 1)
	openingID := entries[0].TransactionID()
	if _, err := q.Transaction(ctx, openingID, app.Viewer{ProviderID: "provider-a"}); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("providers never see the internal OPENING: %v", err)
	}
	if _, err := q.Transaction(ctx, openingID, app.Viewer{Internal: true}); err != nil {
		t.Fatalf("internal sees OPENING: %v", err)
	}
	if _, err := q.Transaction(ctx, uuid.New(), app.Viewer{Internal: true}); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}

	if got, err := q.TransactionByExternalID(ctx, "provider-a", cmd.ExternalTransactionID); err != nil || got.ID() != bet.ID() {
		t.Fatalf("by external id: %v", err)
	}
	if _, err := q.TransactionByExternalID(ctx, "provider-b", cmd.ExternalTransactionID); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("external ids are scoped by provider: %v", err)
	}
	if _, err := q.Wallet(ctx, w.ID()); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Wallet(ctx, uuid.New()); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing wallet: %v", err)
	}
}

func TestLedgerPagination(t *testing.T) {
	h := apptest.New(t)
	q := app.NewQueryService(h.Deps)
	svc := newWagering(t, h)
	w := h.OpenWallet(t, "100.00")
	for range 4 {
		process(t, svc, apptest.Command(t, w, "provider-a", "BET", "1.00", ""))
	}
	ctx := context.Background()

	var sizes []int
	var seen []uuid.UUID
	cursor := ""
	for {
		page, err := q.Ledger(ctx, w.ID(), cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(page.Entries))
		for _, e := range page.Entries {
			seen = append(seen, e.ID())
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if len(sizes) != 3 || sizes[0] != 2 || sizes[1] != 2 || sizes[2] != 1 || len(seen) != 5 {
		t.Fatalf("page sizes %v, seen %d", sizes, len(seen))
	}
	all, err := q.Ledger(ctx, w.ID(), "", 0)
	if err != nil || len(all.Entries) != 5 || all.NextCursor != "" {
		t.Fatalf("default limit page: %d entries next %q err %v", len(all.Entries), all.NextCursor, err)
	}
	for i, e := range all.Entries {
		if e.ID() != seen[i] {
			t.Fatal("pages must follow the same stable order")
		}
	}

	if _, err := q.Ledger(ctx, w.ID(), "", app.MaxLedgerLimit+1); !errors.Is(err, app.ErrInvalidLimit) {
		t.Fatalf("limit too large: %v", err)
	}
	if _, err := q.Ledger(ctx, w.ID(), "", -1); !errors.Is(err, app.ErrInvalidLimit) {
		t.Fatalf("negative limit: %v", err)
	}
	if _, err := q.Ledger(ctx, w.ID(), "not-a-cursor", 2); !errors.Is(err, app.ErrInvalidCursor) {
		t.Fatalf("bad cursor: %v", err)
	}
	if _, err := q.Ledger(ctx, uuid.New(), "", 2); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing wallet: %v", err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test -tags=unit ./internal/app/... ; go test -tags=integration ./internal/app/ -run 'Visibility|Pagination'`
Expected: FAIL de compilação, `undefined: decodeCursor` e `undefined: app.NewQueryService`.

- [ ] **Step 3: Implementar**

`internal/app/query_service.go`:

```go
package app

import (
	"context"
	"encoding/base64"
	"fmt"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
)

// Ledger page sizes.
const (
	DefaultLedgerLimit = 50
	MaxLedgerLimit     = 200
)

// LedgerPage is one page of ledger entries ordered by id. NextCursor is
// empty on the last page.
type LedgerPage struct {
	Entries    []wallet.LedgerEntry
	NextCursor string
}

// QueryService answers read-only questions with per-provider visibility.
type QueryService struct {
	d Deps
}

// NewQueryService builds a QueryService.
func NewQueryService(d Deps) *QueryService { return &QueryService{d: d} }

// Wallet reads a wallet.
func (s *QueryService) Wallet(ctx context.Context, id uuid.UUID) (*wallet.Wallet, error) {
	return s.d.Wallets.Get(ctx, id)
}

// Transaction reads a transaction the viewer may see. A transaction of
// another provider (or the internal OPENING, for providers) is reported as
// ErrNotFound so its existence is not disclosed.
func (s *QueryService) Transaction(ctx context.Context, id uuid.UUID, v Viewer) (*wagering.WagerTransaction, error) {
	t, err := s.d.Transactions.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !v.canSee(t) {
		return nil, fmt.Errorf("%w: transaction %s", ErrNotFound, id)
	}
	return t, nil
}

// TransactionByExternalID reads providerID's transaction by its external id.
// The caller authorizes providerID.
func (s *QueryService) TransactionByExternalID(ctx context.Context, providerID, externalTransactionID string) (*wagering.WagerTransaction, error) {
	return s.d.Transactions.FindByExternalID(ctx, providerID, externalTransactionID)
}

// Ledger returns a page of the wallet's ledger. cursor is opaque (empty for
// the first page); limit 0 means DefaultLedgerLimit.
func (s *QueryService) Ledger(ctx context.Context, walletID uuid.UUID, cursor string, limit int) (LedgerPage, error) {
	if limit == 0 {
		limit = DefaultLedgerLimit
	}
	if limit < 1 || limit > MaxLedgerLimit {
		return LedgerPage{}, fmt.Errorf("%w: %d (1..%d)", ErrInvalidLimit, limit, MaxLedgerLimit)
	}
	after, err := decodeCursor(cursor)
	if err != nil {
		return LedgerPage{}, err
	}
	if _, err := s.d.Wallets.Get(ctx, walletID); err != nil {
		return LedgerPage{}, err
	}
	entries, err := s.d.Ledger.List(ctx, walletID, after, limit+1)
	if err != nil {
		return LedgerPage{}, err
	}
	page := LedgerPage{Entries: entries}
	if len(entries) > limit {
		page.Entries = entries[:limit]
		page.NextCursor = encodeCursor(entries[limit-1].ID())
	}
	return page, nil
}

// encodeCursor hides the last entry id behind base64url.
func encodeCursor(id uuid.UUID) string { return base64.RawURLEncoding.EncodeToString(id[:]) }

// decodeCursor parses a cursor; "" is the first page.
func decodeCursor(cursor string) (uuid.UUID, error) {
	if cursor == "" {
		return uuid.Nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || len(raw) != len(uuid.UUID{}) {
		return uuid.Nil, ErrInvalidCursor
	}
	id, err := uuid.FromBytes(raw)
	if err != nil {
		return uuid.Nil, ErrInvalidCursor
	}
	return id, nil
}
```

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=unit ./internal/app/... && go test -race -tags=integration ./internal/app/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/app
git commit -m "feat(app): scoped transaction queries and cursor-paginated ledger" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 8: Reconciliação

**Files:**
- Create: `internal/app/reconciliation_service.go`
- Test: `internal/app/reconciliation_service_test.go` (integration)

**Interfaces:**
- Consumes: `TxManager.WithinSnapshot`, `LedgerRepository.Totals` (Task 1), `Wallets.Get`, `money.FromMinor`,
  `Money.Sub` e `Metrics.ReconciliationDivergence`.
- Produces:
  - `app.Reconciliation{WalletID uuid.UUID; Stored, Calculated, Difference money.Money; Consistent bool;
    CheckedEntries int64}`.
  - `app.NewReconciliationService(d Deps) *ReconciliationService` e
    `(*ReconciliationService).Reconcile(ctx, walletID uuid.UUID) (Reconciliation, error)`.

- [ ] **Step 1: Escrever o teste que falha**

`internal/app/reconciliation_service_test.go`:

```go
//go:build !unit && !e2e

package app_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func TestReconcileConsistentWallet(t *testing.T) {
	h := apptest.New(t)
	w := h.OpenWallet(t, "1000.00")
	process(t, newWagering(t, h), apptest.Command(t, w, "provider-a", "BET", "25.00", ""))

	r, err := app.NewReconciliationService(h.Deps).Reconcile(context.Background(), w.ID())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Consistent || r.Stored.Amount() != "975.00" || r.Calculated.Amount() != "975.00" ||
		r.Difference.Amount() != "0.00" || r.CheckedEntries != 2 || r.WalletID != w.ID() {
		t.Fatalf("reconciliation = %+v", r)
	}
	if h.Metrics.Count("divergence") != 0 {
		t.Fatal("no divergence expected")
	}
}

func TestReconcileReportsDivergenceWithoutFixingIt(t *testing.T) {
	h := apptest.New(t)
	w := h.OpenWallet(t, "100.00")
	// Simulate corruption the application could never produce.
	if err := pgtest.OwnerDB(t).Exec("UPDATE wallets SET balance_minor = balance_minor + 100 WHERE id = ?", w.ID()).Error; err != nil {
		t.Fatal(err)
	}
	r, err := app.NewReconciliationService(h.Deps).Reconcile(context.Background(), w.ID())
	if err != nil {
		t.Fatal(err)
	}
	if r.Consistent || r.Stored.Amount() != "101.00" || r.Calculated.Amount() != "100.00" || r.Difference.Amount() != "1.00" {
		t.Fatalf("reconciliation = %+v", r)
	}
	if h.Metrics.Count("divergence") != 1 {
		t.Fatal("divergence metric not recorded")
	}
	if balance(t, h, w.ID()) != "101.00" {
		t.Fatal("reconciliation must not change the stored balance")
	}
}

func TestReconcileEmptyAndMissingWallets(t *testing.T) {
	h := apptest.New(t)
	empty := h.OpenWallet(t, "0.00")
	svc := app.NewReconciliationService(h.Deps)
	r, err := svc.Reconcile(context.Background(), empty.ID())
	if err != nil || !r.Consistent || r.CheckedEntries != 0 || !r.Calculated.IsZero() {
		t.Fatalf("empty wallet = %+v, %v", r, err)
	}
	if _, err := svc.Reconcile(context.Background(), uuid.New()); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("missing wallet error = %v", err)
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falha**

Run: `go test -tags=integration ./internal/app/ -run Reconcile`
Expected: FAIL de compilação, `undefined: app.NewReconciliationService`.

- [ ] **Step 3: Implementar**

`internal/app/reconciliation_service.go`:

```go
package app

import (
	"context"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

// Reconciliation compares the stored balance with the balance rebuilt from
// the ledger. Difference = Stored - Calculated.
type Reconciliation struct {
	WalletID       uuid.UUID
	Stored         money.Money
	Calculated     money.Money
	Difference     money.Money
	Consistent     bool
	CheckedEntries int64
}

// ReconciliationService checks wallets against their ledger. It never
// changes data: divergences are reported in the result, logs and metrics.
type ReconciliationService struct {
	d Deps
}

// NewReconciliationService builds a ReconciliationService.
func NewReconciliationService(d Deps) *ReconciliationService { return &ReconciliationService{d: d} }

// Reconcile reads the wallet and its ledger totals from one consistent,
// read-only snapshot.
func (s *ReconciliationService) Reconcile(ctx context.Context, walletID uuid.UUID) (Reconciliation, error) {
	var r Reconciliation
	err := s.d.Tx.WithinSnapshot(ctx, func(ctx context.Context) error {
		w, err := s.d.Wallets.Get(ctx, walletID)
		if err != nil {
			return err
		}
		net, count, err := s.d.Ledger.Totals(ctx, walletID)
		if err != nil {
			return err
		}
		calculated, err := money.FromMinor(net, w.Currency())
		if err != nil {
			return err
		}
		difference, err := w.Balance().Sub(calculated)
		if err != nil {
			return err
		}
		r = Reconciliation{
			WalletID: walletID, Stored: w.Balance(), Calculated: calculated, Difference: difference,
			Consistent: difference.IsZero(), CheckedEntries: count,
		}
		return nil
	})
	if err != nil {
		return Reconciliation{}, err
	}
	if !r.Consistent {
		s.d.Metrics.ReconciliationDivergence()
		s.d.Log.WarnContext(ctx, "wallet reconciliation divergence",
			"walletId", walletID.String(), "difference", r.Difference.String(), "checkedEntries", r.CheckedEntries)
	}
	return r, nil
}
```

- [ ] **Step 4: Rodar os testes**

Run: `go test -race -tags=integration ./internal/app/...`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
gofmt -l . && go vet ./...
git add internal/app
git commit -m "feat(app): wallet reconciliation from a consistent read-only snapshot" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```

---

### Task 9: Configuração da política de referências e módulo Fx da aplicação

**Files:**
- Modify: `internal/platform/config/config.go`, `internal/platform/config/config_test.go`
- Create: `internal/app/module.go`
- Test: `internal/app/module_test.go` (integration, package `app_test`)

**Interfaces:**
- Consumes: `config.Config` e `positiveDuration`/`positiveInt` (existentes); `postgres.Module` e `config.Module`
  (Plano 2); todos os construtores de serviço.
- Produces:
  - `config.Wagering{ReferenceRetryBaseDelay, ReferenceRetryMaxDelay time.Duration; ReferenceRetryMaxAttempts int}`
    em `Config.Wagering`. Variáveis de ambiente: `REFERENCE_RETRY_BASE_DELAY` (2s), `REFERENCE_RETRY_MAX_DELAY` (5m)
    e `REFERENCE_RETRY_MAX_ATTEMPTS` (10). O máximo precisa ser ≥ a base.
  - `app.Module`, que fornece `Clock`, `Deps`, `*WalletService`, `*WageringService`, `*QueryService` e
    `*ReconciliationService`. Ele **exige** de fora um `app.Metrics` e um `*slog.Logger`, que o Plano 4 fornece.

- [ ] **Step 1: Escrever os testes que falham**

Acrescentar a `internal/platform/config/config_test.go`:

```go
func TestLoadWageringDefaultsAndOverrides(t *testing.T) {
	cfg, err := Load(env(map[string]string{"DATABASE_URL": "postgres://x"}))
	if err != nil {
		t.Fatal(err)
	}
	want := Wagering{ReferenceRetryBaseDelay: 2 * time.Second, ReferenceRetryMaxDelay: 5 * time.Minute, ReferenceRetryMaxAttempts: 10}
	if cfg.Wagering != want {
		t.Fatalf("Wagering = %+v, want %+v", cfg.Wagering, want)
	}
	cfg, err = Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "REFERENCE_RETRY_BASE_DELAY": "100ms",
		"REFERENCE_RETRY_MAX_DELAY": "1s", "REFERENCE_RETRY_MAX_ATTEMPTS": "3",
	}))
	if err != nil || cfg.Wagering.ReferenceRetryBaseDelay != 100*time.Millisecond || cfg.Wagering.ReferenceRetryMaxAttempts != 3 {
		t.Fatalf("overrides = %+v, %v", cfg.Wagering, err)
	}
}

func TestLoadWageringInvalid(t *testing.T) {
	_, err := Load(env(map[string]string{
		"DATABASE_URL": "postgres://x", "REFERENCE_RETRY_BASE_DELAY": "10s", "REFERENCE_RETRY_MAX_DELAY": "1s",
		"REFERENCE_RETRY_MAX_ATTEMPTS": "0",
	}))
	if err == nil || !strings.Contains(err.Error(), "REFERENCE_RETRY_MAX_DELAY") || !strings.Contains(err.Error(), "REFERENCE_RETRY_MAX_ATTEMPTS") {
		t.Fatalf("error = %v, want both keys reported", err)
	}
}
```

`internal/app/module_test.go`:

```go
//go:build !unit && !e2e

package app_test

import (
	"context"
	"log/slog"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/postgres"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
)

func externalDeps() fx.Option {
	return fx.Options(
		fx.Provide(func() app.Metrics { return app.NopMetrics{} }),
		fx.Provide(func() *slog.Logger { return slog.New(slog.DiscardHandler) }),
	)
}

func TestAppModuleGraph(t *testing.T) {
	t.Setenv("DATABASE_URL", pgtest.AppURL())
	err := fx.ValidateApp(fx.NopLogger, config.Module, postgres.Module, app.Module, externalDeps(),
		fx.Invoke(func(*app.WalletService, *app.WageringService, *app.QueryService, *app.ReconciliationService) {}))
	if err != nil {
		t.Fatal(err)
	}
}

func TestAppModuleServesUseCases(t *testing.T) {
	pgtest.AppDB(t)
	t.Setenv("DATABASE_URL", pgtest.AppURL())
	var wallets *app.WalletService
	fxApp := fxtest.New(t, fx.NopLogger, config.Module, postgres.Module, app.Module, externalDeps(), fx.Populate(&wallets))
	fxApp.RequireStart()
	defer fxApp.RequireStop()
	cmd, err := app.ParseOpenWallet(uuid.NewString(), "10.00", "BRL")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wallets.Open(context.Background(), cmd, app.Meta{}); err != nil {
		t.Fatal(err)
	}
}

func TestAppModuleRejectsInvalidRetryPolicy(t *testing.T) {
	t.Setenv("DATABASE_URL", pgtest.AppURL())
	t.Setenv("REFERENCE_RETRY_MAX_ATTEMPTS", "0")
	fxApp := fx.New(fx.NopLogger, config.Module, postgres.Module, app.Module, externalDeps(),
		fx.Invoke(func(*app.WageringService) {}))
	if fxApp.Err() == nil {
		t.Fatal("an invalid retry policy must stop the application before it starts")
	}
}
```

- [ ] **Step 2: Rodar e confirmar que falham**

Run: `go test -tags=unit ./internal/platform/... ; go test -tags=integration ./internal/app/ -run Module`
Expected: FAIL de compilação, `undefined: Wagering` e `undefined: app.Module`.

- [ ] **Step 3: Implementar**

Em `internal/platform/config/config.go`, acrescentar ao `Config` o campo `Wagering Wagering`, o tipo e o carregamento:

```go
// Wagering configures how PENDING_REFERENCE operations wait for their reference.
type Wagering struct {
	ReferenceRetryBaseDelay   time.Duration
	ReferenceRetryMaxDelay    time.Duration
	ReferenceRetryMaxAttempts int
}
```

Em `Load`, depois de montar `db` e antes do `if len(errs) > 0`:

```go
	wagering := Wagering{
		ReferenceRetryBaseDelay:   positiveDuration(getenv, "REFERENCE_RETRY_BASE_DELAY", 2*time.Second, &errs),
		ReferenceRetryMaxDelay:    positiveDuration(getenv, "REFERENCE_RETRY_MAX_DELAY", 5*time.Minute, &errs),
		ReferenceRetryMaxAttempts: positiveInt(getenv, "REFERENCE_RETRY_MAX_ATTEMPTS", 10, &errs),
	}
	if wagering.ReferenceRetryMaxDelay != 0 && wagering.ReferenceRetryMaxDelay < wagering.ReferenceRetryBaseDelay {
		errs = append(errs, fmt.Errorf("REFERENCE_RETRY_MAX_DELAY (%s) must be >= REFERENCE_RETRY_BASE_DELAY (%s)",
			wagering.ReferenceRetryMaxDelay, wagering.ReferenceRetryBaseDelay))
	}
```

e retornar `Config{Database: db, Wagering: wagering}`.

`internal/app/module.go`:

```go
package app

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

// Module wires the use cases. It requires the persistence ports (postgres
// module), a Metrics and a *slog.Logger from the composition root.
var Module = fx.Module("app",
	fx.Provide(
		func() Clock { return SystemClock },
		newDeps,
		NewWalletService,
		func(d Deps, cfg config.Config) (*WageringService, error) {
			return NewWageringService(d, retryPolicy(cfg))
		},
		NewQueryService,
		NewReconciliationService,
	),
)

type depsIn struct {
	fx.In
	Tx           TxManager
	Wallets      WalletRepository
	Transactions TransactionRepository
	Ledger       LedgerRepository
	Outbox       OutboxRepository
	Clock        Clock
	Metrics      Metrics
	Log          *slog.Logger
}

func newDeps(in depsIn) Deps {
	return Deps{
		Tx: in.Tx, Wallets: in.Wallets, Transactions: in.Transactions, Ledger: in.Ledger, Outbox: in.Outbox,
		Clock: in.Clock, Metrics: in.Metrics, Log: in.Log,
	}
}

func retryPolicy(cfg config.Config) wagering.RetryPolicy {
	return wagering.RetryPolicy{
		BaseDelay:   cfg.Wagering.ReferenceRetryBaseDelay,
		MaxDelay:    cfg.Wagering.ReferenceRetryMaxDelay,
		MaxAttempts: cfg.Wagering.ReferenceRetryMaxAttempts,
	}
}
```

- [ ] **Step 4: Verificação final do plano**

Run:

```bash
docker compose --profile test up -d --wait postgres_test && docker compose --profile test run --rm migrate_test
gofmt -l . && go vet ./... && go test -race -tags=unit ./... && go test -race -count=1 ./...
go list -f '{{join .Imports "\n"}}' ./internal/domain/... | sort -u
go list -f '{{join .Imports "\n"}}' ./internal/app | sort -u
```

Expected:
- Todos os testes dão `ok`.
- O domínio só importa stdlib, uuid e `internal/domain`.
- `internal/app` não importa `gorm`, `gin`, `aws` nem `internal/adapters`. Só `module.go` importa `go.uber.org/fx`
  e `internal/platform/config`.

- [ ] **Step 5: Commit**

```bash
go mod tidy
git add go.mod go.sum internal/app internal/platform/config
git commit -m "feat(app): fx module for use cases and configurable reference retry policy" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>"
```
