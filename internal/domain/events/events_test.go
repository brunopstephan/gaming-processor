//go:build !integration && !e2e

package events

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
)

func brl(t *testing.T, amount string) money.Money {
	t.Helper()
	m, err := money.Parse(amount, "BRL")
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestEnvelopeJSONContract(t *testing.T) {
	walletID := uuid.MustParse("0192f291-27dd-7d3f-8071-5f8685deef37")
	data := WalletBalanceChanged{
		WalletID:      walletID,
		TransactionID: uuid.MustParse("0192f298-345e-7e38-af88-e43f851a819d"),
		Direction:     "DEBIT",
		Money:         brl(t, "25.00"),
		BalanceBefore: brl(t, "1000.00"),
		BalanceAfter:  brl(t, "975.00"),
		WalletVersion: 2,
	}
	occurred := time.Date(2026, 9, 8, 9, 0, 0, 0, time.FixedZone("BRT", -3*3600))
	env, err := NewEnvelope(uuid.MustParse("0192f2a0-0000-7000-8000-000000000001"), data, "corr-1", "msg-123", occurred)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"eventId":"0192f2a0-0000-7000-8000-000000000001","eventType":"WalletBalanceChanged",` +
		`"aggregateId":"0192f291-27dd-7d3f-8071-5f8685deef37","correlationId":"corr-1","causationId":"msg-123",` +
		`"occurredAt":"2026-09-08T12:00:00.000Z","version":1,"data":{"walletId":"0192f291-27dd-7d3f-8071-5f8685deef37",` +
		`"transactionId":"0192f298-345e-7e38-af88-e43f851a819d","direction":"DEBIT",` +
		`"money":{"amount":"25.00","currency":"BRL"},"balanceBefore":{"amount":"1000.00","currency":"BRL"},` +
		`"balanceAfter":{"amount":"975.00","currency":"BRL"},"walletVersion":2}}`
	if string(b) != want {
		t.Fatalf("envelope JSON\n got: %s\nwant: %s", b, want)
	}
}

func TestEnvelopeOmitsEmptyCausation(t *testing.T) {
	data := WagerTransactionRejected{
		TransactionID: uuid.New(), WalletID: uuid.New(), ProviderID: "provider-a",
		ExternalTransactionID: "tx-1", Kind: "BET", Money: brl(t, "80.00"), FailureCode: "INSUFFICIENT_FUNDS",
	}
	env, err := NewEnvelope(uuid.New(), data, "corr", "", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(env)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["causationId"]; ok {
		t.Fatal("empty causationId must be omitted")
	}
	if m["eventType"] != TypeWagerTransactionRejected || m["aggregateId"] != data.WalletID.String() {
		t.Fatalf("envelope = %v", m)
	}
}

func TestEventTypesAndVersions(t *testing.T) {
	tests := []struct {
		data Data
		want string
	}{
		{WagerTransactionProcessed{}, "WagerTransactionProcessed"},
		{WagerTransactionRejected{}, "WagerTransactionRejected"},
		{WalletBalanceChanged{}, "WalletBalanceChanged"},
		{WagerTransactionPendingReference{}, "WagerTransactionPendingReference"},
	}
	for _, tt := range tests {
		if tt.data.EventType() != tt.want || tt.data.EventVersion() != 1 {
			t.Errorf("%T: type %q version %d", tt.data, tt.data.EventType(), tt.data.EventVersion())
		}
	}
}

func TestNewEnvelopeValidation(t *testing.T) {
	data := WalletBalanceChanged{WalletID: uuid.New()}
	now := time.Now()
	cases := map[string]func() (Envelope, error){
		"nil event id": func() (Envelope, error) { return NewEnvelope(uuid.Nil, data, "c", "", now) },
		"nil data":     func() (Envelope, error) { return NewEnvelope(uuid.New(), nil, "c", "", now) },
		"no correlation": func() (Envelope, error) {
			return NewEnvelope(uuid.New(), data, "", "", now)
		},
		"zero time": func() (Envelope, error) { return NewEnvelope(uuid.New(), data, "c", "", time.Time{}) },
		"nil aggregate": func() (Envelope, error) {
			return NewEnvelope(uuid.New(), WalletBalanceChanged{}, "c", "", now)
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := fn(); !errors.Is(err, ErrInvalidEnvelope) {
				t.Fatalf("error = %v, want ErrInvalidEnvelope", err)
			}
		})
	}
}
