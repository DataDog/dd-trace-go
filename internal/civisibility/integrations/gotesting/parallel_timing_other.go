//go:build !windows

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
	now   time.Time
	valid bool
}

func parallelTimeNowType() reflect.Type { return reflect.TypeFor[time.Time]() }

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
		now:   *(*time.Time)(unsafe.Add(base, layout.common.start.offset+layout.parallelNow.offset)),
		valid: true,
	}
}

func sampleParallelTiming(t *testing.T, baseline parallelTimingBaseline, bodyEnd time.Time) parallelTimingSample {
	layout := getTestingInternalsLayout()
	if t == nil || layout == nil || !layout.parallelTimingOK || !baseline.valid {
		return parallelTimingSample{}
	}
	base := commonBaseForTest(t, layout)
	if base == nil {
		return parallelTimingSample{}
	}
	resumed := *(*time.Time)(unsafe.Add(base, layout.common.start.offset+layout.parallelNow.offset))
	return parallelTimingSample{
		preDuration:       *fieldPtr[time.Duration](base, layout.common.duration),
		baselineToResume:  resumed.Sub(baseline.now),
		postResume:        bodyEnd.Sub(resumed),
		pauseClockValid:   true,
		wallProjectionEnd: bodyEnd,
		wallProjectionOK:  true,
	}
}
