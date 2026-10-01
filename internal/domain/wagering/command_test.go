//go:build !integration && !e2e

package wagering

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func validRaw() RawCommand {
	return RawCommand{
		ProviderID:            "provider-a",
		ExternalTransactionID: "transaction-123",
		IdempotencyKey:        "provider-a:transaction-123",
		PlayerID:              "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
		WalletID:              "0192f291-27dd-7d3f-8071-5f8685deef37",
		RoundID:               "round-987",
		GameID:                "fortune-chimp",
		Kind:                  "BET",
		Amount:                "25.00",
		Currency:              "BRL",
	}
}

func TestParseCommandValid(t *testing.T) {
	cmd, err := ParseCommand(validRaw())
	if err != nil {
		t.Fatal(err)
	}
	if cmd.Kind != KindBet || cmd.Money.Amount() != "25.00" || cmd.PlayerID.String() != validRaw().PlayerID {
		t.Fatalf("cmd = %+v", cmd)
	}
}

func TestParseCommandViolations(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(r *RawCommand)
		code   FailureCode
		field  string
	}{
		{"missing provider", func(r *RawCommand) { r.ProviderID = "" }, FailureMissingField, "providerId"},
		{"missing external id", func(r *RawCommand) { r.ExternalTransactionID = "" }, FailureMissingField, "externalTransactionId"},
		{"missing round", func(r *RawCommand) { r.RoundID = "" }, FailureMissingField, "roundId"},
		{"missing game", func(r *RawCommand) { r.GameID = "" }, FailureMissingField, "gameId"},
		{"too long game", func(r *RawCommand) { r.GameID = strings.Repeat("g", 256) }, FailureInvalidField, "gameId"},
		{"missing key", func(r *RawCommand) { r.IdempotencyKey = "" }, FailureMissingIdempotencyKey, "idempotencyKey"},
		{"too long key", func(r *RawCommand) { r.IdempotencyKey = strings.Repeat("k", 256) }, FailureInvalidIdempotencyKey, "idempotencyKey"},
		{"missing player", func(r *RawCommand) { r.PlayerID = "" }, FailureMissingField, "playerId"},
		{"uppercase uuid", func(r *RawCommand) { r.PlayerID = strings.ToUpper(r.PlayerID) }, FailureInvalidID, "playerId"},
		{"braced uuid", func(r *RawCommand) { r.WalletID = "{" + r.WalletID + "}" }, FailureInvalidID, "walletId"},
		{"nil uuid", func(r *RawCommand) { r.WalletID = "00000000-0000-0000-0000-000000000000" }, FailureInvalidID, "walletId"},
		{"garbage uuid", func(r *RawCommand) { r.WalletID = "wallet-1" }, FailureInvalidID, "walletId"},
		{"unknown kind", func(r *RawCommand) { r.Kind = "JACKPOT" }, FailureUnknownKind, "kind"},
		{"lowercase kind", func(r *RawCommand) { r.Kind = "bet" }, FailureUnknownKind, "kind"},
		{"opening", func(r *RawCommand) { r.Kind = "OPENING" }, FailureKindNotAllowed, "kind"},
		{"missing kind", func(r *RawCommand) { r.Kind = "" }, FailureMissingField, "kind"},
		{"missing amount", func(r *RawCommand) { r.Amount = "" }, FailureMissingField, "money.amount"},
		{"missing currency", func(r *RawCommand) { r.Currency = "" }, FailureMissingField, "money.currency"},
		{"bad amount", func(r *RawCommand) { r.Amount = "25.0" }, FailureInvalidMoney, "money.amount"},
		{"negative amount", func(r *RawCommand) { r.Amount = "-25.00" }, FailureInvalidMoney, "money.amount"},
		{"bad currency", func(r *RawCommand) { r.Currency = "JPY" }, FailureUnsupportedCurrency, "money.currency"},
		{"zero bet", func(r *RawCommand) { r.Amount = "0.00" }, FailureInvalidAmountForKind, "money.amount"},
		{"zero win", func(r *RawCommand) { r.Kind, r.Amount = "WIN", "0.00" }, FailureInvalidAmountForKind, "money.amount"},
		{"zero refund", func(r *RawCommand) {
			r.Kind, r.Amount, r.ReferenceExternalTransactionID = "REFUND", "0.00", "tx-0"
		}, FailureInvalidAmountForKind, "money.amount"},
		{"zero rollback", func(r *RawCommand) {
			r.Kind, r.Amount, r.ReferenceExternalTransactionID = "ROLLBACK", "0.00", "tx-0"
		}, FailureInvalidAmountForKind, "money.amount"},
		{"positive loss", func(r *RawCommand) { r.Kind, r.Amount = "LOSS", "1.00" }, FailureInvalidAmountForKind, "money.amount"},
		{"refund without ref", func(r *RawCommand) { r.Kind = "REFUND" }, FailureMissingReference, "referenceExternalTransactionId"},
		{"rollback without ref", func(r *RawCommand) { r.Kind = "ROLLBACK" }, FailureMissingReference, "referenceExternalTransactionId"},
		{"bet with ref", func(r *RawCommand) { r.ReferenceExternalTransactionID = "tx-0" }, FailureReferenceNotAllowed, "referenceExternalTransactionId"},
		{"loss with ref", func(r *RawCommand) {
			r.Kind, r.Amount, r.ReferenceExternalTransactionID = "LOSS", "0.00", "tx-0"
		}, FailureReferenceNotAllowed, "referenceExternalTransactionId"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := validRaw()
			tt.mutate(&raw)
			_, err := ParseCommand(raw)
			var ie *InputError
			if !errors.As(err, &ie) {
				t.Fatalf("error = %v, want *InputError", err)
			}
			for _, v := range ie.Violations {
				if v.Code == tt.code && v.Field == tt.field {
					return
				}
			}
			t.Fatalf("violations %+v do not contain %s on %s", ie.Violations, tt.code, tt.field)
		})
	}
}

