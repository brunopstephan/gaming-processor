//go:build !integration && !e2e

package wagering

import (
	"testing"
	"time"
)

func TestRetryPolicyDelay(t *testing.T) {
	p := DefaultRetryPolicy()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
		32 * time.Second, 64 * time.Second, 128 * time.Second, 256 * time.Second, 5 * time.Minute, 5 * time.Minute}
	for i, w := range want {
		if got := p.Delay(i + 1); got != w {
			t.Errorf("Delay(%d) = %s, want %s", i+1, got, w)
		}
	}
	if got := p.Delay(1000); got != 5*time.Minute {
		t.Errorf("Delay(1000) = %s, want cap", got)
	}
}

func TestRetryPolicyValidate(t *testing.T) {
	for _, p := range []RetryPolicy{
		{BaseDelay: 0, MaxDelay: time.Second, MaxAttempts: 1},
		{BaseDelay: time.Second, MaxDelay: time.Millisecond, MaxAttempts: 1},
		{BaseDelay: time.Second, MaxDelay: time.Second, MaxAttempts: 0},
	} {
		if p.Validate() == nil {
			t.Errorf("Validate(%+v) = nil, want error", p)
		}
	}
}
