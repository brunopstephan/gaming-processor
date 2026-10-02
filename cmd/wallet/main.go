// Command wallet runs the wallet service. Component toggles choose what this
// process runs: the HTTP API, the SQS consumer, the outbox relay and the
// reference worker (all by default).
package main

import (
	"fmt"
	"os"

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
	fx.New(composition.Modules(cfg), composition.Logger(), fx.StopTimeout(composition.StopTimeout(cfg))).Run()
}
