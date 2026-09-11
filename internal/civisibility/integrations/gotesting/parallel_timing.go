// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
)

const (
	parallelTimingSkewTolerance = time.Millisecond
	parallelWaitOperationName   = "testing.parallel.wait"
	parallelWaitResourceName    = "testing.T.Parallel"
	parallelWaitTag             = "test.parallel.wait"
)

// Keep the baseline out of testExecutionMetadata because only parallel tests
// need it. A non-nil state also records that the hook ran with an invalid baseline.
type parallelTimingState struct {
	baseline parallelTimingBaseline
}

// parallelTimingSample combines Go's native testing clock with the wrapper's
// wall clock. T.Parallel records the pre-pause duration and resets the native
// start time when the test resumes.
type parallelTimingSample struct {
	durationBeforePause time.Duration
	elapsedToResume     time.Duration
	durationAfterResume time.Duration
	pauseClockValid     bool
	wallProjectionEnd   time.Time
	wallProjectionOK    bool
}

type testExecutionTiming struct {
	isParallel        bool
	activeDuration    time.Duration
	activeDurationOK  bool
	pauseStart        time.Time
	pauseEnd          time.Time
	pauseProjectionOK bool
}

func initializeTestExecutionTiming(test integrations.Test) {
	if test == nil {
		return
	}
	test.SetTag(constants.TestActiveDuration, int64(0))
	test.SetTag(constants.TestIsParallel, false)
}

func captureParallelTimingHook(t *testing.T) {
	if t == nil {
		return
	}
	execMeta := getTestMetadata(t)
	if execMeta == nil || execMeta.parallelTiming != nil {
		return
	}
	execMeta.parallelTiming = &parallelTimingState{baseline: captureParallelTimingBaseline(t)}
}

func observeHookedTestExecutionTiming(
	t *testing.T,
	execMeta *testExecutionMetadata,
	bodyDuration time.Duration,
	bodyEnd time.Time,
) testExecutionTiming {
	if execMeta == nil || execMeta.parallelTiming == nil {
		return testExecutionTiming{activeDuration: max(bodyDuration, 0), activeDurationOK: true}
	}
	return observeTestExecutionTiming(t, execMeta, parallelTimingBaseline{}, bodyDuration, bodyEnd)
}

func observeTestExecutionTiming(
	t *testing.T,
	execMeta *testExecutionMetadata,
	fallback parallelTimingBaseline,
	bodyDuration time.Duration,
	bodyEnd time.Time,
) testExecutionTiming {
	layout := getTestingInternalsLayout()
	if t == nil || layout == nil || layout.disabled || !layout.parallelStateOK {
		return testExecutionTiming{}
	}
	base := commonBaseForTest(t, layout)
	if base == nil {
		return testExecutionTiming{}
	}
	isParallel := *fieldPtr[bool](base, layout.common.isParallel)
	if !isParallel {
		return testExecutionTiming{activeDuration: max(bodyDuration, 0), activeDurationOK: true}
	}
	if !layout.parallelTimingOK {
		return testExecutionTiming{isParallel: true}
	}
	baseline := fallback
	if execMeta != nil && execMeta.parallelTiming != nil {
		baseline = execMeta.parallelTiming.baseline
	}
	if !baseline.valid {
		return testExecutionTiming{isParallel: true}
	}
	sample := sampleParallelTiming(t, baseline, bodyEnd)
	return calculateTestExecutionTiming(bodyDuration, sample)
}

func calculateTestExecutionTiming(bodyDuration time.Duration, sample parallelTimingSample) testExecutionTiming {
	timing := testExecutionTiming{isParallel: true}
	if !sample.pauseClockValid || sample.durationBeforePause < 0 {
		return timing
	}
	pauseRaw := sample.elapsedToResume - sample.durationBeforePause
	if pauseRaw < -parallelTimingSkewTolerance {
		return timing
	}
	pauseDuration := max(pauseRaw, 0)
	if pauseDuration > bodyDuration+parallelTimingSkewTolerance {
		return timing
	}
	timing.activeDuration = max(bodyDuration-pauseDuration, 0)
	timing.activeDurationOK = true

	if !sample.wallProjectionOK || sample.durationAfterResume < -parallelTimingSkewTolerance {
		return timing
	}
	timing.pauseEnd = sample.wallProjectionEnd.Add(-max(sample.durationAfterResume, 0))
	timing.pauseStart = timing.pauseEnd.Add(-pauseDuration)
	timing.pauseProjectionOK = !timing.pauseEnd.Before(timing.pauseStart)
	return timing
}

func reportTestExecutionTiming(test integrations.Test, timing testExecutionTiming) {
	if test == nil {
		return
	}
	if timing.isParallel {
		test.SetTag(constants.TestIsParallel, true)
	}
	if timing.activeDurationOK {
		test.SetTag(constants.TestActiveDuration, timing.activeDuration.Nanoseconds())
	}
	if !timing.isParallel || !timing.pauseProjectionOK {
		return
	}

	eventStart := test.StartTime()
	startOffset := timing.pauseStart.Sub(eventStart)
	endOffset := timing.pauseEnd.Sub(eventStart)
	if eventStart.IsZero() || startOffset < -parallelTimingSkewTolerance || endOffset < startOffset {
		return
	}
	startOffset = max(startOffset, 0)
	endOffset = max(endOffset, startOffset)
	test.SetTag(constants.TestParallelPauseStartOffset, startOffset.Nanoseconds())
	test.SetTag(constants.TestParallelPauseEndOffset, endOffset.Nanoseconds())
	test.SetTag(constants.TestParallelPauseDuration, (endOffset - startOffset).Nanoseconds())
	emitParallelWaitSpan(test, eventStart.Add(startOffset), eventStart.Add(endOffset))
}

func emitParallelWaitSpan(test integrations.Test, start, finish time.Time) {
	if test == nil || start.IsZero() || finish.Before(start) {
		return
	}
	span, _ := tracer.StartSpanFromContext(
		test.Context(),
		parallelWaitOperationName,
		tracer.ResourceName(parallelWaitResourceName),
		tracer.StartTime(start),
		tracer.Tag(ext.Component, "go-testing"),
		tracer.Tag(parallelWaitTag, true),
	)
	if span != nil {
		span.Finish(tracer.FinishTime(finish))
	}
}
