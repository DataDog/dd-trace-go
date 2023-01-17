// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

// Package traceprof contains shared logic for cross-cutting tracer/profiler features.
package traceprof

import (
	"sync"
	"sync/atomic"
)

// pprof labels applied by the tracer to show up in the profiler's profiles.
const (
	SpanID          = "span id"
	LocalRootSpanID = "local root span id"
	TraceEndpoint   = "trace endpoint"
)

// env variables used to control cross-cutting tracer/profiling features.
const (
	CodeHotspotsEnvVar  = "DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED" // aka code hotspots
	EndpointEnvVar      = "DD_PROFILING_ENDPOINT_COLLECTION_ENABLED"      // aka endpoint profiling
	EndpointCountEnvVar = "DD_PROFILING_ENDPOINT_COUNT_ENABLED"           // aka unit of work
)

// globalEndpointCounter is disabled by default. It gets enabled the first time
// a customers application calls profiler.Start().
var globalEndpointCounter = &EndpointCounter{enabled: 0, counts: map[string]int64{}}

// GlobalEndpointCounter returns the hitpoint endcounter that is shared between
// tracing and profiling to support the profiling unit-of-work feature.
func GlobalEndpointCounter() *EndpointCounter {
	return globalEndpointCounter
}

// NewEndpointCounter returns a new NewEndpointCounter.
func NewEndpointCounter() *EndpointCounter {
	return &EndpointCounter{enabled: 1, counts: map[string]int64{}}
}

// EndpointCounter counts hits per endpoint.
//
// TODO: This is a naive implementation with poor performance, e.g. 125ns/op in
// BenchmarkEndpointCounter on M1. We can do 10-20x better with something more
// complicated [1]. This will be done in a follow-up PR if we decide to enable this
// by default.
// [1] https://github.com/felixge/countermap/blob/main/xsync_map_counter_map.go
type EndpointCounter struct {
	enabled uint64
	mu      sync.Mutex
	counts  map[string]int64
}

// SetEnabled changes if endpoint counting is enabled or not. The previous
// value is returned.
func (e *EndpointCounter) SetEnabled(enabled bool) bool {
	oldVal := atomic.SwapUint64(&e.enabled, boolToUint64(enabled))
	return oldVal == 1
}

// Inc increments the hit counter for the given endpoint by 1. If endpoint
// counting is disabled, this method does nothing and is almost zero-cost.
func (e *EndpointCounter) Inc(endpoint string) {
	if atomic.LoadUint64(&e.enabled) == 0 {
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()
	e.counts[endpoint]++
}

// GetAndReset returns the hit counts for all endpoints and resets their counts
// back to 0.
func (e *EndpointCounter) GetAndReset() map[string]int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	counts := e.counts
	e.counts = make(map[string]int64)
	return counts
}

// boolToUint64 converts b to 0 if false or 1 if true.
func boolToUint64(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}
