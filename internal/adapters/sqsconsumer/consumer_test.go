//go:build !unit && !e2e

package sqsconsumer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/postgres"
	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

type fakeMetrics struct {
	mu      sync.Mutex
	retries int
	dlq     map[string]int
}

func (m *fakeMetrics) SQSRetry() { m.mu.Lock(); m.retries++; m.mu.Unlock() }
func (m *fakeMetrics) SQSDeadLetter(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dlq == nil {
		m.dlq = map[string]int{}
	}
	m.dlq[reason]++
}

type fixture struct {
	h       *apptest.Harness
	q       sqstest.Queues
	owner   *sqs.Client
	metrics *fakeMetrics
	c       *Consumer
}

func newFixture(t *testing.T, deps app.Deps, h *apptest.Harness) *fixture {
	t.Helper()
	ws, err := app.NewWageringService(deps, wagering.DefaultRetryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	q := sqstest.NewQueues(t, sqstest.Options{})
	f := &fixture{h: h, q: q, owner: sqstest.Owner(t), metrics: &fakeMetrics{}}
	f.c = New(f.owner, app.NewIntakeService(deps, h.Inbox, ws), f.metrics, slog.New(slog.DiscardHandler), Settings{
		InputURL: q.Input.URL, DLQURL: q.InputDLQ.URL, RetryBaseDelay: time.Second, RetryMaxDelay: 4 * time.Second,
		SenderProviders: map[string]string{sqstest.ProviderAAccount: "provider-a", sqstest.ProviderBAccount: "provider-b"},
	})
	return f
}

func body(msgID, provider string, w *wallet.Wallet, kind, amount, ext, ref string) string {
	refField := ""
	if ref != "" {
		refField = fmt.Sprintf(`,"referenceExternalTransactionId":%q`, ref)
	}
	return fmt.Sprintf(`{"messageId":%q,"type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z",`+
		`"data":{"providerId":%q,"externalTransactionId":%q,"idempotencyKey":%q,"playerId":%q,"walletId":%q,`+
		`"roundId":"round-1","gameId":"game-1","kind":%q,"money":{"amount":%q,"currency":"BRL"}%s}}`,
		msgID, provider, ext, provider+":"+ext, w.PlayerID(), w.ID(), kind, amount, refField)
}

// deliver sends body as sender and returns it as the consumer receives it.
func (f *fixture) deliver(t *testing.T, sender *sqs.Client, body string) types.Message {
	t.Helper()
	sqstest.Send(t, sender, f.q.Input.URL, body, "group-1")
	msgs := sqstest.Receive(t, f.owner, f.q.Input.URL, 10*time.Second)
	if len(msgs) != 1 {
		t.Fatalf("received %d messages, want 1", len(msgs))
	}
	return msgs[0]
}

// inputEmpty asserts the input queue holds no message, visible or in flight:
// a received but undeleted message is invisible, so only the queue counters
// can tell a delete from a pending visibility timeout.
func (f *fixture) inputEmpty(t *testing.T) {
	t.Helper()
	var visible, inFlight string
	for deadline := time.Now().Add(3 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		out, err := f.owner.GetQueueAttributes(context.Background(), &sqs.GetQueueAttributesInput{
			QueueUrl: aws.String(f.q.Input.URL),
			AttributeNames: []types.QueueAttributeName{
				types.QueueAttributeNameApproximateNumberOfMessages,
				types.QueueAttributeNameApproximateNumberOfMessagesNotVisible,
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		visible = out.Attributes[string(types.QueueAttributeNameApproximateNumberOfMessages)]
		inFlight = out.Attributes[string(types.QueueAttributeNameApproximateNumberOfMessagesNotVisible)]
		if visible == "0" && inFlight == "0" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("input queue has %s visible and %s in flight messages, want none", visible, inFlight)
		}
	}
}

func (f *fixture) balance(t *testing.T, w *wallet.Wallet) string {
	t.Helper()
	got, err := f.h.Wallets.Get(context.Background(), w.ID())
	if err != nil {
		t.Fatal(err)
	}
	return got.Balance().Amount()
}

func TestHandleProcessesAndDeletes(t *testing.T) {
	h := apptest.New(t)
	f := newFixture(t, h.Deps, h)
	w := h.OpenWallet(t, "100.00")
	msgID := "msg-" + uuid.NewString()
	msg := f.deliver(t, sqstest.Client(t, sqstest.ProviderAAccount), body(msgID, "provider-a", w, "BET", "25.00", uuid.NewString(), ""))

	f.c.HandleMessage(context.Background(), msg)

	f.inputEmpty(t)
	if f.balance(t, w) != "75.00" {
		t.Fatalf("balance = %s", f.balance(t, w))
	}
	if _, err := h.Inbox.Get(context.Background(), app.ConsumerWagerTransactions, msgID); err != nil {
		t.Fatalf("inbox: %v", err)
	}
}

// deleteFails simulates a crash after commit: the delete never reaches SQS.
type deleteFails struct{ API }

func (d deleteFails) DeleteMessage(context.Context, *sqs.DeleteMessageInput, ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	return nil, errors.New("connection reset")
}

func TestRedeliveryAfterLostDeleteIsDuplicate(t *testing.T) {
	h := apptest.New(t)
	f := newFixture(t, h.Deps, h)
	w := h.OpenWallet(t, "100.00")
	msg := f.deliver(t, sqstest.Client(t, sqstest.ProviderAAccount), body("msg-"+uuid.NewString(), "provider-a", w, "BET", "25.00", uuid.NewString(), ""))

	crashing := New(deleteFails{f.owner}, f.c.intake, f.metrics, f.c.log, f.c.s)
	crashing.HandleMessage(context.Background(), msg)
	if _, err := f.owner.ChangeMessageVisibility(context.Background(), &sqs.ChangeMessageVisibilityInput{
		QueueUrl: aws.String(f.q.Input.URL), ReceiptHandle: msg.ReceiptHandle, VisibilityTimeout: 0,
	}); err != nil {
		t.Fatal(err)
	}
	again := sqstest.Receive(t, f.owner, f.q.Input.URL, 10*time.Second)
	if len(again) != 1 {
		t.Fatalf("redelivery: got %d messages", len(again))
	}
	f.c.HandleMessage(context.Background(), again[0])

	f.inputEmpty(t)
	if f.balance(t, w) != "75.00" || h.Metrics.Count("inbox_duplicate") != 1 {
		t.Fatalf("balance %s duplicates %d, want one debit and one duplicate", f.balance(t, w), h.Metrics.Count("inbox_duplicate"))
	}
}

// sendFails simulates a DLQ outage.
type sendFails struct{ API }

func (sendFails) SendMessage(context.Context, *sqs.SendMessageInput, ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	return nil, errors.New("dlq unavailable")
}

func TestFailedDeadLetterNeverDeletes(t *testing.T) {
	h := apptest.New(t)
	f := newFixture(t, h.Deps, h)
	w := h.OpenWallet(t, "100.00")
	msg := f.deliver(t, sqstest.Client(t, sqstest.ProviderBAccount), body("msg-"+uuid.NewString(), "provider-a", w, "BET", "5.00", uuid.NewString(), ""))

	broken := New(sendFails{f.owner}, f.c.intake, f.metrics, f.c.log, f.c.s)
	broken.HandleMessage(context.Background(), msg)

	if f.metrics.retries != 1 {
		t.Fatalf("retries = %d, want 1", f.metrics.retries)
	}
	if dead := sqstest.Receive(t, f.owner, f.q.InputDLQ.URL, time.Second); len(dead) != 0 {
		t.Fatalf("dlq has %d messages, want none", len(dead))
	}
	again := sqstest.Receive(t, f.owner, f.q.Input.URL, 10*time.Second)
	if len(again) != 1 || again[0].Attributes["ApproximateReceiveCount"] != "2" {
		t.Fatalf("redelivery = %+v, want the message back with receive count 2", again)
	}
}

// failedDuplicate answers every message as an inbox duplicate whose persisted
// transaction is FAILED: the redelivery after a FAILED commit whose
// dead-lettering did not complete.
type failedDuplicate struct{ tx *wagering.WagerTransaction }

func (d failedDuplicate) Handle(context.Context, string, wagering.Command, app.Meta) (app.IntakeResult, error) {
	return app.IntakeResult{Outcome: app.IntakeDuplicate, Transaction: d.tx}, nil
}

func TestDuplicateOfFailedTransactionIsDeadLettered(t *testing.T) {
	h := apptest.New(t)
	f := newFixture(t, h.Deps, h)
	w := h.OpenWallet(t, "100.00")
	ext := uuid.NewString()
	cmd, err := wagering.ParseCommand(wagering.RawCommand{ProviderID: "provider-a", ExternalTransactionID: ext,
		IdempotencyKey: "provider-a:" + ext, PlayerID: w.PlayerID().String(), WalletID: w.ID().String(),
		RoundID: "round-1", GameID: "game-1", Kind: "BET", Amount: "5.00", Currency: "BRL"})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := wagering.NewExternal(uuid.New(), cmd, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.MarkFailed(time.Now()); err != nil {
		t.Fatal(err)
	}
	c := New(f.owner, failedDuplicate{tx}, f.metrics, f.c.log, f.c.s)
	msg := f.deliver(t, sqstest.Client(t, sqstest.ProviderAAccount), body("msg-"+uuid.NewString(), "provider-a", w, "BET", "5.00", ext, ""))
	if d := c.decide(context.Background(), msg); d.action != actDeadLetter || d.reason != "INFRASTRUCTURE_FAILURE" {
		t.Fatalf("decision = %+v, want dead-letter INFRASTRUCTURE_FAILURE", d)
	}
	c.HandleMessage(context.Background(), msg)
	dead := sqstest.Receive(t, f.owner, f.q.InputDLQ.URL, 10*time.Second)
	if len(dead) != 1 || sqstest.FailureReason(dead[0]) != "INFRASTRUCTURE_FAILURE" {
		t.Fatalf("dlq = %+v", dead)
	}
	f.inputEmpty(t)
}

func TestHandleDeadLetters(t *testing.T) {
	h := apptest.New(t)
	f := newFixture(t, h.Deps, h)
	w := h.OpenWallet(t, "100.00")
	providerA, providerB := sqstest.Client(t, sqstest.ProviderAAccount), sqstest.Client(t, sqstest.ProviderBAccount)
	reusedID, conflictExt := "msg-"+uuid.NewString(), uuid.NewString()
	f.c.HandleMessage(context.Background(), f.deliver(t, providerA, body(reusedID, "provider-a", w, "BET", "1.00", uuid.NewString(), "")))
	f.c.HandleMessage(context.Background(), f.deliver(t, providerA, body("msg-"+uuid.NewString(), "provider-a", w, "BET", "1.00", conflictExt, "")))

	cases := []struct {
		name   string
		sender *sqs.Client
		body   string
		reason string
	}{
		{"unknown sender", sqstest.Owner(t), body("msg-"+uuid.NewString(), "provider-a", w, "BET", "5.00", uuid.NewString(), ""), "PROVIDER_IDENTITY_MISMATCH"},
		{"spoofed provider", providerB, body("msg-"+uuid.NewString(), "provider-a", w, "BET", "5.00", uuid.NewString(), ""), "PROVIDER_IDENTITY_MISMATCH"},
		{"malformed", providerA, `{"messageId":`, "MALFORMED_PAYLOAD"},
		{"numeric amount", providerA, `{"messageId":"m","type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00Z","data":{"money":{"amount":5}}}`, "INVALID_MONEY"},
		{"opening", providerA, body("msg-"+uuid.NewString(), "provider-a", w, "OPENING", "5.00", uuid.NewString(), ""), "KIND_NOT_ALLOWED"},
		{"reused message id", providerA, body(reusedID, "provider-a", w, "BET", "2.00", uuid.NewString(), ""), "MESSAGE_ID_REUSED"},
		{"key conflict", providerA, body("msg-"+uuid.NewString(), "provider-a", w, "BET", "9.00", conflictExt, ""), "IDEMPOTENCY_KEY_CONFLICT"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := f.deliver(t, tc.sender, tc.body)
			f.c.HandleMessage(context.Background(), msg)
			dead := sqstest.Receive(t, f.owner, f.q.InputDLQ.URL, 10*time.Second)
			if len(dead) != 1 || aws.ToString(dead[0].Body) != tc.body || sqstest.FailureReason(dead[0]) != tc.reason {
				t.Fatalf("dlq = %+v, want the original body with failureReason %s", dead, tc.reason)
			}
			sqstest.Delete(t, f.owner, f.q.InputDLQ.URL, dead[0])
			f.inputEmpty(t)
		})
	}
	if f.balance(t, w) != "98.00" {
		t.Fatalf("balance = %s, dead-lettered messages must not move money", f.balance(t, w))
	}
	if f.metrics.dlq["PROVIDER_IDENTITY_MISMATCH"] != 2 || f.metrics.dlq["MESSAGE_ID_REUSED"] != 1 {
		t.Fatalf("dlq metrics = %v", f.metrics.dlq)
	}
}

func TestHandleBusinessOutcomesDelete(t *testing.T) {
	h := apptest.New(t)
	f := newFixture(t, h.Deps, h)
	w := h.OpenWallet(t, "10.00")
	providerA := sqstest.Client(t, sqstest.ProviderAAccount)
	for _, b := range []string{
		body("msg-"+uuid.NewString(), "provider-a", w, "BET", "50.00", uuid.NewString(), ""),                            // REJECTED
		body("msg-"+uuid.NewString(), "provider-a", w, "REFUND", "5.00", uuid.NewString(), "missing-"+uuid.NewString()), // PENDING_REFERENCE
	} {
		f.c.HandleMessage(context.Background(), f.deliver(t, providerA, b))
		f.inputEmpty(t)
	}
	if dead := sqstest.Receive(t, f.owner, f.q.InputDLQ.URL, time.Second); len(dead) != 0 {
		t.Fatal("business outcomes must not be dead-lettered")
	}
}

func TestHandleTransientFailureRetriesLater(t *testing.T) {
	h := apptest.New(t)
	deps := h.Deps
	deps.Tx = postgres.NewTxManager(h.DB, config.Config{Database: config.Database{LockTimeout: 300 * time.Millisecond, StatementTimeout: 5 * time.Second}})
	f := newFixture(t, deps, h)
	w := h.OpenWallet(t, "100.00")

	locked, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- h.Tx.WithinTx(context.Background(), func(ctx context.Context) error {
			if _, err := h.Wallets.GetForUpdate(ctx, w.ID()); err != nil {
				return err
			}
			close(locked)
			<-release
			return nil
		})
	}()
	<-locked

	msg := f.deliver(t, sqstest.Client(t, sqstest.ProviderAAccount), body("msg-"+uuid.NewString(), "provider-a", w, "BET", "25.00", uuid.NewString(), ""))
	f.c.HandleMessage(context.Background(), msg)
	if f.metrics.retries != 1 || f.balance(t, w) != "100.00" {
		t.Fatalf("retries %d balance %s, want a retry and no movement", f.metrics.retries, f.balance(t, w))
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	again := sqstest.Receive(t, f.owner, f.q.Input.URL, 10*time.Second)
	if len(again) != 1 || again[0].Attributes["ApproximateReceiveCount"] != "2" {
		t.Fatalf("redelivery = %+v", again)
	}
	f.c.HandleMessage(context.Background(), again[0])
	f.inputEmpty(t)
	if f.balance(t, w) != "75.00" {
		t.Fatalf("balance = %s after retry", f.balance(t, w))
	}
}

func TestHandleAfterHTTPReplaysAndDeletes(t *testing.T) {
	h := apptest.New(t)
	f := newFixture(t, h.Deps, h)
	w := h.OpenWallet(t, "100.00")
	ext := uuid.NewString()
	cmd, err := wagering.ParseCommand(wagering.RawCommand{ProviderID: "provider-a", ExternalTransactionID: ext,
		IdempotencyKey: "provider-a:" + ext, PlayerID: w.PlayerID().String(), WalletID: w.ID().String(),
		RoundID: "round-1", GameID: "game-1", Kind: "BET", Amount: "40.00", Currency: "BRL"})
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	if _, err := ws.Process(context.Background(), cmd, app.Meta{Channel: app.ChannelHTTP}, nil); err != nil {
		t.Fatal(err)
	}
	f.c.HandleMessage(context.Background(), f.deliver(t, sqstest.Client(t, sqstest.ProviderAAccount),
		body("msg-"+uuid.NewString(), "provider-a", w, "BET", "40.00", ext, "")))
	f.inputEmpty(t)
	if f.balance(t, w) != "60.00" || h.Metrics.Count("replay:sqs") != 1 {
		t.Fatalf("balance %s replays %d, want one debit and an sqs replay", f.balance(t, w), h.Metrics.Count("replay:sqs"))
	}
}
