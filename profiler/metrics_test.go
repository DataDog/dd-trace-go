// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package profiler

import (
	"bytes"
	"math"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func newTestMetrics(now time.Time) *metrics {
	m := newMetrics()
	m.reset(now)
	return m
}

func valsRing(vals ...time.Duration) [256]uint64 {
	var ring [256]uint64
	for i := 0; i < len(vals) && i < 256; i++ {
		ring[i] = uint64(vals[i])
	}
	return ring
}

func timeRing(vals ...time.Time) [256]uint64 {
	var ring [256]uint64
	for i := 0; i < len(vals) && i < 256; i++ {
		ring[i] = uint64(vals[i].UnixNano())
	}
	return ring
}

func TestMetricsCompute(t *testing.T) {
	now := now()
	prev := metricsSnapshot{
		NumGoroutine: 23,
		MemStats: runtime.MemStats{
			TotalAlloc:   100,
			Mallocs:      10,
			Frees:        2,
			HeapAlloc:    75,
			NumGC:        1,
			PauseTotalNs: uint64(2 * time.Second),
			PauseEnd:     timeRing(now.Add(-11 * time.Second)),
			PauseNs:      valsRing(2 * time.Second),
		},
	}
	curr := metricsSnapshot{
		NumGoroutine: 42,
		MemStats: runtime.MemStats{
			TotalAlloc:   150,
			Mallocs:      14,
			Frees:        30,
			HeapAlloc:    50,
			NumGC:        3,
			PauseTotalNs: uint64(3 * time.Second),
			PauseEnd:     timeRing(now.Add(-11*time.Second), now.Add(-9*time.Second), now.Add(-time.Second)),
			PauseNs:      valsRing(time.Second, time.Second/2, time.Second/2),
		},
	}

	assert.Equal(t,
		[]point{
			{metric: "go_alloc_bytes_per_sec", value: 5},
			{metric: "go_allocs_per_sec", value: 0.4},
			{metric: "go_frees_per_sec", value: 2.8},
			{metric: "go_heap_growth_bytes_per_sec", value: -2.5},
			{metric: "go_gcs_per_sec", value: 0.2},
			{metric: "go_gc_pause_time", value: 0.1}, // % of time spent paused
			{metric: "go_max_gc_pause_time", value: float64(time.Second / 2)},
			{metric: "go_num_goroutine", value: 42},
		},
		computeMetrics(&prev, &curr, 10*time.Second, now))

	assert.Equal(t,
		[]point{
			{metric: "go_alloc_bytes_per_sec", value: 0},
			{metric: "go_allocs_per_sec", value: 0},
			{metric: "go_frees_per_sec", value: 0},
			{metric: "go_heap_growth_bytes_per_sec", value: 0},
			{metric: "go_gcs_per_sec", value: 0},
			{metric: "go_gc_pause_time", value: 0},
			{metric: "go_max_gc_pause_time", value: 0},
			{metric: "go_num_goroutine", value: 23},
		},
		computeMetrics(&prev, &prev, 10*time.Second, now),
		"identical memstats")
}

func TestMetricsMaxPauseNs(t *testing.T) {
	start := now()

	assert.Equal(t, uint64(0),
		maxPauseNs(&runtime.MemStats{}, start),
		"max is 0 for empty pause buffers")

	assert.Equal(t, uint64(time.Second),
		maxPauseNs(
			&runtime.MemStats{
				NumGC:    3,
				PauseNs:  valsRing(time.Minute, time.Second, time.Millisecond),
				PauseEnd: timeRing(start.Add(-1), start, start.Add(1)),
			},
			start,
		),
		"only values in period are considered")

	assert.Equal(t, uint64(time.Minute),
		maxPauseNs(
			&runtime.MemStats{
				NumGC:    1000,
				PauseNs:  valsRing(time.Second, time.Minute, time.Millisecond),
				PauseEnd: timeRing(),
			},
			time.Unix(0, 0),
		),
		"should terminate if all values are in period")
}

func TestMetricsReport(t *testing.T) {
	now := now()
	var err error
	var buf bytes.Buffer
	m := newTestMetrics(now)

	m.compute = func(_ *metricsSnapshot, _ *metricsSnapshot, _ time.Duration, _ time.Time) []point {
		return []point{
			{metric: "metric_name", value: 1.1},
			{metric: "does_not_include_NaN", value: math.NaN()},
			{metric: "does_not_include_+Inf", value: math.Inf(1)},
			{metric: "does_not_include_-Inf", value: math.Inf(-1)},
		}
	}

	err = m.report(now.Add(time.Second), &buf)
	assert.NoError(t, err)
	assert.Equal(t, "[[\"metric_name\",1.1]]", buf.String())
}

func TestMetricsCollectFrequency(t *testing.T) {
	now := now()
	var err error
	var buf bytes.Buffer
	m := newTestMetrics(now)

	err = m.report(now.Add(-time.Second), &buf)
	assert.Error(t, err, "collection call times must be monotonically increasing")
	assert.Empty(t, buf)

	err = m.report(now.Add(time.Second-1), &buf)
	assert.Error(t, err, "must be at least one second between collection calls")
	assert.Empty(t, buf)

	err = m.report(now.Add(time.Second), &buf)
	assert.NoError(t, err, "one second between calls should work")
	assert.NotEmpty(t, buf)
}
