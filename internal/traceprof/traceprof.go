// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

// Package traceprof contains shared logic for cross-cutting tracer/profiler features.
package traceprof

import (
	"sync"
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
	return &EndpointCounter{counts: map[string]int64{}}
}

// EndpointCounter counts hits per endpoint.
//
// TODO: This is a naive implementation with poor performance. We can do 10-20x
// better with something more complicated. This will be done in a follow-up PR
// if we decide to enable this by default.
type EndpointCounter struct {
	mu     sync.Mutex
	counts map[string]int64
}

// Inc increments the hit counter for the given endpoint by 1.
func (e *EndpointCounter) Inc(endpoint string) {
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
