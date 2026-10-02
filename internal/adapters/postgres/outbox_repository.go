package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
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
	lastError = strings.ReplaceAll(strings.ToValidUTF8(lastError, "?"), "\x00", "")
	res := conn(ctx, r.db).Exec(`
UPDATE outbox_events
   SET next_attempt_at = now() + make_interval(secs => ?), locked_by = NULL, locked_until = NULL, last_error = ?
 WHERE id = ? AND locked_by = ? AND published_at IS NULL`, delay.Seconds(), lastError, id, owner)
	return mapError(res.Error)
}
