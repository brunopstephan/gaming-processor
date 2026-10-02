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