func TestParseCommandZeroPolicy(t *testing.T) {
	loss := validRaw()
	loss.Kind, loss.Amount = "LOSS", "0.00"
	if _, err := ParseCommand(loss); err != nil {
		t.Fatalf("LOSS 0.00 must be accepted: %v", err)
	}
	win := validRaw()
	win.Kind, win.ReferenceExternalTransactionID = "WIN", "transaction-100"
	if _, err := ParseCommand(win); err != nil {
		t.Fatalf("WIN with optional reference must be accepted: %v", err)
	}
}

func TestPayloadHashCanonicalJSON(t *testing.T) {
	cmd, err := ParseCommand(validRaw())
	if err != nil {
		t.Fatal(err)
	}
	canonical := `{"externalTransactionId":"transaction-123","gameId":"fortune-chimp","kind":"BET",` +
		`"money":{"amount":"25.00","currency":"BRL"},"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",` +
		`"providerId":"provider-a","roundId":"round-987","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37"}`
	sum := sha256.Sum256([]byte(canonical))
	if got, want := cmd.PayloadHash(), hex.EncodeToString(sum[:]); got != want {
		t.Fatalf("PayloadHash() = %s, want sha256(%s) = %s", got, canonical, want)
	}
}

func TestPayloadHashFields(t *testing.T) {
	base, _ := ParseCommand(validRaw())

	otherKey := validRaw()
	otherKey.IdempotencyKey = "something-else"
	k, _ := ParseCommand(otherKey)
	if k.PayloadHash() != base.PayloadHash() {
		t.Fatal("idempotency key must not affect the hash")
	}

	otherAmount := validRaw()
	otherAmount.Amount = "25.01"
	a, _ := ParseCommand(otherAmount)
	if a.PayloadHash() == base.PayloadHash() {
		t.Fatal("amount must affect the hash")
	}

	withRef := validRaw()
	withRef.Kind, withRef.ReferenceExternalTransactionID = "WIN", "transaction-100"
	withoutRef := validRaw()
	withoutRef.Kind = "WIN"
	r1, _ := ParseCommand(withRef)
	r2, _ := ParseCommand(withoutRef)
	if r1.PayloadHash() == r2.PayloadHash() {
		t.Fatal("reference must affect the hash when present")
	}

	html := validRaw()
	html.GameID = "a<b>&c"
	h, _ := ParseCommand(html)
	canonical := `{"externalTransactionId":"transaction-123","gameId":"a<b>&c","kind":"BET",` +
		`"money":{"amount":"25.00","currency":"BRL"},"playerId":"0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",` +
		`"providerId":"provider-a","roundId":"round-987","walletId":"0192f291-27dd-7d3f-8071-5f8685deef37"}`
	sum := sha256.Sum256([]byte(canonical))
	if h.PayloadHash() != hex.EncodeToString(sum[:]) {
		t.Fatal("canonical JSON must not HTML-escape")
	}
}
