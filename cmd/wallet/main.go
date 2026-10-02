// Command wallet runs the wallet service: HTTP API (and, in later plans, the
// SQS consumer and background workers).
package main

import (
	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/composition"
)

func main() {
	fx.New(composition.Modules(), composition.Logger()).Run()
}
