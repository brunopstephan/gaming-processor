//go:build !integration && !e2e

package health

import (
	"context"
	"errors"
	"testing"
)

func TestReadiness(t *testing.T) {
	failing := errors.New("down")
	r := NewReadiness([]Check{
		{Name: "postgres", Probe: func(context.Context) error { return nil }},
		{Name: "sqs", Probe: func(context.Context) error { return failing }},
	})
	ok, results := r.Check(context.Background())
	if ok || results["postgres"] != "UP" || results["sqs"] != "DOWN" {
		t.Fatalf("ok %v results %v", ok, results)
	}
	healthy := NewReadiness([]Check{{Name: "postgres", Probe: func(context.Context) error { return nil }}})
	if ok, _ := healthy.Check(context.Background()); !ok {
		t.Fatal("healthy readiness reported not ready")
	}
	healthy.StartDraining()
	if ok, results := healthy.Check(context.Background()); ok || results["draining"] != "true" {
		t.Fatalf("draining must report not ready: %v", results)
	}
}
