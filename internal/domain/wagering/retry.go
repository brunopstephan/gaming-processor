package wagering

import (
	"errors"
	"time"
)

// RetryPolicy controls how a PENDING_REFERENCE operation waits for its
// reference: exponential backoff (BaseDelay × 2^(n-1), capped at MaxDelay)
// and rejection once MaxAttempts resolutions failed.
type RetryPolicy struct {
	BaseDelay   time.Duration
	MaxDelay    time.Duration
	MaxAttempts int
}

// DefaultRetryPolicy is 2s base, 5min cap, 10 attempts.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{BaseDelay: 2 * time.Second, MaxDelay: 5 * time.Minute, MaxAttempts: 10}
}

// Validate reports an unusable policy.
func (p RetryPolicy) Validate() error {
	switch {
	case p.BaseDelay <= 0:
		return errors.New("wagering: retry base delay must be positive")
	case p.MaxDelay < p.BaseDelay:
		return errors.New("wagering: retry max delay must be >= base delay")
	case p.MaxAttempts < 1:
		return errors.New("wagering: retry max attempts must be >= 1")
	}
	return nil
}

// Delay returns the wait after the given number of failed attempts (>= 1).
func (p RetryPolicy) Delay(attempts int) time.Duration {
	d := p.BaseDelay
	for i := 1; i < attempts; i++ {
		if d >= p.MaxDelay/2 {
			return p.MaxDelay
		}
		d *= 2
	}
	return min(d, p.MaxDelay)
}
