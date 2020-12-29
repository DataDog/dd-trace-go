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

func TestMetricsCompute(t *testing.T) {
	prev := runtime.MemStats{
		TotalAlloc: 100,
		Mallocs:    10,
		Frees:      2,
		HeapAlloc:  75,
	}
	curr := runtime.MemStats{
		TotalAlloc: 150,
		Mallocs:    14,
		Frees:      30,
		HeapAlloc:  50,
	}

	points := computeMetrics(&prev, &curr, 10*time.Second)
	assert.Equal(t, []point{
		{metric: "go_alloc_bytes_per_sec", value: 5},
		{metric: "go_allocs_per_sec", value: 0.4},
		{metric: "go_frees_per_sec", value: 2.8},
		{metric: "go_heap_growth_bytes_per_sec", value: -2.5},
	}, points)

	assert.Equal(t, []point{
		{metric: "go_alloc_bytes_per_sec", value: 0},
		{metric: "go_allocs_per_sec", value: 0},
		{metric: "go_frees_per_sec", value: 0},
		{metric: "go_heap_growth_bytes_per_sec", value: 0},
	}, computeMetrics(&prev, &prev, 10*time.Second), "identical memstats")
}

func TestMetricsReport(t *testing.T) {
	now := now()
	var err error
	var buf bytes.Buffer
	m := newTestMetrics(now)

	m.compute = func(prev *runtime.MemStats, curr *runtime.MemStats, periodInSec time.Duration) []point {
		return []point{{metric: "metric_name", value: 1.1}}
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

func TestMetricsRemoveInvalid(t *testing.T) {
	assert.Equal(t,
		[]point{
			{metric: "bar", value: 1.0},
		},
		removeInvalid(
			[]point{
				{metric: "foo", value: math.NaN()},
				{metric: "bar", value: 1.0},
				{metric: "fiz", value: math.Inf(1)},
				{metric: "buz", value: math.Inf(-1)},
			}))
}
