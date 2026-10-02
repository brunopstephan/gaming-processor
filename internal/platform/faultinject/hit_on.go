//go:build faultinject

package faultinject

import (
	"fmt"
	"os"
)

// Enabled reports whether this binary can inject faults.
const Enabled = true

// active is the point named by the FAULT environment variable.
var active = os.Getenv("FAULT")

// Hit exits the process at once (no deferred calls, no shutdown hooks) when
// point is the active fault, like a crash.
func Hit(point string) {
	if point == active {
		fmt.Fprintf(os.Stderr, "faultinject: crashing at %s\n", point)
		os.Exit(ExitCode)
	}
}
