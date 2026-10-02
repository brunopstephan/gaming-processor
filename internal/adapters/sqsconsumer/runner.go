package sqsconsumer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// AbortGrace bounds the wait for workers after their work was canceled: an
// in-flight message may take settleTimeout to settle and the poll loop then
// releases the rest of its batch with another settleTimeout.
const AbortGrace = 2*settleTimeout + 2*time.Second

// Start launches Settings.Workers goroutines; each long-polls the input
// queue and handles its batch one message at a time.
func (c *Consumer) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pollCtx, c.stopPolling = context.WithCancel(context.Background())
	c.workCtx, c.abortWork = context.WithCancel(context.Background())
	c.done = make(chan struct{})
	finished := make(chan struct{}, c.s.Workers)
	for range c.s.Workers {
		go func() {
			defer func() { finished <- struct{}{} }()
			c.poll()
		}()
	}
	go func() {
		for range c.s.Workers {
			<-finished
		}
		close(c.done)
	}()
}

func (c *Consumer) poll() {
	for c.pollCtx.Err() == nil {
		out, err := c.api.ReceiveMessage(c.pollCtx, &sqs.ReceiveMessageInput{
			QueueUrl:                    aws.String(c.s.InputURL),
			MaxNumberOfMessages:         int32(c.s.MaxMessages),
			WaitTimeSeconds:             int32(c.s.WaitTime / time.Second),
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameAll},
		})
		if err != nil {
			if c.pollCtx.Err() != nil {
				return
			}
			c.log.Warn("receive failed", "error", err.Error())
			select {
			case <-c.pollCtx.Done():
				return
			case <-time.After(time.Second):
			}
			continue
		}
		c.handleBatch(out.Messages)
	}
}

// handleBatch handles msgs in order. Once a message of a FIFO group is left
// for retry, later messages of that group in the batch are released unprocessed
// so they cannot overtake it.
func (c *Consumer) handleBatch(msgs []types.Message) {
	blocked := map[string]bool{}
	for i, msg := range msgs {
		if c.pollCtx.Err() != nil {
			c.release(msgs[i:])
			return
		}
		group := msg.Attributes[attrGroupID]
		if group != "" && blocked[group] {
			c.release(msgs[i : i+1])
			continue
		}
		if !c.HandleMessage(c.workCtx, msg) && group != "" {
			blocked[group] = true
		}
	}
}

// release makes received but unstarted messages visible again at once.
func (c *Consumer) release(msgs []types.Message) {
	ctx, cancel := context.WithTimeout(context.Background(), settleTimeout)
	defer cancel()
	for _, msg := range msgs {
		if _, err := c.api.ChangeMessageVisibility(ctx, &sqs.ChangeMessageVisibilityInput{
			QueueUrl: aws.String(c.s.InputURL), ReceiptHandle: msg.ReceiptHandle, VisibilityTimeout: 0,
		}); err != nil {
			c.log.Warn("release failed; the visibility timeout applies", "sqsMessageId", aws.ToString(msg.MessageId), "error", err.Error())
		}
	}
}

// Stop stops polling and waits for in-flight messages up to
// Settings.ShutdownTimeout (or ctx). When time runs out it cancels the
// in-flight work: transactions roll back and those messages are released.
func (c *Consumer) Stop(ctx context.Context) error {
	c.mu.Lock()
	stopPolling, abortWork, done := c.stopPolling, c.abortWork, c.done
	c.mu.Unlock()
	if done == nil {
		return nil
	}
	stopPolling()
	wait, cancel := context.WithTimeout(ctx, c.s.ShutdownTimeout)
	defer cancel()
	select {
	case <-done:
		abortWork()
		return nil
	case <-wait.Done():
	}
	c.log.Warn("shutdown timeout reached; aborting in-flight messages")
	abortWork()
	grace := time.NewTimer(AbortGrace)
	defer grace.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("sqsconsumer: workers did not stop after abort: %w", ctx.Err())
	case <-grace.C:
		return errors.New("sqsconsumer: workers did not stop after abort")
	}
}
