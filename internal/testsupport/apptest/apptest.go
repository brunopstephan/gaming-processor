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
	Inbox        *postgres.InboxRepository
	Metrics      *RecordingMetrics
}

// Clock matches PostgreSQL's microsecond precision, so State round trips compare equal.
func Clock() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }

// New connects to postgres_test (failing fast with setup instructions) and
// builds the dependencies of the use cases.
func New(t testing.TB) *Harness {
	t.Helper()
	pgtest.AppDB(t)
	return newHarness(t, pgtest.AppURL())
}

// NewIsolated is New over a fresh database of its own (pgtest.FreshDatabase).
func NewIsolated(t testing.TB) *Harness {
	t.Helper()
	return newHarness(t, pgtest.FreshDatabase(t))
}

func newHarness(t testing.TB, url string) *Harness {
	t.Helper()
	dbCfg := config.Database{URL: url, MaxOpenConns: 40, LockTimeout: 5 * time.Second, StatementTimeout: 10 * time.Second}
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
		Inbox:        postgres.NewInboxRepository(db),
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

// InboxDuplicate implements app.Metrics.
func (m *RecordingMetrics) InboxDuplicate() { m.inc("inbox_duplicate") }

// OutboxPublishAttempt implements app.Metrics.
func (m *RecordingMetrics) OutboxPublishAttempt(result string) { m.inc("outbox:" + result) }

// OutboxLag implements app.Metrics.
func (m *RecordingMetrics) OutboxLag(time.Duration) { m.inc("outbox_lag") }

// ReferenceRetry implements app.Metrics.
func (m *RecordingMetrics) ReferenceRetry() { m.inc("reference_retry") }
