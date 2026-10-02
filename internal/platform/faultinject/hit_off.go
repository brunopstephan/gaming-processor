//go:build !faultinject

package faultinject

// Enabled reports whether this binary can inject faults.
const Enabled = false

// Hit does nothing in the normal build.
func Hit(string) {}
