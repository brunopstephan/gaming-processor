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
