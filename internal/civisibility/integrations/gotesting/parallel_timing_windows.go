//go:build windows

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"reflect"
	"testing"
	"time"
	"unsafe"
)

type parallelTimingBaseline struct {
	now   int64
	valid bool
}

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
	frequency := testingClockFrequency()
	elapsedToResume, baselineOK := testingClockDuration(resumed-baseline.now, frequency)
	if !baselineOK {
		return parallelTimingSample{}
	}
	sample := parallelTimingSample{
		durationBeforePause: *fieldPtr[time.Duration](base, layout.common.duration),
		elapsedToResume:     elapsedToResume,
		pauseClockValid:     true,
	}
	// QPC has no wall-clock epoch. Bracket the read with time.Now and use the
	// midpoint when the bracket is tight; retry once if the goroutine was preempted.
	for range 2 {
		before := time.Now()
		counter, ok := testingClockNow()
		after := time.Now()
		if !ok {
			continue
		}
		durationAfterResume, ok := testingClockDuration(counter-resumed, frequency)
		if !ok {
			continue
		}
		sample.durationAfterResume = durationAfterResume
		if after.Sub(before) <= parallelTimingSkewTolerance {
			sample.wallProjectionEnd = before.Add(after.Sub(before) / 2)
			sample.wallProjectionOK = true
			break
		}
	}
	return sample
}
