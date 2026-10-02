//go:build !unit && !e2e

package awssqs

import (
	"context"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

func TestPublisherSendsEventToFIFOQueue(t *testing.T) {
	q := sqstest.NewQueues(t, sqstest.Options{})
	owner := sqstest.Owner(t)
	pub := NewPublisher(&Clients{Publisher: owner}, &Queues{Events: q.Events.URL})
	e := app.OutboxEvent{ID: uuid.New(), AggregateID: uuid.New(), EventType: "WagerTransactionProcessed",
		Payload: []byte(`{"eventId":"x"}`), OccurredAt: time.Now()}
	for range 2 { // the second publish is a republication: deduplicated by eventId
		if err := pub.Publish(context.Background(), e); err != nil {
			t.Fatal(err)
		}
	}
	msgs := sqstest.Receive(t, owner, q.Events.URL, 5*time.Second)
	if len(msgs) != 1 {
		t.Fatalf("received %d messages, want 1 (deduplicated)", len(msgs))
	}
	m := msgs[0]
	if aws.ToString(m.Body) != string(e.Payload) || m.Attributes["MessageGroupId"] != e.AggregateID.String() ||
		m.Attributes["MessageDeduplicationId"] != e.ID.String() ||
		aws.ToString(m.MessageAttributes["eventType"].StringValue) != e.EventType {
		t.Fatalf("message = %+v", m)
	}
}
