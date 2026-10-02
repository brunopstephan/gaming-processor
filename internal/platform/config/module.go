package config

import (
	"os"

	"go.uber.org/fx"
)

// Module provides Config from the process environment. Invalid configuration
// fails the Fx graph before any component starts.
var Module = fx.Module("config",
	fx.Provide(func() (Config, error) { return Load(os.Getenv) }),
)
