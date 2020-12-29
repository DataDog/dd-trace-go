// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package profiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"runtime"
	"time"
)

type point struct {
	metric string
	value  float64
}

// MarshalJSON serialize points as array tuples
func (p point) MarshalJSON() ([]byte, error) {
	return json.Marshal([]interface{}{
		p.metric,
		p.value,
	})
}

type collectionTooFrequent struct {
	min      time.Duration
	observed time.Duration
}

func (e collectionTooFrequent) Error() string {
	return fmt.Sprintf("period between metrics collection is too small min=%v observed=%v", e.min, e.observed)
}

type metrics struct {
	collectedAt time.Time
	stats       runtime.MemStats
	compute     func(*runtime.MemStats, *runtime.MemStats, time.Duration) []point
}

func newMetrics() *metrics {
	return &metrics{
		compute: computeMetrics,
	}
}

func (m *metrics) reset(now time.Time) {
	m.collectedAt = now
	runtime.ReadMemStats(&m.stats)
}

func (m *metrics) report(now time.Time, buf *bytes.Buffer) error {
	period := now.Sub(m.collectedAt)

	if period < time.Second {
		// Profiler could be mis-configured to report more frequently than every second
		// or a system clock issue causes time to run backwards.
		// We can't emit valid metrics in either case.
		return collectionTooFrequent{min: time.Second, observed: period}
	}

	previousStats := m.stats
	m.reset(now)

	points := removeInvalid(m.compute(&previousStats, &m.stats, period))
	data, err := json.Marshal(points)

	if err != nil {
		return err
	}

	if _, err := buf.Write(data); err != nil {
		return err
	}

	return nil
}

func computeMetrics(prev *runtime.MemStats, curr *runtime.MemStats, period time.Duration) []point {
	return []point{
		{metric: "go_alloc_bytes_per_sec", value: rate(curr.TotalAlloc, prev.TotalAlloc, period/time.Second)},
		{metric: "go_allocs_per_sec", value: rate(curr.Mallocs, prev.Mallocs, period/time.Second)},
		{metric: "go_frees_per_sec", value: rate(curr.Frees, prev.Frees, period/time.Second)},
		{metric: "go_heap_growth_bytes_per_sec", value: rate(curr.HeapAlloc, prev.HeapAlloc, period/time.Second)},
	}
}

func rate(curr, prev uint64, period time.Duration) float64 {
	return float64(int64(curr)-int64(prev)) / float64(period)
}

// removeInvalid removes NaN and +/-Inf values as they can't be json-serialized
// This is an extra safety check to ensure we don't emit bad data in case of
// a metric computation coding error
func removeInvalid(points []point) (result []point) {
	for _, p := range points {
		if math.IsNaN(p.value) || math.IsInf(p.value, 0) {
			continue
		}
		result = append(result, p)
	}
	return result
}
