//go:build !integration && !e2e

package sqsconsumer

import (
	"context"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// recordingAPI records the settlement calls the consumer makes, in order.
type recordingAPI struct {
	mu  sync.Mutex
	ops []string
}

func (r *recordingAPI) record(op string) { r.mu.Lock(); r.ops = append(r.ops, op); r.mu.Unlock() }

func (r *recordingAPI) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.ops...)
}

func (r *recordingAPI) ReceiveMessage(context.Context, *sqs.ReceiveMessageInput, ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error) {
	return nil, fmt.Errorf("unused")
}

func (r *recordingAPI) DeleteMessage(_ context.Context, in *sqs.DeleteMessageInput, _ ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error) {
	r.record("delete " + aws.ToString(in.ReceiptHandle))
	return &sqs.DeleteMessageOutput{}, nil
}

func (r *recordingAPI) ChangeMessageVisibility(_ context.Context, in *sqs.ChangeMessageVisibilityInput, _ ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error) {
	r.record(fmt.Sprintf("visibility %s %d", aws.ToString(in.ReceiptHandle), in.VisibilityTimeout))
	return &sqs.ChangeMessageVisibilityOutput{}, nil
}

func (r *recordingAPI) SendMessage(_ context.Context, in *sqs.SendMessageInput, _ ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	r.record("send " + aws.ToString(in.MessageDeduplicationId))
	return &sqs.SendMessageOutput{}, nil
}

type noMetrics struct{}

func (noMetrics) SQSRetry()            {}
func (noMetrics) SQSDeadLetter(string) {}

// scriptedIntake answers per messageId and records the order of calls.
type scriptedIntake struct {
	mu     sync.Mutex
	calls  []string
	result func(messageID string) (app.IntakeResult, error)
}

func (s *scriptedIntake) Handle(_ context.Context, messageID string, _ wagering.Command, _ app.Meta) (app.IntakeResult, error) {
	s.mu.Lock()
	s.calls = append(s.calls, messageID)
	s.mu.Unlock()
	return s.result(messageID)
}

func unitMessage(id, group string) types.Message {
	body := fmt.Sprintf(`{"messageId":%q,"type":"WagerTransactionRequested","occurredAt":"2026-09-08T12:00:00.000Z",`+
		`"data":{"providerId":"provider-a","externalTransactionId":"ext-%s","idempotencyKey":"provider-a:ext-%s",`+
		`"playerId":"00000000-0000-4000-8000-000000000001","walletId":"00000000-0000-4000-8000-000000000002",`+
		`"roundId":"round-1","gameId":"game-1","kind":"BET","money":{"amount":"1.00","currency":"BRL"}}}`, id, id, id)
	return types.Message{
		MessageId: aws.String("sqs-" + id), ReceiptHandle: aws.String("rh-" + id), Body: aws.String(body),
		Attributes: map[string]string{attrSenderID: "sender-a", attrGroupID: group, attrReceiveCount: "1"},
	}
}

func newUnitConsumer(api API, intake Intake) *Consumer {
	c := New(api, intake, noMetrics{}, slog.New(slog.DiscardHandler), Settings{
		RetryBaseDelay: time.Second, RetryMaxDelay: 4 * time.Second,
		SenderProviders: map[string]string{"sender-a": "provider-a"},
	})
	c.pollCtx, c.stopPolling = context.WithCancel(context.Background())
	c.workCtx, c.abortWork = context.WithCancel(context.Background())
	return c
}

func TestBatchReleasesLaterMessagesOfARetriedGroup(t *testing.T) {
	api := &recordingAPI{}
	intake := &scriptedIntake{result: func(id string) (app.IntakeResult, error) {
		if id == "m1" {
			return app.IntakeResult{}, app.ErrTransient
		}
		return app.IntakeResult{Outcome: app.IntakeDuplicate}, nil
	}}
	c := newUnitConsumer(api, intake)

	c.handleBatch([]types.Message{unitMessage("m1", "g1"), unitMessage("m2", "g1"), unitMessage("m3", "g2")})

	if want := []string{"m1", "m3"}; !reflect.DeepEqual(intake.calls, want) {
		t.Fatalf("processed %v, want %v: m2 must not overtake the retried m1 of its group", intake.calls, want)
	}
	want := []string{"visibility rh-m1 1", "visibility rh-m2 0", "delete rh-m3"}
	if got := api.snapshot(); !reflect.DeepEqual(got, want) {
		t.Fatalf("settlements = %v, want %v", got, want)
	}
}

func TestBatchKeepsProcessingAGroupAfterTerminalSettlement(t *testing.T) {
	api := &recordingAPI{}
	intake := &scriptedIntake{result: func(string) (app.IntakeResult, error) {
		return app.IntakeResult{Outcome: app.IntakeDuplicate}, nil
	}}
	c := newUnitConsumer(api, intake)

	c.handleBatch([]types.Message{unitMessage("m1", "g1"), unitMessage("m2", "g1")})

	if want := []string{"m1", "m2"}; !reflect.DeepEqual(intake.calls, want) {
		t.Fatalf("processed %v, want %v", intake.calls, want)
	}
}

type panickingIntake struct{}

func (panickingIntake) Handle(context.Context, string, wagering.Command, app.Meta) (app.IntakeResult, error) {
	panic("boom")
}

func TestHandleMessageRecoversFromPanicAndRetries(t *testing.T) {
	api := &recordingAPI{}
	c := newUnitConsumer(api, panickingIntake{})

	if c.HandleMessage(context.Background(), unitMessage("m1", "g1")) {
		t.Fatal("a panicked message must be reported as left for retry")
	}
	if want := []string{"visibility rh-m1 1"}; !reflect.DeepEqual(api.snapshot(), want) {
		t.Fatalf("settlements = %v, want %v (no delete, retry with backoff)", api.snapshot(), want)
	}
}

func TestDeadLetterCarriesSenderID(t *testing.T) {
	var sent *sqs.SendMessageInput
	api := &captureSend{recordingAPI: &recordingAPI{}, sent: &sent}
	c := newUnitConsumer(api, &scriptedIntake{})
	msg := unitMessage("m1", "g1")
	msg.Body = aws.String(`{"messageId":`)

	if !c.HandleMessage(context.Background(), msg) {
		t.Fatal("a malformed message is dead-lettered and finished")
	}
	if sent == nil || aws.ToString(sent.MessageAttributes["senderId"].StringValue) != "sender-a" ||
		aws.ToString(sent.MessageAttributes["failureReason"].StringValue) != "MALFORMED_PAYLOAD" {
		t.Fatalf("dlq message = %+v, want senderId and failureReason attributes", sent)
	}
}

type captureSend struct {
	*recordingAPI
	sent **sqs.SendMessageInput
}

func (c *captureSend) SendMessage(ctx context.Context, in *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error) {
	*c.sent = in
	return c.recordingAPI.SendMessage(ctx, in, opts...)
}
