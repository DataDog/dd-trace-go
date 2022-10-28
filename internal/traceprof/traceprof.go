// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

// Package traceprof contains shared logic for cross-cutting tracer/profiler features.
package traceprof

import "sync"

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

var counts = &EndpointCounter{counts: map[string]int64{}}

func GlobalEndpointCounter() *EndpointCounter {
	return counts
}

func NewEndpointCounter() *EndpointCounter {
	return &EndpointCounter{counts: map[string]int64{}}
}

type EndpointCounter struct {
	lock   sync.Mutex
	counts map[string]int64
}

func (e *EndpointCounter) Inc(endpoint string) {
	e.lock.Lock()
	defer e.lock.Unlock()
	e.counts[endpoint]++
}

func (e *EndpointCounter) GetAndReset() map[string]int64 {
	e.lock.Lock()
	defer e.lock.Unlock()
	counts := e.counts
	e.counts = make(map[string]int64, len(e.counts))
	return counts
}
