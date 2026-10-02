//go:build !unit && !e2e

package sqstest

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
)

func queueURL(t *testing.T, c *sqs.Client, name string) string {
	t.Helper()
	out, err := c.GetQueueUrl(context.Background(), &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		t.Fatalf("queue %s not provisioned (%v). %s", name, err, infraHint)
	}
	return aws.ToString(out.QueueUrl)
}

func requireDenied(t *testing.T, err error, what string) {
	t.Helper()
	if err == nil || !strings.Contains(err.Error(), "AccessDenied") {
		t.Fatalf("%s: error = %v, want AccessDenied", what, err)
	}
}

func TestProvisionedQueues(t *testing.T) {
	owner := Owner(t)
	for name, dlq := range map[string]string{
		"wager-transactions.fifo": "wager-transactions-dlq.fifo",
		"wallet-events.fifo":      "wallet-events-dlq.fifo",
	} {
		out, err := owner.GetQueueAttributes(context.Background(), &sqs.GetQueueAttributesInput{
			QueueUrl: aws.String(queueURL(t, owner, name)), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameAll},
		})
		if err != nil {
			t.Fatal(err)
		}
		var redrive map[string]any
		if err := json.Unmarshal([]byte(out.Attributes["RedrivePolicy"]), &redrive); err != nil {
			t.Fatalf("%s redrive %q: %v", name, out.Attributes["RedrivePolicy"], err)
		}
		if !strings.HasSuffix(fmt.Sprint(redrive["deadLetterTargetArn"]), ":"+dlq) || fmt.Sprint(redrive["maxReceiveCount"]) != "5" ||
			out.Attributes["VisibilityTimeout"] != "30" || out.Attributes["FifoQueue"] != "true" {
			t.Fatalf("%s attributes = %v", name, out.Attributes)
		}
	}
}

func TestProvisionedIdentities(t *testing.T) {
	ctx := context.Background()
	owner := Owner(t)
	input, inputDLQ, events := queueURL(t, owner, "wager-transactions.fifo"),
		queueURL(t, owner, "wager-transactions-dlq.fifo"), queueURL(t, owner, "wallet-events.fifo")
	send := func(c *sqs.Client, url, body string) error {
		_, err := c.SendMessage(ctx, &sqs.SendMessageInput{QueueUrl: aws.String(url), MessageBody: aws.String(body),
			MessageGroupId: aws.String("probe"), MessageDeduplicationId: aws.String(uuid.NewString())})
		return err
	}

	probe := `{"probe":"` + uuid.NewString() + `"}`
	if err := send(ProfileClient(t, "provider-a"), input, probe); err != nil {
		t.Fatalf("provider-a must be allowed to send: %v", err)
	}
	requireDenied(t, send(Client(t, "333333333333"), input, probe), "unknown account send")

	consumer := ProfileClient(t, "wallet-consumer")
	found := false
	for deadline := time.Now().Add(10 * time.Second); !found && time.Now().Before(deadline); {
		for _, m := range Receive(t, consumer, input, 2*time.Second) {
			if aws.ToString(m.Body) == probe {
				found = true
				if m.Attributes["SenderId"] != ProviderAAccount {
					t.Fatalf("SenderId = %q, want %s", m.Attributes["SenderId"], ProviderAAccount)
				}
			}
			Delete(t, consumer, input, m)
		}
	}
	if !found {
		t.Fatal("wallet-consumer did not receive the provider message")
	}
	if err := send(consumer, inputDLQ, probe); err != nil {
		t.Fatalf("wallet-consumer must be allowed to dead-letter: %v", err)
	}
	requireDenied(t, send(consumer, events, probe), "consumer send to events")

	publisher := ProfileClient(t, "wallet-publisher")
	if err := send(publisher, events, probe); err != nil {
		t.Fatalf("wallet-publisher must be allowed to publish: %v", err)
	}
	_, err := publisher.ReceiveMessage(ctx, &sqs.ReceiveMessageInput{QueueUrl: aws.String(input), WaitTimeSeconds: 0})
	requireDenied(t, err, "publisher receive from input")

	for _, url := range []string{inputDLQ, events} { // keep the shared test queues small
		for _, m := range Receive(t, owner, url, time.Second) {
			Delete(t, owner, url, m)
		}
	}
}
