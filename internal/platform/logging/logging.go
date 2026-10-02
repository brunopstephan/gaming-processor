// Package logging builds the JSON structured logger.
package logging

import (
	"io"
	"log/slog"
	"os"

	"go.uber.org/fx"

	"github.com/brunopstephan/backend-challenge-go/internal/platform/config"
)

// New returns a JSON slog.Logger writing to w at level.
func New(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// Module provides the process logger (stdout, JSON).
var Module = fx.Module("logging",
	fx.Provide(func(cfg config.Config) *slog.Logger { return New(os.Stdout, cfg.Log.Level) }),
)
