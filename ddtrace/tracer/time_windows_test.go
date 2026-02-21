// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
	for n := 0; n < b.N; n++ {
		lowPrecisionNow()
	}
}

func BenchmarkHighPrecisionTime(b *testing.B) {
	for n := 0; n < b.N; n++ {
		highPrecisionNow()
	}
}

func TestHighPrecisionTimerIsMoreAccurate(t *testing.T) {
	startLow := lowPrecisionNow()
	startHigh := highPrecisionNow()
	stopHigh := highPrecisionNow()
	for stopHigh == startHigh {
		stopHigh = highPrecisionNow()
	}
	stopLow := lowPrecisionNow()
	assert.Equal(t, int64(0), stopLow-startLow)
}
