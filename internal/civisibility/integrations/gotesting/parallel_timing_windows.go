//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"math/bits"
	"reflect"
	"sync"
	"testing"
	"time"
	"unsafe"
)

type parallelTimingBaseline struct {
	now   int64
	valid bool
}

var (
	parallelTimingFrequencyOnce sync.Once
	parallelTimingFrequency     int64
)

func parallelTimeNowType() reflect.Type { return reflect.TypeFor[int64]() }

func captureParallelTimingBaseline(t *testing.T) parallelTimingBaseline {
	layout := getTestingInternalsLayout()
	if t == nil || layout == nil || !layout.parallelTimingOK {
		return parallelTimingBaseline{}
	}
	base := commonBaseForTest(t, layout)
	if base == nil {
		return parallelTimingBaseline{}
	}
	return parallelTimingBaseline{
		now:   *(*int64)(unsafe.Add(base, layout.common.start.offset+layout.parallelNow.offset)),
		valid: true,
	}
}

func sampleParallelTiming(t *testing.T, baseline parallelTimingBaseline, _ time.Time) parallelTimingSample {
	layout := getTestingInternalsLayout()
	if t == nil || layout == nil || !layout.parallelTimingOK || !baseline.valid {
		return parallelTimingSample{}
	}
	base := commonBaseForTest(t, layout)
	if base == nil {
		return parallelTimingSample{}
	}
	resumed := *(*int64)(unsafe.Add(base, layout.common.start.offset+layout.parallelNow.offset))
	frequency := getParallelTimingFrequency()
	baselineToResume, baselineOK := parallelCounterDuration(resumed-baseline.now, frequency)
	if !baselineOK {
		return parallelTimingSample{}
	}
	sample := parallelTimingSample{
		preDuration:      *fieldPtr[time.Duration](base, layout.common.duration),
		baselineToResume: baselineToResume,
		pauseClockValid:  true,
	}
	for range 2 {
		before := time.Now()
		counter, ok := retryAttemptPerformanceValue(retryAttemptQueryPerformanceCounter)
		after := time.Now()
		if !ok {
			continue
		}
		postResume, ok := parallelCounterDuration(counter-resumed, frequency)
		if !ok {
			continue
		}
		sample.postResume = postResume
		if after.Sub(before) <= parallelTimingSkewTolerance {
			sample.wallProjectionEnd = before.Add(after.Sub(before) / 2)
			sample.wallProjectionOK = true
			break
		}
	}
	return sample
}

func getParallelTimingFrequency() int64 {
	parallelTimingFrequencyOnce.Do(func() {
		frequency, ok := retryAttemptPerformanceValue(retryAttemptQueryPerformanceFrequency)
		if ok && frequency > 0 {
			parallelTimingFrequency = frequency
		}
	})
	return parallelTimingFrequency
}

func parallelCounterDuration(delta, frequency int64) (time.Duration, bool) {
	if delta < 0 || frequency <= 0 {
		return 0, false
	}
	hi, lo := bits.Mul64(uint64(delta), uint64(time.Second)/uint64(time.Nanosecond))
	if hi >= uint64(frequency) {
		return 0, false
	}
	nanos, _ := bits.Div64(hi, lo, uint64(frequency))
	if nanos > uint64(^uint64(0)>>1) {
		return 0, false
	}
	return time.Duration(nanos), true
}
