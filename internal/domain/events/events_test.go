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

func marshalData(t *testing.T, d Data) string {
	t.Helper()
	env, err := NewEnvelope(uuid.MustParse("0192f2a0-0000-7000-8000-000000000001"), d, "corr-1", "", time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatal(err)
	}
	return string(parsed.Data)
}

func TestWagerTransactionProcessedExternalJSON(t *testing.T) {
	ref := uuid.MustParse("0192f298-345e-7e38-af88-e43f851a819e")
	got := marshalData(t, WagerTransactionProcessed{
		TransactionID:          uuid.MustParse("0192f298-345e-7e38-af88-e43f851a819d"),
		WalletID:               uuid.MustParse("0192f291-27dd-7d3f-8071-5f8685deef37"),
		PlayerID:               uuid.MustParse("0192f291-27dd-7d3f-8071-5f8685deef38"),
		Origin:                 "EXTERNAL",
		Kind:                   "REFUND",
		Money:                  brl(t, "25.00"),
		ProviderID:             "provider-a",
		ExternalTransactionID:  "tx-2",
		RoundID:                "round-1",
		GameID:                 "game-1",
		ReferenceTransactionID: &ref,
		BalanceAfter:           brl(t, "100.00"),
	})
	want := `{"transactionId":"0192f298-345e-7e38-af88-e43f851a819d","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37",` +
		`"playerId":"0192f291-27dd-7d3f-8071-5f8685deef38","origin":"EXTERNAL","kind":"REFUND",` +
		`"money":{"amount":"25.00","currency":"BRL"},"providerId":"provider-a","externalTransactionId":"tx-2",` +
		`"roundId":"round-1","gameId":"game-1","referenceTransactionId":"0192f298-345e-7e38-af88-e43f851a819e",` +
		`"balanceAfter":{"amount":"100.00","currency":"BRL"}}`
	if got != want {
		t.Fatalf("data JSON\n got: %s\nwant: %s", got, want)
	}
}

func TestWagerTransactionProcessedOpeningJSON(t *testing.T) {
	got := marshalData(t, WagerTransactionProcessed{
		TransactionID: uuid.MustParse("0192f298-345e-7e38-af88-e43f851a819d"),
		WalletID:      uuid.MustParse("0192f291-27dd-7d3f-8071-5f8685deef37"),
		PlayerID:      uuid.MustParse("0192f291-27dd-7d3f-8071-5f8685deef38"),
		Origin:        "INTERNAL",
		Kind:          "OPENING",
		Money:         brl(t, "1000.00"),
		BalanceAfter:  brl(t, "1000.00"),
	})
	want := `{"transactionId":"0192f298-345e-7e38-af88-e43f851a819d","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37",` +
		`"playerId":"0192f291-27dd-7d3f-8071-5f8685deef38","origin":"INTERNAL","kind":"OPENING",` +
		`"money":{"amount":"1000.00","currency":"BRL"},"balanceAfter":{"amount":"1000.00","currency":"BRL"}}`
	if got != want {
		t.Fatalf("data JSON\n got: %s\nwant: %s", got, want)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"providerId", "externalTransactionId", "roundId", "gameId", "referenceTransactionId"} {
		if _, ok := m[k]; ok {
			t.Errorf("%s must be absent for OPENING", k)
		}
	}
}

func TestWagerTransactionRejectedJSON(t *testing.T) {
	got := marshalData(t, WagerTransactionRejected{
		TransactionID:         uuid.MustParse("0192f298-345e-7e38-af88-e43f851a819d"),
		WalletID:              uuid.MustParse("0192f291-27dd-7d3f-8071-5f8685deef37"),
		ProviderID:            "provider-a",
		ExternalTransactionID: "tx-1",
		Kind:                  "BET",
		Money:                 brl(t, "80.00"),
		FailureCode:           "INSUFFICIENT_FUNDS",
	})
	want := `{"transactionId":"0192f298-345e-7e38-af88-e43f851a819d","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37",` +
		`"providerId":"provider-a","externalTransactionId":"tx-1","kind":"BET",` +
		`"money":{"amount":"80.00","currency":"BRL"},"failureCode":"INSUFFICIENT_FUNDS"}`
	if got != want {
		t.Fatalf("data JSON\n got: %s\nwant: %s", got, want)
	}
}

func TestWagerTransactionPendingReferenceJSON(t *testing.T) {
	got := marshalData(t, WagerTransactionPendingReference{
		TransactionID:                  uuid.MustParse("0192f298-345e-7e38-af88-e43f851a819d"),
		WalletID:                       uuid.MustParse("0192f291-27dd-7d3f-8071-5f8685deef37"),
		ProviderID:                     "provider-a",
		ExternalTransactionID:          "tx-3",
		Kind:                           "ROLLBACK",
		ReferenceExternalTransactionID: "tx-2",
		Attempts:                       1,
		NextAttemptAt:                  FormatTime(time.Date(2026, 9, 8, 12, 0, 2, 0, time.UTC)),
	})
	want := `{"transactionId":"0192f298-345e-7e38-af88-e43f851a819d","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37",` +
		`"providerId":"provider-a","externalTransactionId":"tx-3","kind":"ROLLBACK",` +
		`"referenceExternalTransactionId":"tx-2","attempts":1,"nextAttemptAt":"2026-09-08T12:00:02.000Z"}`
	if got != want {
		t.Fatalf("data JSON\n got: %s\nwant: %s", got, want)
	}
}
