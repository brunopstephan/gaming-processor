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

func TestPanickingJobDoesNotKillTheLoop(t *testing.T) {
	calls := make(chan int, 16)
	var n atomic.Int32
	loop := NewLoop("test", 10*time.Millisecond, func(context.Context) (bool, error) {
		c := int(n.Add(1))
		calls <- c
		if c == 1 {
			panic("boom")
		}
		return false, nil
	}, slog.New(slog.DiscardHandler))
	loop.Start()
	for want := 1; want <= 2; want++ {
		select {
		case got := <-calls:
			if got != want {
				t.Fatalf("call %d, want %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("job run %d never happened: the loop died after the panic", want)
		}
	}
	if err := loop.Stop(context.Background(), time.Second); err != nil {
		t.Fatalf("stop after a panic = %v", err)
	}
}
