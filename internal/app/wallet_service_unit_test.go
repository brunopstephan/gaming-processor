//go:build !integration && !e2e

package app

import (
	"errors"
	"testing"

	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

func TestParseOpenWallet(t *testing.T) {
	cmd, err := ParseOpenWallet("0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "1000.00", "BRL")
	if err != nil || cmd.InitialBalance.Amount() != "1000.00" || cmd.PlayerID.String() != "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1" {
		t.Fatalf("ParseOpenWallet = %+v, %v", cmd, err)
	}
	if _, err := ParseOpenWallet("0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "0.00", "BRL"); err != nil {
		t.Fatalf("zero initial balance is allowed: %v", err)
	}

	tests := []struct {
		name, player, amount, currency string
		code                           wagering.FailureCode
		field                          string
	}{
		{"missing player", "", "1.00", "BRL", wagering.FailureMissingField, "playerId"},
		{"uppercase player", "0192F28F-5DC0-7D58-BDB2-814AD6A0F4A1", "1.00", "BRL", wagering.FailureInvalidID, "playerId"},
		{"nil player", "00000000-0000-0000-0000-000000000000", "1.00", "BRL", wagering.FailureInvalidID, "playerId"},
		{"missing amount", "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "", "BRL", wagering.FailureMissingField, "initialBalance.amount"},
		{"missing currency", "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "1.00", "", wagering.FailureMissingField, "initialBalance.currency"},
		{"negative", "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "-1.00", "BRL", wagering.FailureInvalidMoney, "initialBalance.amount"},
		{"bad scale", "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "1.0", "BRL", wagering.FailureInvalidMoney, "initialBalance.amount"},
		{"bad currency", "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1", "1.00", "JPY", wagering.FailureUnsupportedCurrency, "initialBalance.currency"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseOpenWallet(tt.player, tt.amount, tt.currency)
			var ie *wagering.InputError
			if !errors.As(err, &ie) {
				t.Fatalf("error = %v, want *wagering.InputError", err)
			}
			for _, v := range ie.Violations {
				if v.Code == tt.code && v.Field == tt.field {
					return
				}
			}
			t.Fatalf("violations %+v lack %s on %s", ie.Violations, tt.code, tt.field)
		})
	}
}

func TestMetaNormalized(t *testing.T) {
	m := Meta{}.normalized()
	if m.CorrelationID == "" || m.Channel != ChannelHTTP {
		t.Fatalf("normalized = %+v", m)
	}
	kept := Meta{CorrelationID: "c", CausationID: "m", Channel: ChannelSQS}.normalized()
	if kept != (Meta{CorrelationID: "c", CausationID: "m", Channel: ChannelSQS}) {
		t.Fatalf("normalized changed explicit values: %+v", kept)
	}
}
