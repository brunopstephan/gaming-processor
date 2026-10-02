// Package sqstest talks to the ministack_test container
// (`docker compose --profile test up -d --wait ministack_test`): per-test
// FIFO queues with redrive, clients for the owner and provider accounts, and
// send/receive helpers.
package sqstest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"
)

// Region and the accounts provisioned in MiniStack. A 12-digit access key is
// the root credential of that account; "test" is the owner account's root.
const (
	Region           = "us-east-1"
	OwnerAccount     = "000000000000"
	ProviderAAccount = "111111111111"
	ProviderBAccount = "222222222222"
)

const infraHint = "Suba a infra: `docker compose --profile test up -d --wait ministack_test && " +
	"docker compose --profile test run --rm ministack_test_init`"

// Endpoint is the test MiniStack (env MINISTACK_TEST_URL).
func Endpoint() string {
	if v := os.Getenv("MINISTACK_TEST_URL"); v != "" {
		return v
	}
	return "http://localhost:4567"
}

// Client returns an SQS client authenticated as accessKey.
func Client(t testing.TB, accessKey string) *sqs.Client {
	t.Helper()
	return sqs.New(sqs.Options{
		Region:       Region,
		BaseEndpoint: aws.String(Endpoint()),
		Credentials:  credentials.NewStaticCredentialsProvider(accessKey, accessKey+"-secret", ""),
	})
}

// Owner returns a client with the queue owner's root credential.
func Owner(t testing.TB) *sqs.Client { return Client(t, "test") }

// CredentialsFile is the shared credentials file written by
// ministack_test_init (env MINISTACK_TEST_CREDENTIALS).
func CredentialsFile() string {
	if v := os.Getenv("MINISTACK_TEST_CREDENTIALS"); v != "" {
		return v
	}
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "..", ".local", "ministack-test", "credentials")
}

// ProfileClient returns a client for a profile of CredentialsFile.
func ProfileClient(t testing.TB, profile string) *sqs.Client {
	t.Helper()
	if _, err := os.Stat(CredentialsFile()); err != nil {
		t.Fatalf("credenciais do ministack_test ausentes em %s (%v). %s", CredentialsFile(), err, infraHint)
	}
	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(Region),
		awsconfig.WithSharedCredentialsFiles([]string{CredentialsFile()}),
		awsconfig.WithSharedConfigProfile(profile),
	)
	if err != nil {
		t.Fatalf("profile %s: %v", profile, err)
	}
	return sqs.NewFromConfig(cfg, func(o *sqs.Options) { o.BaseEndpoint = aws.String(Endpoint()) })
}

// Queue is a queue created for one test.
type Queue struct {
	Name string
	URL  string
}

// Queues mirrors the provisioned layout: input and events queues with their DLQs.
type Queues struct {
	Input, InputDLQ, Events, EventsDLQ Queue
}

// Options tune the per-test queues; zero values mean 30s and 5.
type Options struct {
	VisibilityTimeout int
	MaxReceiveCount   int
}

// NewQueues creates uniquely named FIFO queues with redrive and the provider
// send policy on the input queue, deleted at cleanup.
func NewQueues(t testing.TB, opts Options) Queues {
	t.Helper()
	if opts.VisibilityTimeout == 0 {
		opts.VisibilityTimeout = 30
	}
	if opts.MaxReceiveCount == 0 {
		opts.MaxReceiveCount = 5
	}
	c := Owner(t)
	prefix := "t" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	inDLQ := create(t, c, prefix+"-in-dlq.fifo", nil)
	evDLQ := create(t, c, prefix+"-ev-dlq.fifo", nil)
	in := create(t, c, prefix+"-in.fifo", map[string]string{
		"VisibilityTimeout": fmt.Sprint(opts.VisibilityTimeout),
		"RedrivePolicy":     redrive(t, c, inDLQ, opts.MaxReceiveCount),
	})
	ev := create(t, c, prefix+"-ev.fifo", map[string]string{
		"VisibilityTimeout": fmt.Sprint(opts.VisibilityTimeout),
		"RedrivePolicy":     redrive(t, c, evDLQ, opts.MaxReceiveCount),
	})
	policy, _ := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Effect": "Allow",
			"Principal": map[string]any{"AWS": []string{
				"arn:aws:iam::" + ProviderAAccount + ":root", "arn:aws:iam::" + ProviderBAccount + ":root",
			}},
			"Action":   "sqs:SendMessage",
			"Resource": "arn:aws:sqs:" + Region + ":" + OwnerAccount + ":" + in.Name,
		}},
	})
	if _, err := c.SetQueueAttributes(context.Background(), &sqs.SetQueueAttributesInput{
		QueueUrl: aws.String(in.URL), Attributes: map[string]string{"Policy": string(policy)},
	}); err != nil {
		t.Fatalf("queue policy: %v", err)
	}
	return Queues{Input: in, InputDLQ: inDLQ, Events: ev, EventsDLQ: evDLQ}
}

