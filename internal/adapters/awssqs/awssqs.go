// Package awssqs builds the SQS clients (one identity for the consumer, one
// for the publisher), resolves the queues the enabled components use and
// reports SQS readiness.
package awssqs

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
)

// Clients are the SQS identities of the process.
type Clients struct {
	Consumer  *sqs.Client
	Publisher *sqs.Client
}

// NewClients loads both identities. No request is sent here.
func NewClients(cfg config.Config) (*Clients, error) {
	consumer, err := newClient(cfg.SQS, cfg.SQS.ConsumerProfile)
	if err != nil {
		return nil, fmt.Errorf("awssqs: consumer credentials: %w", err)
	}
	publisher, err := newClient(cfg.SQS, cfg.SQS.PublisherProfile)
	if err != nil {
		return nil, fmt.Errorf("awssqs: publisher credentials: %w", err)
	}
	return &Clients{Consumer: consumer, Publisher: publisher}, nil
}

func newClient(cfg config.SQS, profile string) (*sqs.Client, error) {
	opts := []func(*awsconfig.LoadOptions) error{awsconfig.WithRegion(cfg.Region)}
	if profile != "" {
		opts = append(opts, awsconfig.WithSharedConfigProfile(profile))
	}
	awsCfg, err := awsconfig.LoadDefaultConfig(context.Background(), opts...)
	if err != nil {
		return nil, err
	}
	return sqs.NewFromConfig(awsCfg, func(o *sqs.Options) {
		if cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(cfg.Endpoint)
		}
	}), nil
}

// Queues are the queue URLs, resolved when the process starts.
type Queues struct {
	Input    string
	InputDLQ string
	Events   string
}

// NewQueues resolves, on start, the queues of the enabled components. Start
// fails if SQS is unreachable or a queue does not exist.
func NewQueues(lc fx.Lifecycle, cfg config.Config, c *Clients) *Queues {
	q := &Queues{}
	lc.Append(fx.Hook{OnStart: func(ctx context.Context) error { return q.resolve(ctx, cfg, c) }})
	return q
}

func (q *Queues) resolve(ctx context.Context, cfg config.Config, c *Clients) error {
	var err error
	if cfg.Toggles.Consumer {
		if q.Input, err = queueURL(ctx, c.Consumer, cfg.SQS.InputQueue); err != nil {
			return err
		}
		if q.InputDLQ, err = queueURL(ctx, c.Consumer, cfg.SQS.InputDLQ); err != nil {
			return err
		}
	}
	if cfg.Toggles.Outbox {
		if q.Events, err = queueURL(ctx, c.Publisher, cfg.SQS.EventsQueue); err != nil {
			return err
		}
	}
	return nil
}

func queueURL(ctx context.Context, c *sqs.Client, name string) (string, error) {
	out, err := c.GetQueueUrl(ctx, &sqs.GetQueueUrlInput{QueueName: aws.String(name)})
	if err != nil {
		return "", fmt.Errorf("awssqs: queue %s: %w", name, err)
	}
	return aws.ToString(out.QueueUrl), nil
}

// newHealthCheck probes every resolved queue with the identity that uses it:
// the input queue with the consumer, the events queue with the publisher.
func newHealthCheck(c *Clients, q *Queues) health.Check {
	return health.Check{Name: "sqs", Probe: func(ctx context.Context) error {
		probes := []struct {
			client *sqs.Client
			url    string
			name   string
		}{
			{c.Consumer, q.Input, "input"},
			{c.Publisher, q.Events, "events"},
		}
		probed := false
		for _, p := range probes {
			if p.url == "" {
				continue
			}
			probed = true
			_, err := p.client.GetQueueAttributes(ctx, &sqs.GetQueueAttributesInput{
				QueueUrl:       aws.String(p.url),
				AttributeNames: []types.QueueAttributeName{types.QueueAttributeNameApproximateNumberOfMessages},
			})
			if err != nil {
				return fmt.Errorf("awssqs: %s queue: %w", p.name, err)
			}
		}
		if !probed {
			return errors.New("awssqs: queues not resolved")
		}
		return nil
	}}
}

// Module provides the clients, the resolved queues and the readiness check.
var Module = fx.Module("sqs",
	fx.Provide(
		NewClients,
		NewQueues,
		fx.Annotate(newHealthCheck, fx.ResultTags(`group:"readiness"`)),
	),
)
