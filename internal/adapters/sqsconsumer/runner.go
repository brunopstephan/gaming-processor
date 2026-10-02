package sqsconsumer

import (
	"context"
	"errors"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// abortGrace bounds the wait for workers after their work was canceled.
const abortGrace = 5 * time.Second

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
		for i, msg := range out.Messages {
			if c.pollCtx.Err() != nil {
				c.release(out.Messages[i:])
				break
			}
			c.HandleMessage(c.workCtx, msg)
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
	select {
	case <-done:
		return nil
	case <-time.After(abortGrace):
		return errors.New("sqsconsumer: workers did not stop after abort")
	}
}
