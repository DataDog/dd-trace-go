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
	counts := map[string]*atomic.Int64{}
	ec := &EndpointCounter{}
	ec.counts.Store(counts)
	return ec
}

type EndpointCounter struct {
	lock   sync.RWMutex
	counts atomic.Value
}

func (e *EndpointCounter) Inc(endpoint string) {
	counts := e.counts.Load().(map[string]*atomic.Int64)
	val, ok := counts[endpoint]
	if ok {
		val.Add(1)
		return
	}

	e.lock.Lock()
	defer e.lock.Unlock()

	newCounts := make(map[string]*atomic.Int64)
	for k, v := range counts {
		newCounts[k] = v
	}
	val = &atomic.Int64{}
	val.Add(1)
	newCounts[endpoint] = val
	e.counts.Store(newCounts)
}

func (e *EndpointCounter) GetAndReset() map[string]int64 {
	e.lock.Lock()
	defer e.lock.Unlock()

	counts := e.counts.Load().(map[string]*atomic.Int64)
	retCounts := map[string]int64{}
	for k, v := range counts {
		retCounts[k] = v.Load()
	}

	e.counts.Store(map[string]*atomic.Int64{})
	return retCounts
}
