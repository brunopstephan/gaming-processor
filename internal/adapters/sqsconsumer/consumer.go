// Package sqsconsumer consumes wager operations from the input FIFO queue.
// Each message is decoded strictly, its sender is mapped to the provider it
// represents, and the operation goes through the same use case as HTTP with
// the inbox entry in the same SQL transaction. A message is deleted only after
// that transaction committed; invalid or refused messages go to the DLQ with a
// failureReason; transient failures are retried with backoff and, once
// exhausted, redriven to the DLQ by SQS.
package sqsconsumer

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
)

// API is the part of the SQS client the consumer uses.
type API interface {
	ReceiveMessage(ctx context.Context, in *sqs.ReceiveMessageInput, opts ...func(*sqs.Options)) (*sqs.ReceiveMessageOutput, error)
	DeleteMessage(ctx context.Context, in *sqs.DeleteMessageInput, opts ...func(*sqs.Options)) (*sqs.DeleteMessageOutput, error)
	ChangeMessageVisibility(ctx context.Context, in *sqs.ChangeMessageVisibilityInput, opts ...func(*sqs.Options)) (*sqs.ChangeMessageVisibilityOutput, error)
	SendMessage(ctx context.Context, in *sqs.SendMessageInput, opts ...func(*sqs.Options)) (*sqs.SendMessageOutput, error)
}

// Intake is the use case behind the consumer (app.IntakeService).
type Intake interface {
	Handle(ctx context.Context, messageID string, cmd wagering.Command, meta app.Meta) (app.IntakeResult, error)
}

// Metrics are the consumer's own measurements.
type Metrics interface {
	SQSRetry()
	SQSDeadLetter(reason string)
}

// Settings configure the consumer. Queue URLs are known after start.
type Settings struct {
	InputURL        string
	DLQURL          string
	Workers         int
	WaitTime        time.Duration
	MaxMessages     int
	RetryBaseDelay  time.Duration
	RetryMaxDelay   time.Duration
	ShutdownTimeout time.Duration
	// SenderProviders maps the SenderId attribute to the providerId it represents.
	SenderProviders map[string]string
}

// Consumer handles input messages.
type Consumer struct {
	api     API
	intake  Intake
	metrics Metrics
	log     *slog.Logger
	s       Settings

	// lifecycle (runner.go)
	mu          sync.Mutex
	stopPolling context.CancelFunc
	abortWork   context.CancelFunc
	pollCtx     context.Context
	workCtx     context.Context
	done        chan struct{}
}

// New builds a Consumer.
func New(api API, intake Intake, metrics Metrics, log *slog.Logger, s Settings) *Consumer {
	return &Consumer{api: api, intake: intake, metrics: metrics, log: log, s: s}
}

const (
	attrSenderID     = "SenderId"
	attrReceiveCount = "ApproximateReceiveCount"
	attrGroupID      = "MessageGroupId"
	settleTimeout    = 5 * time.Second
)

type action int

const (
	actDelete action = iota
	actDeadLetter
	actRetry
)

type decision struct {
	action action
	reason string
}

// HandleMessage handles one received message and settles it with SQS.
func (c *Consumer) HandleMessage(ctx context.Context, msg types.Message) {
	c.settle(ctx, msg, c.decide(ctx, msg))
}

func (c *Consumer) decide(ctx context.Context, msg types.Message) decision {
	log := c.log.With("sqsMessageId", aws.ToString(msg.MessageId))
	provider, known := c.s.SenderProviders[msg.Attributes[attrSenderID]]
	if !known {
		log.WarnContext(ctx, "message from an unknown sender", "senderId", msg.Attributes[attrSenderID])
		return decision{actDeadLetter, string(wagering.FailureProviderIdentityMismatch)}
	}
	req, err := parseRequest(aws.ToString(msg.Body))
	if err != nil {
		return decision{actDeadLetter, inputCode(err)}
	}
	corr := req.CorrelationID
	if corr == "" {
		corr = req.MessageID
	}
	log = log.With("messageId", req.MessageID, "correlationId", corr, "providerId", provider, "walletId", req.Raw.WalletID)
	if req.Raw.ProviderID != provider {
		log.WarnContext(ctx, "providerId does not match the sender", "claimedProviderId", req.Raw.ProviderID)
		return decision{actDeadLetter, string(wagering.FailureProviderIdentityMismatch)}
	}
	cmd, err := wagering.ParseCommand(req.Raw)
	if err != nil {
		return decision{actDeadLetter, inputCode(err)}
	}
	res, err := c.intake.Handle(ctx, req.MessageID, cmd, app.Meta{CorrelationID: corr})
	switch {
	case errors.Is(err, app.ErrIdempotencyKeyConflict):
		return decision{actDeadLetter, "IDEMPOTENCY_KEY_CONFLICT"}
	case errors.Is(err, app.ErrExternalTransactionConflict):
		return decision{actDeadLetter, "EXTERNAL_TRANSACTION_CONFLICT"}
	case err != nil:
		log.WarnContext(ctx, "message will be retried", "error", err.Error(), "transient", errors.Is(err, app.ErrTransient))
		return decision{action: actRetry}
	}
	switch {
	case res.Outcome == app.IntakeDuplicate:
		if res.Transaction != nil && res.Transaction.Status() == wagering.StatusFailed {
			// A redelivery after a FAILED commit whose dead-lettering did not complete.
			return decision{actDeadLetter, string(wagering.FailureInfrastructure)}
		}
		log.InfoContext(ctx, "duplicate message dropped")
		return decision{action: actDelete}
	case res.Outcome == app.IntakeMessageReused:
		return decision{actDeadLetter, string(wagering.FailureMessageIDReused)}
	case res.Transaction.Status() == wagering.StatusFailed:
		return decision{actDeadLetter, string(wagering.FailureInfrastructure)}
	}
	return decision{action: actDelete}
}

