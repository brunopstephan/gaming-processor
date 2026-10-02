// Package background runs periodic jobs (outbox relay, reference worker)
// inside the Fx lifecycle: a job runs again at once while it reports more
// work, otherwise after an interval; stopping cancels the running job and
// waits for it, with a deadline.
package background

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.uber.org/fx"
)

// Job does one unit of work; more=true asks to run again at once.
type Job func(ctx context.Context) (more bool, err error)

// Loop runs a Job until stopped.
type Loop struct {
	name     string
	interval time.Duration
	job      Job
	log      *slog.Logger
	cancel   context.CancelFunc
	done     chan struct{}
}

// NewLoop builds a stopped loop.
func NewLoop(name string, interval time.Duration, job Job, log *slog.Logger) *Loop {
	return &Loop{name: name, interval: interval, job: job, log: log.With("component", name)}
}

// Start runs the loop in a goroutine.
func (l *Loop) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	l.cancel, l.done = cancel, make(chan struct{})
	go l.run(ctx)
}

func (l *Loop) run(ctx context.Context) {
	defer close(l.done)
	for {
		more, err := l.runJob(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			l.log.WarnContext(ctx, "background job failed", "error", err.Error())
			more = false
		}
		if more {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(l.interval):
		}
	}
}

// runJob runs one job; a panic is logged and treated as a failed run so the
// loop waits an interval and carries on.
func (l *Loop) runJob(ctx context.Context) (more bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			l.log.ErrorContext(ctx, "background job panicked", "panic", fmt.Sprint(r))
			more, err = false, fmt.Errorf("background: %s panicked: %v", l.name, r)
		}
	}()
	return l.job(ctx)
}

// Stop cancels the running job and waits for the loop to end, up to timeout
// or ctx.
func (l *Loop) Stop(ctx context.Context, timeout time.Duration) error {
	if l.cancel == nil {
		return nil
	}
	l.cancel()
	select {
	case <-l.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("background: %s: %w", l.name, ctx.Err())
	case <-time.After(timeout):
		return fmt.Errorf("background: %s did not stop within %s", l.name, timeout)
	}
}

// Register starts the loop with the application and stops it on shutdown.
func Register(lc fx.Lifecycle, l *Loop, stopTimeout time.Duration) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error { l.Start(); return nil },
		OnStop:  func(ctx context.Context) error { return l.Stop(ctx, stopTimeout) },
	})
}
