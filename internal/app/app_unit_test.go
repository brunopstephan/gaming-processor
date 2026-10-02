//go:build !integration && !e2e

package app

import (
	"testing"
	"time"
)

func TestSystemClockIsUTCAtMicrosecondPrecision(t *testing.T) {
	for range 100 {
		now := SystemClock()
		if now.Nanosecond()%1000 != 0 || now.Location() != time.UTC {
			t.Fatalf("SystemClock() = %v (ns %d, loc %v)", now, now.Nanosecond(), now.Location())
		}
	}
}
