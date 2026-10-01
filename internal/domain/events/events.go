// Package events defines the integration events written to the outbox and
// published after commit. Each concrete type fixes its own event type and
// schema version; the envelope copies them at construction.
package events

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ErrInvalidEnvelope reports missing envelope metadata.
var ErrInvalidEnvelope = errors.New("events: invalid envelope")

// Data is the typed payload of an integration event.
type Data interface {
	EventType() string
	EventVersion() int
	AggregateID() uuid.UUID
}

const timeLayout = "2006-01-02T15:04:05.000Z07:00"

// FormatTime renders t as RFC 3339 in UTC with millisecond precision.
func FormatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// Envelope is the immutable outer structure of every published event.
type Envelope struct {
	EventID       uuid.UUID `json:"eventId"`
	EventType     string    `json:"eventType"`
	AggregateID   uuid.UUID `json:"aggregateId"`
	CorrelationID string    `json:"correlationId"`
	CausationID   string    `json:"causationId,omitempty"`
	OccurredAt    string    `json:"occurredAt"`
	Version       int       `json:"version"`
	Data          Data      `json:"data"`
}

// NewEnvelope wraps data. Event type, version and aggregate come from data.
func NewEnvelope(eventID uuid.UUID, data Data, correlationID, causationID string, occurredAt time.Time) (Envelope, error) {
	switch {
	case eventID == uuid.Nil:
		return Envelope{}, fmt.Errorf("%w: eventId is required", ErrInvalidEnvelope)
	case data == nil:
		return Envelope{}, fmt.Errorf("%w: data is required", ErrInvalidEnvelope)
	case data.AggregateID() == uuid.Nil:
		return Envelope{}, fmt.Errorf("%w: aggregateId is required", ErrInvalidEnvelope)
	case correlationID == "":
		return Envelope{}, fmt.Errorf("%w: correlationId is required", ErrInvalidEnvelope)
	case occurredAt.IsZero():
		return Envelope{}, fmt.Errorf("%w: occurredAt is required", ErrInvalidEnvelope)
	}
	return Envelope{
		EventID:       eventID,
		EventType:     data.EventType(),
		AggregateID:   data.AggregateID(),
		CorrelationID: correlationID,
		CausationID:   causationID,
		OccurredAt:    FormatTime(occurredAt),
		Version:       data.EventVersion(),
		Data:          data,
	}, nil
}
