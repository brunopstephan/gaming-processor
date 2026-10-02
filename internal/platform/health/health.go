// Package health aggregates readiness probes (PostgreSQL now, SQS later)
// and reports not-ready while the process drains during shutdown.
package health

import (
	"context"
	"sync/atomic"
	"time"
)

// Check is one named readiness probe, contributed via the Fx group "readiness".
type Check struct {
	Name  string
	Probe func(ctx context.Context) error
}

// Readiness runs every check with a timeout.
type Readiness struct {
	checks   []Check
	draining atomic.Bool
}

// NewReadiness builds the aggregate.
func NewReadiness(checks []Check) *Readiness { return &Readiness{checks: checks} }

// StartDraining makes Check report not-ready (load balancers stop routing).
func (r *Readiness) StartDraining() { r.draining.Store(true) }

// Check runs the probes; ok is false if any fails or the process is draining.
func (r *Readiness) Check(ctx context.Context) (bool, map[string]string) {
	results := map[string]string{}
	ok := true
	if r.draining.Load() {
		results["draining"] = "true"
		ok = false
	}
	for _, c := range r.checks {
		pctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		err := c.Probe(pctx)
		cancel()
		if err != nil {
			results[c.Name] = "DOWN"
			ok = false
			continue
		}
		results[c.Name] = "UP"
	}
	return ok, results
}
