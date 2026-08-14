// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

func exerciseParallelTiming(t *testing.T) {
	t.Helper()

	wallEnd := time.Now()
	timing := calculateTestExecutionTiming(20*time.Millisecond, parallelTimingSample{
		preDuration:       3 * time.Millisecond,
		baselineToResume:  11 * time.Millisecond,
		postResume:        4 * time.Millisecond,
		pauseClockValid:   true,
		wallProjectionEnd: wallEnd,
		wallProjectionOK:  true,
	})
	require.True(t, timing.isParallel)
	require.True(t, timing.activeDurationOK)
	require.Equal(t, 12*time.Millisecond, timing.activeDuration)
	require.Equal(t, wallEnd.Add(-4*time.Millisecond), timing.pauseEnd)
	require.Equal(t, wallEnd.Add(-12*time.Millisecond), timing.pauseStart)
	require.Equal(t, 8*time.Millisecond, timing.pauseEnd.Sub(timing.pauseStart))
	require.True(t, timing.pauseProjectionOK)

	withinSkew := calculateTestExecutionTiming(time.Millisecond, parallelTimingSample{
		preDuration:      time.Millisecond,
		baselineToResume: time.Millisecond - parallelTimingSkewTolerance/2,
		pauseClockValid:  true,
	})
	require.True(t, withinSkew.activeDurationOK)
	require.Equal(t, time.Millisecond, withinSkew.activeDuration)

	invalidPause := calculateTestExecutionTiming(time.Millisecond, parallelTimingSample{
		preDuration:      3 * time.Millisecond,
		baselineToResume: time.Millisecond,
		pauseClockValid:  true,
	})
	require.False(t, invalidPause.activeDurationOK)
	require.False(t, invalidPause.pauseProjectionOK)

	invalidProjection := calculateTestExecutionTiming(20*time.Millisecond, parallelTimingSample{
		preDuration:      3 * time.Millisecond,
		baselineToResume: 11 * time.Millisecond,
		postResume:       -2 * time.Millisecond,
		pauseClockValid:  true,
		wallProjectionOK: true,
	})
	require.True(t, invalidProjection.activeDurationOK)
	require.Equal(t, 12*time.Millisecond, invalidProjection.activeDuration)
	require.False(t, invalidProjection.pauseProjectionOK)

	policyTiming := calculateTestExecutionTiming(6*time.Second, parallelTimingSample{
		preDuration:      time.Second,
		baselineToResume: 3 * time.Second,
		pauseClockValid:  true,
	})
	require.True(t, policyTiming.activeDurationOK)
	require.Equal(t, 4*time.Second, policyTiming.activeDuration)
	settings := &civisibilitynet.SettingsResponseData{}
	settings.EarlyFlakeDetection.SlowTestRetries.FiveS = 11
	settings.EarlyFlakeDetection.SlowTestRetries.TenS = 5
	activeRetries, activeOK := efdRetryCountForDuration(settings, policyTiming.activeDuration)
	wallRetries, wallOK := efdRetryCountForDuration(settings, 6*time.Second)
	require.True(t, activeOK)
	require.True(t, wallOK)
	require.EqualValues(t, 11, activeRetries)
	require.EqualValues(t, 5, wallRetries)

	hookedNonParallel := observeHookedTestExecutionTiming(nil, &testExecutionMetadata{}, 2*time.Millisecond, time.Time{})
	require.False(t, hookedNonParallel.isParallel)
	require.True(t, hookedNonParallel.activeDurationOK)
	require.Equal(t, 2*time.Millisecond, hookedNonParallel.activeDuration)
	hookedUnsupportedParallel := observeHookedTestExecutionTiming(nil, &testExecutionMetadata{
		parallelTiming: &parallelTimingState{},
	}, 2*time.Millisecond, time.Time{})
	require.True(t, hookedUnsupportedParallel.isParallel)
	require.False(t, hookedUnsupportedParallel.activeDurationOK)

	event := newProcessRetryRecordingTestForTesting("parallel-timing")
	initializeTestExecutionTiming(event)
	require.EqualValues(t, 0, event.tags[constants.TestActiveDuration])
	require.Equal(t, false, event.tags[constants.TestIsParallel])
	applyTestExecutionTiming(event, testExecutionTiming{
		isParallel:       true,
		activeDuration:   12 * time.Millisecond,
		activeDurationOK: true,
	})
	require.EqualValues(t, (12 * time.Millisecond).Nanoseconds(), event.tags[constants.TestActiveDuration])
	require.Equal(t, true, event.tags[constants.TestIsParallel])
	require.NotContains(t, event.tags, constants.TestParallelPauseDuration)

	policyAttempt := processRetryAttemptResult{Result: processRetryResult{
		DurationNanos: (7 * time.Second).Nanoseconds(),
		DurationValid: true,
	}}
	policyDuration, ok := processRetryPolicyDuration(policyAttempt)
	require.True(t, ok)
	require.Equal(t, 7*time.Second, policyDuration)
	policyAttempt.Result.DurationValid = false
	policyDuration, ok = processRetryPolicyDuration(policyAttempt)
	require.False(t, ok)
	require.Zero(t, policyDuration)

}

func BenchmarkParallelTimingBaselineCapture(b *testing.B) {
	var t testing.T
	b.ReportAllocs()
	for b.Loop() {
		_ = captureParallelTimingBaseline(&t)
	}
}

func BenchmarkParallelTimingHookedNonParallel(b *testing.B) {
	meta := &testExecutionMetadata{}
	b.ReportAllocs()
	for b.Loop() {
		_ = observeHookedTestExecutionTiming(nil, meta, time.Nanosecond, time.Time{})
	}
}
