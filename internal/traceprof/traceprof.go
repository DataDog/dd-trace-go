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
	EndpointEnvVar     = "DD_PROFILING_ENDPOINT_COLLECTION_ENABLED"
	CodeHotspotsEnvVar = "DD_PROFILING_CODE_HOTSPOTS_COLLECTION_ENABLED"
)

var counts = NewEndpointCounter()

func GlobalEndpointCounter() *EndpointCounter {
	return counts
}

func NewEndpointCounter() *EndpointCounter {
	return &EndpointCounter{counts: map[string]*atomic.Int64{}}
}

type EndpointCounter struct {
	lock   sync.RWMutex
	counts map[string]*atomic.Int64
}

func (e *EndpointCounter) Inc(endpoint string) {
	e.lock.RLock()
	val, ok := e.counts[endpoint]
	e.lock.RUnlock()

	if !ok {
		e.lock.Lock()
		defer e.lock.Unlock()
		val = &atomic.Int64{}
		e.counts[endpoint] = val
	}

	val.Add(1)
}

func (e *EndpointCounter) GetAndReset() map[string]int64 {
	counts := map[string]int64{}
	e.lock.Lock()
	defer e.lock.Unlock()
	for k, v := range e.counts {
		counts[k] = v.Load()
	}
	e.counts = make(map[string]*atomic.Int64)
	return counts
}
