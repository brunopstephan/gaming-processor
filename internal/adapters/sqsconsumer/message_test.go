//go:build !integration && !e2e

package sqsconsumer

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

const validBody = `{"messageId":"msg-123","type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z",
"data":{"providerId":"provider-a","externalTransactionId":"transaction-123","idempotencyKey":"provider-a:transaction-123",
"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37",
"roundId":"round-987","gameId":"fortune-chimp","kind":"BET","money":{"amount":"25.00","currency":"BRL"}}}`

func TestParseRequestValid(t *testing.T) {
	req, err := parseRequest(validBody)
	if err != nil {
		t.Fatal(err)
	}
	want := wagering.RawCommand{ProviderID: "provider-a", ExternalTransactionID: "transaction-123",
		IdempotencyKey: "provider-a:transaction-123", PlayerID: "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
		WalletID: "0192f291-27dd-7d3f-8071-5f8685deef37", RoundID: "round-987", GameID: "fortune-chimp",
		Kind: "BET", Amount: "25.00", Currency: "BRL"}
	if req.MessageID != "msg-123" || req.CorrelationID != "" || req.Raw != want {
		t.Fatalf("req = %+v", req)
	}
	if _, err := wagering.ParseCommand(req.Raw); err != nil {
		t.Fatalf("the challenge's example must parse: %v", err)
	}
}

func TestParseRequestInvalid(t *testing.T) {
	cases := map[string]struct {
		body string
		code wagering.FailureCode
	}{
		"not json":         {`{`, wagering.FailureMalformedPayload},
		"unknown field":    {strings.Replace(validBody, `"type"`, `"extra":1,"type"`, 1), wagering.FailureMalformedPayload},
		"numeric amount":   {strings.Replace(validBody, `"amount":"25.00"`, `"amount":25.00`, 1), wagering.FailureInvalidMoney},
		"missing id":       {strings.Replace(validBody, `"messageId":"msg-123",`, ``, 1), wagering.FailureMissingField},
		"long id":          {strings.Replace(validBody, `msg-123`, strings.Repeat("m", 256), 1), wagering.FailureInvalidField},
		"wrong type":       {strings.Replace(validBody, `WagerTransactionRequested`, `Other`, 1), wagering.FailureInvalidField},
		"missing type":     {strings.Replace(validBody, `"type":"WagerTransactionRequested",`, ``, 1), wagering.FailureMissingField},
		"bad occurredAt":   {strings.Replace(validBody, `2026-09-08T12:00:00.000Z`, `yesterday`, 1), wagering.FailureInvalidField},
		"missing occurred": {strings.Replace(validBody, `"occurredAt":"2026-09-08T12:00:00.000Z",`, ``, 1), wagering.FailureMissingField},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := parseRequest(tc.body)
			var in *wagering.InputError
			if !errors.As(err, &in) || in.Code() != tc.code {
				t.Fatalf("error = %v, want %s", err, tc.code)
			}
		})
	}
}

func TestRetryDelay(t *testing.T) {
	base, maxDelay := 2*time.Second, 10*time.Second
	for n, want := range map[int]time.Duration{1: 2 * time.Second, 2: 4 * time.Second, 3: 8 * time.Second, 4: 10 * time.Second, 9: 10 * time.Second} {
		if got := retryDelay(n, base, maxDelay); got != want {
			t.Errorf("retryDelay(%d) = %s, want %s", n, got, want)
		}
	}
}
