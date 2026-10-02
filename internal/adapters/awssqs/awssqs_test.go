//go:build !unit && !e2e

package awssqs

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/health"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

func testConfig(t *testing.T, q sqstest.Queues) config.Config {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	return config.Config{
		Toggles: config.Toggles{Consumer: true, Outbox: true},
		SQS: config.SQS{Endpoint: sqstest.Endpoint(), Region: sqstest.Region,
			InputQueue: q.Input.Name, InputDLQ: q.InputDLQ.Name, EventsQueue: q.Events.Name},
	}
}

type readinessIn struct {
	fx.In
	Checks []health.Check `group:"readiness"`
}

func TestModuleResolvesQueuesAndReportsReadiness(t *testing.T) {
	q := sqstest.NewQueues(t, sqstest.Options{})
	var (
		queues *Queues
		in     readinessIn
	)
	app := fxtest.New(t, fx.NopLogger, Module, fx.Supply(testConfig(t, q)), fx.Populate(&queues),
		fx.Invoke(func(got readinessIn) { in = got }))
	app.RequireStart()
	defer app.RequireStop()

	if queues.Input != q.Input.URL || queues.InputDLQ != q.InputDLQ.URL || queues.Events != q.Events.URL {
		t.Fatalf("queues = %+v, want %+v", queues, q)
	}
	if len(in.Checks) != 1 || in.Checks[0].Name != "sqs" {
		t.Fatalf("checks = %+v", in.Checks)
	}
	if err := in.Checks[0].Probe(context.Background()); err != nil {
		t.Fatalf("sqs probe: %v", err)
	}
}

func TestStartFailsWhenQueueIsMissing(t *testing.T) {
	q := sqstest.NewQueues(t, sqstest.Options{})
	cfg := testConfig(t, q)
	cfg.SQS.InputQueue = "missing-" + q.Input.Name
	app := fx.New(fx.NopLogger, Module, fx.Supply(cfg), fx.Invoke(func(*Queues) {}))
	if err := app.Start(context.Background()); err == nil {
		_ = app.Stop(context.Background())
		t.Fatal("start must fail when a queue does not exist")
	}
}

func TestOnlyEnabledComponentsResolveTheirQueues(t *testing.T) {
	q := sqstest.NewQueues(t, sqstest.Options{})
	cfg := testConfig(t, q)
	cfg.Toggles = config.Toggles{Outbox: true}
	cfg.SQS.InputQueue = "not-used-" + q.Input.Name
	var queues *Queues
	app := fxtest.New(t, fx.NopLogger, Module, fx.Supply(cfg), fx.Populate(&queues))
	app.RequireStart()
	defer app.RequireStop()
	if queues.Input != "" || queues.Events != q.Events.URL {
		t.Fatalf("queues = %+v", queues)
	}
}

func TestHealthCheckProbesEveryResolvedQueue(t *testing.T) {
	q := sqstest.NewQueues(t, sqstest.Options{})
	clients, err := NewClients(testConfig(t, q))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	healthy := newHealthCheck(clients, &Queues{Input: q.Input.URL, Events: q.Events.URL})
	if err := healthy.Probe(ctx); err != nil {
		t.Fatalf("healthy probe: %v", err)
	}
	badEvents := newHealthCheck(clients, &Queues{Input: q.Input.URL, Events: q.Events.URL + "-gone"})
	if err := badEvents.Probe(ctx); err == nil || !strings.Contains(err.Error(), "events") {
		t.Fatalf("broken events queue must fail the probe naming it, got %v", err)
	}
	badInput := newHealthCheck(clients, &Queues{Input: q.Input.URL + "-gone", Events: q.Events.URL})
	if err := badInput.Probe(ctx); err == nil || !strings.Contains(err.Error(), "input") {
		t.Fatalf("broken input queue must fail the probe naming it, got %v", err)
	}
	if err := newHealthCheck(clients, &Queues{}).Probe(ctx); err == nil {
		t.Fatal("no resolved queue must fail the probe")
	}
}