func create(t testing.TB, c *sqs.Client, name string, attrs map[string]string) Queue {
	t.Helper()
	all := map[string]string{"FifoQueue": "true", "ContentBasedDeduplication": "false"}
	for k, v := range attrs {
		all[k] = v
	}
	out, err := c.CreateQueue(context.Background(), &sqs.CreateQueueInput{QueueName: aws.String(name), Attributes: all})
	if err != nil {
		t.Fatalf("ministack de teste indisponível (%v). %s", err, infraHint)
	}
	url := aws.ToString(out.QueueUrl)
	t.Cleanup(func() { _, _ = c.DeleteQueue(context.Background(), &sqs.DeleteQueueInput{QueueUrl: aws.String(url)}) })
	return Queue{Name: name, URL: url}
}

func redrive(t testing.TB, c *sqs.Client, dlq Queue, maxReceive int) string {
	t.Helper()
	out, err := c.GetQueueAttributes(context.Background(), &sqs.GetQueueAttributesInput{
		QueueUrl: aws.String(dlq.URL), AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameQueueArn},
	})
	if err != nil {
		t.Fatalf("dlq arn: %v", err)
	}
	policy, _ := json.Marshal(map[string]string{
		"deadLetterTargetArn": out.Attributes[string(types.QueueAttributeNameQueueArn)],
		"maxReceiveCount":     fmt.Sprint(maxReceive),
	})
	return string(policy)
}

// Send sends body to a FIFO queue in group and returns the deduplication id used.
func Send(t testing.TB, c *sqs.Client, queueURL, body, group string) string {
	t.Helper()
	dedup := uuid.NewString()
	if _, err := c.SendMessage(context.Background(), &sqs.SendMessageInput{
		QueueUrl: aws.String(queueURL), MessageBody: aws.String(body),
		MessageGroupId: aws.String(group), MessageDeduplicationId: aws.String(dedup),
	}); err != nil {
		t.Fatalf("send: %v", err)
	}
	return dedup
}

// Receive returns the first non-empty batch (up to 10 messages, with every
// system and message attribute) received within wait, or nil.
func Receive(t testing.TB, c *sqs.Client, queueURL string, wait time.Duration) []types.Message {
	t.Helper()
	deadline := time.Now().Add(wait)
	for {
		out, err := c.ReceiveMessage(context.Background(), &sqs.ReceiveMessageInput{
			QueueUrl: aws.String(queueURL), MaxNumberOfMessages: 10, WaitTimeSeconds: 1,
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{types.MessageSystemAttributeNameAll},
			MessageAttributeNames:       []string{"All"},
		})
		if err != nil {
			t.Fatalf("receive: %v", err)
		}
		if len(out.Messages) > 0 || time.Now().After(deadline) {
			return out.Messages
		}
	}
}

// Delete removes msg from the queue.
func Delete(t testing.TB, c *sqs.Client, queueURL string, msg types.Message) {
	t.Helper()
	if _, err := c.DeleteMessage(context.Background(), &sqs.DeleteMessageInput{
		QueueUrl: aws.String(queueURL), ReceiptHandle: msg.ReceiptHandle,
	}); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

// FailureReason returns the failureReason message attribute ("" if absent).
func FailureReason(msg types.Message) string {
	if v, ok := msg.MessageAttributes["failureReason"]; ok {
		return aws.ToString(v.StringValue)
	}
	return ""
}
