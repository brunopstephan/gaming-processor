//go:build !integration && !e2e

package background

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestLoopRunsAgainWhileThereIsMoreWork(t *testing.T) {
	var calls atomic.Int32
	loop := NewLoop("test", time.Hour, func(context.Context) (bool, error) {
		return calls.Add(1) < 5, nil
	}, slog.New(slog.DiscardHandler))
	loop.Start()
	deadline := time.Now().Add(2 * time.Second)
	for calls.Load() < 5 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := loop.Stop(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 5 {
		t.Fatalf("calls = %d, want 5 back-to-back runs then idle", calls.Load())
	}
}

func TestLoopWaitsIntervalAfterErrorsAndStopsPromptly(t *testing.T) {
	var calls atomic.Int32
	loop := NewLoop("test", 50*time.Millisecond, func(context.Context) (bool, error) {
		calls.Add(1)
		return true, errors.New("boom")
	}, slog.New(slog.DiscardHandler))
	loop.Start()
	time.Sleep(275 * time.Millisecond)
	if err := loop.Stop(context.Background(), time.Second); err != nil {
		t.Fatal(err)
	}
	if n := calls.Load(); n < 3 || n > 8 {
		t.Fatalf("calls = %d, want errors to wait one interval each", n)
	}
}

func TestStopCancelsTheRunningJob(t *testing.T) {
	loop := NewLoop("test", time.Hour, func(ctx context.Context) (bool, error) {
		<-ctx.Done()
		return false, ctx.Err()
	}, slog.New(slog.DiscardHandler))
	loop.Start()
	time.Sleep(20 * time.Millisecond)
	if err := loop.Stop(context.Background(), time.Second); err != nil {
		t.Fatalf("stop = %v", err)
	}
}

func TestStopReportsAStuckJob(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	loop := NewLoop("test", time.Hour, func(context.Context) (bool, error) {
		<-release
		return false, nil
	}, slog.New(slog.DiscardHandler))
	loop.Start()
	time.Sleep(20 * time.Millisecond)
	if err := loop.Stop(context.Background(), 50*time.Millisecond); err == nil {
		t.Fatal("a job that ignores cancellation must make Stop fail after the timeout")
	}
}
