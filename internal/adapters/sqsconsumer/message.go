package sqsconsumer

import (
	"fmt"
	"strings"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/jsonstrict"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// MessageTypeWagerRequested is the only message type of the input queue.
const MessageTypeWagerRequested = "WagerTransactionRequested"

type moneyData struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

type requestData struct {
	ProviderID                     string    `json:"providerId"`
	ExternalTransactionID          string    `json:"externalTransactionId"`
	IdempotencyKey                 string    `json:"idempotencyKey"`
	PlayerID                       string    `json:"playerId"`
	WalletID                       string    `json:"walletId"`
	RoundID                        string    `json:"roundId"`
	GameID                         string    `json:"gameId"`
	Kind                           string    `json:"kind"`
	Money                          moneyData `json:"money"`
	ReferenceExternalTransactionID string    `json:"referenceExternalTransactionId"`
}

// envelope is the message body. correlationId is optional; without it the
// messageId correlates the logs and events.
type envelope struct {
	MessageID     string      `json:"messageId"`
	Type          string      `json:"type"`
	OccurredAt    string      `json:"occurredAt"`
	CorrelationID string      `json:"correlationId"`
	Data          requestData `json:"data"`
}

// request is a decoded message whose envelope is valid.
type request struct {
	MessageID     string
	CorrelationID string
	Raw           wagering.RawCommand
}

// parseRequest decodes body strictly and validates the envelope. Every error
// is a *wagering.InputError (CORRECTABLE); the data itself is validated later
// by wagering.ParseCommand.
func parseRequest(body string) (request, error) {
	var env envelope
	if err := jsonstrict.Decode(strings.NewReader(body), &env); err != nil {
		return request{}, err
	}
	var v []wagering.Violation
	add := func(code wagering.FailureCode, field, reason string) {
		v = append(v, wagering.Violation{Code: code, Field: field, Reason: reason})
	}
	tooLong := fmt.Sprintf("must be at most %d bytes", wagering.MaxFieldLength)
	switch {
	case env.MessageID == "":
		add(wagering.FailureMissingField, "messageId", "is required")
	case len(env.MessageID) > wagering.MaxFieldLength:
		add(wagering.FailureInvalidField, "messageId", tooLong)
	}
	switch {
	case env.Type == "":
		add(wagering.FailureMissingField, "type", "is required")
	case env.Type != MessageTypeWagerRequested:
		add(wagering.FailureInvalidField, "type", "must be "+MessageTypeWagerRequested)
	}
	if env.OccurredAt == "" {
		add(wagering.FailureMissingField, "occurredAt", "is required")
	} else if _, err := time.Parse(time.RFC3339Nano, env.OccurredAt); err != nil {
		add(wagering.FailureInvalidField, "occurredAt", "must be an RFC 3339 timestamp")
	}
	if len(env.CorrelationID) > wagering.MaxFieldLength {
		add(wagering.FailureInvalidField, "correlationId", tooLong)
	}
	if len(v) > 0 {
		return request{}, &wagering.InputError{Violations: v}
	}
	d := env.Data
	return request{MessageID: env.MessageID, CorrelationID: env.CorrelationID, Raw: wagering.RawCommand{
		ProviderID: d.ProviderID, ExternalTransactionID: d.ExternalTransactionID, IdempotencyKey: d.IdempotencyKey,
		PlayerID: d.PlayerID, WalletID: d.WalletID, RoundID: d.RoundID, GameID: d.GameID, Kind: d.Kind,
		Amount: d.Money.Amount, Currency: d.Money.Currency,
		ReferenceExternalTransactionID: d.ReferenceExternalTransactionID,
	}}, nil
}
