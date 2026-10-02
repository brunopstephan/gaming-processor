//go:build !unit && !e2e

package sqsconsumer

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
	"github.com/google/uuid"

	"github.com/brunopstephan/backend-challenge-go/internal/app"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wagering"
	"github.com/brunopstephan/backend-challenge-go/internal/domain/wallet"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/apptest"
	"github.com/brunopstephan/backend-challenge-go/internal/testsupport/sqstest"
)

func eventually(t *testing.T, timeout time.Duration, cond func() bool, what string) {
	t.Helper()
	for deadline := time.Now().Add(timeout); time.Now().Before(deadline); time.Sleep(100 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func runSettings(q sqstest.Queues) Settings {
	return Settings{InputURL: q.Input.URL, DLQURL: q.InputDLQ.URL, Workers: 2, WaitTime: time.Second, MaxMessages: 10,
		RetryBaseDelay: time.Second, RetryMaxDelay: 4 * time.Second, ShutdownTimeout: 5 * time.Second,
		SenderProviders: map[string]string{sqstest.ProviderAAccount: "provider-a"}}
}

func TestConsumerProcessesWhileRunning(t *testing.T) {
	h := apptest.New(t)
	ws, _ := app.NewWageringService(h.Deps, wagering.DefaultRetryPolicy())
	q := sqstest.NewQueues(t, sqstest.Options{})
	c := New(sqstest.Owner(t), app.NewIntakeService(h.Deps, h.Inbox, ws), &fakeMetrics{}, slog.New(slog.DiscardHandler), runSettings(q))
	c.Start()

	providerA := sqstest.Client(t, sqstest.ProviderAAccount)
	shared := h.OpenWallet(t, "100.00")
	others := make([]*wallet.Wallet, 3)
	for i := range others {
		others[i] = h.OpenWallet(t, "50.00")
		sqstest.Send(t, providerA, q.Input.URL, body("msg-"+uuid.NewString(), "provider-a", others[i], "BET", "10.00", uuid.NewString(), ""), others[i].ID().String())
	}
	for range 4 {
		sqstest.Send(t, providerA, q.Input.URL, body("msg-"+uuid.NewString(), "provider-a", shared, "BET", "5.00", uuid.NewString(), ""), shared.ID().String())
	}
	get := func(w *wallet.Wallet) string {
		got, err := h.Wallets.Get(context.Background(), w.ID())
		if err != nil {
			t.Fatal(err)
		}
		return got.Balance().Amount()
	}
	eventually(t, 20*time.Second, func() bool {
		if get(shared) != "80.00" {
			return false
		}
		for _, w := range others {
			if get(w) != "40.00" {
				return false
			}
		}
		return true
	}, "all messages processed")

	start := time.Now()
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("idle stop took %s", time.Since(start))
	}
}

// blockingIntake blocks until its context ends, like a transaction held up
// by a long lock wait, and then reports the cancellation as transient.
type blockingIntake struct{ entered chan struct{} }

func (b blockingIntake) Handle(ctx context.Context, _ string, _ wagering.Command, _ app.Meta) (app.IntakeResult, error) {
	close(b.entered)
	<-ctx.Done()
	return app.IntakeResult{}, app.ErrTransient
}

func TestStopReleasesInFlightMessage(t *testing.T) {
	h := apptest.New(t)
	q := sqstest.NewQueues(t, sqstest.Options{})
	intake := blockingIntake{entered: make(chan struct{})}
	s := runSettings(q)
	s.Workers, s.ShutdownTimeout = 1, 500*time.Millisecond
	owner := sqstest.Owner(t)
	c := New(owner, intake, &fakeMetrics{}, slog.New(slog.DiscardHandler), s)
	c.Start()

	w := h.OpenWallet(t, "10.00")
	sqstest.Send(t, sqstest.Client(t, sqstest.ProviderAAccount), q.Input.URL, body("msg-"+uuid.NewString(), "provider-a", w, "BET", "1.00", uuid.NewString(), ""), "g")
	select {
	case <-intake.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("message never reached the intake")
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	again := sqstest.Receive(t, owner, q.Input.URL, 3*time.Second)
	if len(again) != 1 || again[0].Attributes["ApproximateReceiveCount"] != "2" {
		t.Fatalf("aborted message must be visible again at once, got %+v", again)
	}
}

// slowIntake signals that it started, finishes its work after a delay and
// reports a duplicate (so the message is deleted).
type slowIntake struct {
	entered  chan struct{}
	once     *sync.Once
	finished *atomic.Int32
}

func (s slowIntake) Handle(context.Context, string, wagering.Command, app.Meta) (app.IntakeResult, error) {
	s.once.Do(func() { close(s.entered) })
	time.Sleep(time.Second)
	s.finished.Add(1)
	return app.IntakeResult{Outcome: app.IntakeDuplicate}, nil
}

func TestStopWaitsForInFlightWork(t *testing.T) {
	h := apptest.New(t)
	q := sqstest.NewQueues(t, sqstest.Options{})
	intake := slowIntake{entered: make(chan struct{}), once: &sync.Once{}, finished: &atomic.Int32{}}
	owner := sqstest.Owner(t)
	c := New(owner, intake, &fakeMetrics{}, slog.New(slog.DiscardHandler), runSettings(q))
	c.Start()
	w := h.OpenWallet(t, "10.00")
	sqstest.Send(t, sqstest.Client(t, sqstest.ProviderAAccount), q.Input.URL, body("msg-"+uuid.NewString(), "provider-a", w, "BET", "1.00", uuid.NewString(), ""), "g")
	select {
	case <-intake.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("message never reached the intake")
	}
	start := time.Now()
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if time.Since(start) < 500*time.Millisecond {
		t.Fatalf("Stop returned after %s, before the in-flight work finished", time.Since(start))
	}
	finished := intake.finished
	if finished.Load() != 1 {
		t.Fatalf("finished = %d, the in-flight message must complete before Stop returns", finished.Load())
	}
	if msgs := sqstest.Receive(t, owner, q.Input.URL, 3*time.Second); len(msgs) != 0 {
		t.Fatalf("completed message was not deleted: %v", aws.ToString(msgs[0].Body))
	}
}

// countingIntake counts calls and blocks every one until its context ends.
type countingIntake struct {
	entered chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (b *countingIntake) Handle(ctx context.Context, _ string, _ wagering.Command, _ app.Meta) (app.IntakeResult, error) {
	b.calls.Add(1)
	b.once.Do(func() { close(b.entered) })
	<-ctx.Done()
	return app.IntakeResult{}, app.ErrTransient
}

// TestStopReleasesReceivedButUnstartedMessages: one worker receives both
// messages (different groups) in a single batch and blocks on the first; Stop
// must release the second without ever handling it.
func TestStopReleasesReceivedButUnstartedMessages(t *testing.T) {
	h := apptest.New(t)
	q := sqstest.NewQueues(t, sqstest.Options{})
	intake := &countingIntake{entered: make(chan struct{})}
	s := runSettings(q)
	s.Workers, s.MaxMessages, s.ShutdownTimeout = 1, 10, 500*time.Millisecond
	owner := sqstest.Owner(t)
	c := New(owner, intake, &fakeMetrics{}, slog.New(slog.DiscardHandler), s)

	w := h.OpenWallet(t, "10.00")
	providerA := sqstest.Client(t, sqstest.ProviderAAccount)
	for _, group := range []string{"g1", "g2"} {
		sqstest.Send(t, providerA, q.Input.URL, body("msg-"+uuid.NewString(), "provider-a", w, "BET", "1.00", uuid.NewString(), ""), group)
	}
	c.Start()
	select {
	case <-intake.entered:
	case <-time.After(15 * time.Second):
		t.Fatal("message never reached the intake")
	}
	if err := c.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := intake.calls.Load(); n != 1 {
		t.Fatalf("intake calls = %d, the unstarted message must not be handled", n)
	}
	var got []types.Message
	eventually(t, 5*time.Second, func() bool {
		got = append(got, sqstest.Receive(t, owner, q.Input.URL, time.Second)...)
		return len(got) >= 2
	}, "both messages visible again at once")
	for _, m := range got {
		if m.Attributes["ApproximateReceiveCount"] != "2" {
			t.Fatalf("message %s receive count = %s, want 2 (one batch receive, one after the release)", aws.ToString(m.MessageId), m.Attributes["ApproximateReceiveCount"])
		}
	}
}
