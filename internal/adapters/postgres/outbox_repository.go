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
