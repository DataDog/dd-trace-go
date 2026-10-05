// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"
	"time"
)

func init() {
	// Override now and nowTime to use time.Now() in tests. Production code
	// calls GetSystemTimePreciseAsFileTime() directly for higher precision, but
	// that syscall bypasses testing/synctest's fake clock. time.Now() is
	// intercepted by synctest, so this makes the fake clock work correctly on
	// Windows without changing production behavior.
	now = func() int64 { return time.Now().UnixNano() }
	nowTime = func() time.Time { return time.Now() }
}

func BenchmarkNormalTimeNow(b *testing.B) {
	for b.Loop() {
		lowPrecisionNow()
	}
}

func BenchmarkHighPrecisionTime(b *testing.B) {
	for b.Loop() {
		highPrecisionNow()
	}
}

func TestHighPrecisionTimerIsMoreAccurate(t *testing.T) {
	// A busy Windows CI goroutine can be preempted between the two timer samples,
	// making both advance together. Retry until we get an uninterrupted measurement.
	const maxAttempts = 20
	for range maxAttempts {
		startLow := lowPrecisionNow()
		startHigh := highPrecisionNow()
		stopHigh := startHigh
		for stopHigh == startHigh {
			stopHigh = highPrecisionNow()
		}
		if stopLow := lowPrecisionNow(); stopLow == startLow {
			return // highPrecisionNow advanced within a single lowPrecisionNow tick
		}
	}
	t.Errorf("highPrecisionNow never advanced within a single lowPrecisionNow tick after %d attempts", maxAttempts)
}
