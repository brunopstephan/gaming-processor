//go:build !integration && !e2e

package composition

import (
	"testing"
	"time"

	"github.com/brunopstephan/backend-challenge-go/internal/adapters/sqsconsumer"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

func TestStopTimeoutCoversEnabledComponents(t *testing.T) {
	cfg := config.Config{
		HTTP:      config.HTTP{ShutdownTimeout: 15 * time.Second},
		SQS:       config.SQS{ShutdownTimeout: 20 * time.Second},
		Outbox:    config.Outbox{ShutdownTimeout: 10 * time.Second},
		RefWorker: config.RefWorker{ShutdownTimeout: 10 * time.Second},
		Toggles:   config.Toggles{HTTP: true, Consumer: true, Outbox: true, RefWorker: true},
	}
	want := 15*time.Second + 20*time.Second + sqsconsumer.AbortGrace + 10*time.Second + 10*time.Second + 10*time.Second
	if got := StopTimeout(cfg); got != want {
		t.Fatalf("all on = %s, want %s", got, want)
	}
	cfg.Toggles = config.Toggles{HTTP: true}
	if got := StopTimeout(cfg); got != 25*time.Second {
		t.Fatalf("http only = %s, want 25s", got)
	}
}
