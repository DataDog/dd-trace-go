// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func BenchmarkNormalTimeNow(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			lowPrecisionNow()
		}
	})
}

func BenchmarkHighPrecisionTime(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			highPrecisionNow()
		}
	})
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
