package awssqs

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
)

var _ app.EventPublisher = (*Publisher)(nil)

// Publisher sends outbox events to the events FIFO queue.
// MessageGroupId = aggregateId (walletId) keeps one wallet's events in order
// per publisher; MessageDeduplicationId = eventId drops republications within
// SQS's deduplication window. Consumers order by walletVersion.
type Publisher struct {
	client *sqs.Client
	queues *Queues
}

// NewPublisher builds a Publisher with the publisher identity.
func NewPublisher(c *Clients, q *Queues) *Publisher {
	return &Publisher{client: c.Publisher, queues: q}
}

// Publish implements app.EventPublisher.
func (p *Publisher) Publish(ctx context.Context, e app.OutboxEvent) error {
	_, err := p.client.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               aws.String(p.queues.Events),
		MessageBody:            aws.String(string(e.Payload)),
		MessageGroupId:         aws.String(e.AggregateID.String()),
		MessageDeduplicationId: aws.String(e.ID.String()),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"eventType": {DataType: aws.String("String"), StringValue: aws.String(e.EventType)},
		},
	})
	return err
}
