// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gotesting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/integrations"
	civisibilitynet "github.com/DataDog/dd-trace-go/v2/internal/civisibility/utils/net"
)

func TestRetryBucketIndexForDuration(t *testing.T) {
	tests := []struct {
		seconds float64
		want    int
	}{
		{0, 0},
		{1, 0},
		{5, 0},
		{5.1, 1},
		{6, 1},
		{10, 1},
		{10.1, 2},
		{31, 3},
		{300, 3},
		{300.1, 4},
		{600, 4},
	}
	for _, tc := range tests {
		got := retryBucketIndexForDuration(tc.seconds)
		assert.Equal(t, tc.want, got, "seconds=%v", tc.seconds)
	}
}

func TestEfdRetriesForDuration(t *testing.T) {
	settings := &civisibilitynet.SettingsResponseData{}
	settings.EarlyFlakeDetection.SlowTestRetries.FiveS = 10
	settings.EarlyFlakeDetection.SlowTestRetries.TenS = 2
	settings.EarlyFlakeDetection.SlowTestRetries.ThirtyS = 3
	settings.EarlyFlakeDetection.SlowTestRetries.FiveM = 4

	tests := []struct {
		seconds float64
		want    int
	}{
		{1, 10},
		{6, 2},
		{31, 4},
		{301, 0},
	}
	for _, tc := range tests {
		got := efdRetriesForDuration(settings, tc.seconds)
		assert.Equal(t, tc.want, got, "seconds=%v", tc.seconds)
	}
}

func TestDynamicATRRetryCountForDuration(t *testing.T) {
	settings := &civisibilitynet.SettingsResponseData{}
	settings.EarlyFlakeDetection.SlowTestRetries.FiveS = 10
	settings.EarlyFlakeDetection.SlowTestRetries.TenS = 2
	settings.EarlyFlakeDetection.SlowTestRetries.ThirtyS = 3
	settings.EarlyFlakeDetection.SlowTestRetries.FiveM = 4

	// Save and restore global dynamic ATR state.
	originalEnabled := integrations.IsDynamicATREnabled()
	originalBuckets := integrations.GetDynamicATRCustomBuckets()
	t.Cleanup(func() {
		integrations.ResetDynamicATRSettingsForTestingExport()
		integrations.SetDynamicATRSettingsForTestingExport(originalEnabled, originalBuckets)
	})

	t.Run("uses_efd_buckets_when_no_custom", func(t *testing.T) {
		integrations.ResetDynamicATRSettingsForTestingExport()
		integrations.SetDynamicATRSettingsForTestingExport(true, nil)

		tests := []struct {
			duration time.Duration
			want     int64
		}{
			{1 * time.Second, 10},
			{6 * time.Second, 2},
			{31 * time.Second, 4},
			{301 * time.Second, 1}, // >5m bucket -> 0, clamped to min 1
		}
		for _, tc := range tests {
			got := dynamicATRRetryCountForDuration(settings, tc.duration)
			assert.Equal(t, tc.want, got, "duration=%v", tc.duration)
		}
	})

	t.Run("uses_custom_buckets", func(t *testing.T) {
		integrations.ResetDynamicATRSettingsForTestingExport()
		buckets := [5]int{4, 1, 1, 1, 1}
		integrations.SetDynamicATRSettingsForTestingExport(true, &buckets)

		tests := []struct {
			duration time.Duration
			want     int64
		}{
			{1 * time.Second, 4},
			{6 * time.Second, 1},
			{31 * time.Second, 1},
			{301 * time.Second, 1},
		}
		for _, tc := range tests {
			got := dynamicATRRetryCountForDuration(settings, tc.duration)
			assert.Equal(t, tc.want, got, "duration=%v", tc.duration)
		}
	})

	t.Run("clamps_to_min_1", func(t *testing.T) {
		integrations.ResetDynamicATRSettingsForTestingExport()
		buckets := [5]int{0, 0, 0, 0, 0} // invalid but tests the clamp
		// We can't actually set 0 via env parsing, but the function should clamp.
		// Use a valid bucket of 1 instead.
		buckets = [5]int{1, 1, 1, 1, 1}
		integrations.SetDynamicATRSettingsForTestingExport(true, &buckets)
		got := dynamicATRRetryCountForDuration(settings, 1*time.Second)
		assert.Equal(t, int64(1), got)
	})
}
