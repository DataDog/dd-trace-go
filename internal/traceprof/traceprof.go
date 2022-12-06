// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

// Package traceprof contains shared logic for cross-cutting tracer/profiler features.
package traceprof

import (
	"gopkg.in/DataDog/dd-trace-go.v1/internal/atomic" // polyfill for sync/atomic
)

// pprof labels applied by the tracer to show up in the profiler's profiles.
const (
	SpanID          = "span id"
	LocalRootSpanID = "local root span id"
	TraceEndpoint   = "trace endpoint"
)

// env variables used to control cross-cutting tracer/profiling features.
const (
	EndpointEnvVar     = "DD_PROFILING_ENDPOINT_COLLECTION_ENABLED"
	CodeHotspotsEnvVar = "DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED"
)

var counts = NewEndpointCounter()

// GlobalEndpointCounter returns the hitpoint endcounter that is shared between
// tracing and profiling to support the profiling unit-of-work feature.
func GlobalEndpointCounter() *EndpointCounter {
	return counts
}

// NewEndpointCounter returns a new NewEndpointCounter.
func NewEndpointCounter() *EndpointCounter {
	counts := map[string]*atomic.Int64{}
	ec := &EndpointCounter{}
	ec.counts.Store(&counts)
	return ec
}

// EndpointCounter is an optimized map[string]int64 data structure that assumes
// that new keys are rarely added, but existing values are frequently
// incremented. This is motivated by the fact that it sits in the hot path of
// tracer span creation. The implementation uses optimistic concurrency control
// and seems to perform well compared to other approaches that were attempted:
//
// - atomic.Value (optimistic): 13.5ns/op
// - atomic.Value: 15.7ns/op (buggy!)
// - sync.RWMutex: 46.7ns/op (buggy!)
// - sync.Map: 46.5ns/op (buggy!)
// - sync.Mutex: 77.2ns/op
//
// Please run BenchmarkEndpointCounter if you think about changing the
// implementation. It's much easier to make this slow and/or broken than fast
// and correct.
//
// See https://github.com/DataDog/dd-trace-go/pull/1552 for full details.
type EndpointCounter struct {
	counts atomic.Value
}

// Inc increments the hit counter for the given endpoint by 1.
func (e *EndpointCounter) Inc(endpoint string) {
	for {
		oldCounts := e.counts.Load().(*map[string]*atomic.Int64)
		val, ok := (*oldCounts)[endpoint]
		if ok {
			val.Add(1)
			return
		}

		newCounts := make(map[string]*atomic.Int64)
		for k, v := range *oldCounts {
			newCounts[k] = v
		}
		val = &atomic.Int64{}
		val.Add(1)
		newCounts[endpoint] = val
		if e.counts.CompareAndSwap(oldCounts, &newCounts) {
			return
		}
	}
}

// GetAndReset returns the hit counts for all endpoints and resets their counts
// back to 0.
func (e *EndpointCounter) GetAndReset() map[string]int64 {
	for {
		oldCounts := e.counts.Load().(*map[string]*atomic.Int64)
		retCounts := map[string]int64{}
		for k, v := range *oldCounts {
			retCounts[k] = v.Load()
		}

		newCounts := map[string]*atomic.Int64{}
		if e.counts.CompareAndSwap(oldCounts, &newCounts) {
			return retCounts
		}
	}
}
