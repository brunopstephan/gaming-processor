// Command wallet runs the wallet service: HTTP API (and, in later plans, the
// SQS consumer and background workers).
package main

import (
	"fmt"
	"os"
	"time"

	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/composition"
	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

func main() {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}
	// The HTTP drain must be a strict subset of the Fx stop budget, leaving room to close the DB pool.
	fx.New(
		composition.Modules(), composition.Logger(),
		fx.StopTimeout(cfg.HTTP.ShutdownTimeout+10*time.Second),
	).Run()
}
