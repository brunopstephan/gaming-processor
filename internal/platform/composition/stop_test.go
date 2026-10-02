//go:build !integration && !e2e

package composition

import (
	"testing"
	"time"

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
	if got := StopTimeout(cfg); got != 65*time.Second {
		t.Fatalf("all on = %s, want 65s", got)
	}
	cfg.Toggles = config.Toggles{HTTP: true}
	if got := StopTimeout(cfg); got != 25*time.Second {
		t.Fatalf("http only = %s, want 25s", got)
	}
}
