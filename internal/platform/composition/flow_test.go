//go:build !unit && !e2e

package composition_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/httpapi"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/money"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/composition"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/kctest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/pgtest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

func message(w *wallet.Wallet, kind, amount, ext, ref string) string {
	refField := ""
	if ref != "" {
		refField = fmt.Sprintf(`,"referenceExternalTransactionId":%q`, ref)
	}
	return fmt.Sprintf(`{"messageId":%q,"type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z",`+
		`"data":{"providerId":"provider-a","externalTransactionId":%q,"idempotencyKey":%q,"playerId":%q,"walletId":%q,`+
		`"roundId":"round-1","gameId":"game-1","kind":%q,"money":{"amount":%q,"currency":"BRL"}%s}}`,
		"msg-"+uuid.NewString(), ext, "provider-a:"+ext, w.PlayerID(), w.ID(), kind, amount, refField)
}

func TestSQSToEventsFlow(t *testing.T) {
	q := sqstest.NewQueues(t, sqstest.Options{})
	for k, v := range map[string]string{
		"DATABASE_URL": pgtest.FreshDatabase(t), "OIDC_ISSUER_URL": kctest.IssuerURL(), "HTTP_ADDR": "127.0.0.1:0",
		"LOG_LEVEL": "error", "AWS_ACCESS_KEY_ID": "test", "AWS_SECRET_ACCESS_KEY": "test", "SQS_ENDPOINT": sqstest.Endpoint(),
		"SQS_INPUT_QUEUE": q.Input.Name, "SQS_INPUT_DLQ": q.InputDLQ.Name, "SQS_EVENTS_QUEUE": q.Events.Name,
		"SQS_WAIT_TIME": "1s", "OUTBOX_POLL_INTERVAL": "100ms", "REFWORKER_POLL_INTERVAL": "100ms",
		"REFERENCE_RETRY_BASE_DELAY": "200ms", "REFERENCE_RETRY_MAX_DELAY": "200ms",
	} {
		t.Setenv(k, v)
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	var (
		wallets *app.WalletService
		queries *app.QueryService
		server  *httpapi.Server
	)
	fxApp := fxtest.New(t, fx.NopLogger, composition.Modules(cfg), fx.Populate(&wallets, &queries, &server))
	fxApp.RequireStart()
	stopped := false
	defer func() {
		if !stopped {
			fxApp.RequireStop()
		}
	}()

	amount, _ := money.Parse("100.00", "BRL")
	w, err := wallets.Open(context.Background(), app.OpenWalletCommand{PlayerID: uuid.New(), InitialBalance: amount}, app.Meta{})
	if err != nil {
		t.Fatal(err)
	}
	providerA := sqstest.Client(t, sqstest.ProviderAAccount)
	betExt := uuid.NewString()
	sqstest.Send(t, providerA, q.Input.URL, message(w, "REFUND", "25.00", uuid.NewString(), betExt), w.ID().String())
	sqstest.Send(t, providerA, q.Input.URL, message(w, "BET", "25.00", betExt, ""), w.ID().String())

	types := map[string]int{}
	owner := sqstest.Owner(t)
	for deadline := time.Now().Add(30 * time.Second); types["WalletBalanceChanged"] < 3 && time.Now().Before(deadline); {
		for _, m := range sqstest.Receive(t, owner, q.Events.URL, 2*time.Second) {
			var env struct {
				EventType string `json:"eventType"`
			}
			_ = json.Unmarshal([]byte(aws.ToString(m.Body)), &env)
			types[env.EventType]++
			sqstest.Delete(t, owner, q.Events.URL, m)
		}
	}
	// OPENING, BET and REFUND each change the balance; the REFUND waited first.
	if types["WalletBalanceChanged"] != 3 || types["WagerTransactionPendingReference"] != 1 || types["WagerTransactionProcessed"] != 3 {
		t.Fatalf("published event types = %v", types)
	}
	got, err := queries.Wallet(context.Background(), w.ID())
	if err != nil || got.Balance().Amount() != "100.00" {
		t.Fatalf("balance = %v %v, want the bet refunded by the worker", got.Balance().Amount(), err)
	}

	resp, err := http.Get("http://" + server.Addr() + "/health/ready")
	if err != nil {
		t.Fatal(err)
	}
	var ready struct {
		Checks map[string]string `json:"checks"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&ready)
	resp.Body.Close()
	if resp.StatusCode != 200 || ready.Checks["sqs"] != "UP" || ready.Checks["postgres"] != "UP" {
		t.Fatalf("ready = %d %v", resp.StatusCode, ready.Checks)
	}

	start := time.Now()
	fxApp.RequireStop()
	stopped = true
	if time.Since(start) > 15*time.Second {
		t.Fatalf("stop took %s", time.Since(start))
	}
}