func inputCode(err error) string {
	var in *wagering.InputError
	if errors.As(err, &in) {
		return string(in.Code())
	}
	return string(wagering.FailureMalformedPayload)
}

// settle applies d. It runs even if ctx was canceled after the commit, so a
// committed message is still deleted; a retry of canceled work releases the
// message at once instead of backing off.
func (c *Consumer) settle(ctx context.Context, msg types.Message, d decision) {
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), settleTimeout)
	defer cancel()
	switch d.action {
	case actDeadLetter:
		if err := c.deadLetter(sctx, msg, d.reason); err != nil {
			c.log.ErrorContext(sctx, "dead-letter failed; message will be retried",
				"sqsMessageId", aws.ToString(msg.MessageId), "reason", d.reason, "error", err.Error())
			c.retry(sctx, msg, c.backoff(msg))
			return
		}
		c.metrics.SQSDeadLetter(d.reason)
		c.delete(sctx, msg)
	case actRetry:
		delay := c.backoff(msg)
		if ctx.Err() != nil {
			delay = 0
		}
		c.retry(sctx, msg, delay)
	default:
		c.delete(sctx, msg)
	}
}

func (c *Consumer) deadLetter(ctx context.Context, msg types.Message, reason string) error {
	group := msg.Attributes[attrGroupID]
	if group == "" {
		group = "unknown"
	}
	_, err := c.api.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl: aws.String(c.s.DLQURL), MessageBody: msg.Body,
		MessageGroupId: aws.String(group), MessageDeduplicationId: msg.MessageId,
		MessageAttributes: map[string]types.MessageAttributeValue{
			"failureReason": {DataType: aws.String("String"), StringValue: aws.String(reason)},
		},
	})
	return err
}

func (c *Consumer) delete(ctx context.Context, msg types.Message) {
	if _, err := c.api.DeleteMessage(ctx, &sqs.DeleteMessageInput{
		QueueUrl: aws.String(c.s.InputURL), ReceiptHandle: msg.ReceiptHandle,
	}); err != nil {
		c.log.WarnContext(ctx, "delete failed; the redelivery will be dropped by the inbox",
			"sqsMessageId", aws.ToString(msg.MessageId), "error", err.Error())
	}
}

// retry leaves msg for redelivery after delay.
func (c *Consumer) retry(ctx context.Context, msg types.Message, delay time.Duration) {
	c.metrics.SQSRetry()
	if _, err := c.api.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
		QueueUrl: aws.String(c.s.InputURL), ReceiptHandle: msg.ReceiptHandle,
		VisibilityTimeout: int32(delay / time.Second),
	}); err != nil {
		c.log.WarnContext(ctx, "visibility change failed; the queue's visibility timeout applies",
			"sqsMessageId", aws.ToString(msg.MessageId), "error", err.Error())
	}
}

func (c *Consumer) backoff(msg types.Message) time.Duration {
	n, err := strconv.Atoi(msg.Attributes[attrReceiveCount])
	if err != nil || n < 1 {
		n = 1
	}
	return retryDelay(n, c.s.RetryBaseDelay, c.s.RetryMaxDelay)
}

// retryDelay is base×2^(n−1) for the n-th receive, capped at maxDelay.
func retryDelay(n int, base, maxDelay time.Duration) time.Duration {
	d := base
	for i := 1; i < n && d < maxDelay; i++ {
		d *= 2
	}
	return min(d, maxDelay)
}
