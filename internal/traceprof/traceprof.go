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

var counts = NewEndpointCounter()

func GlobalEndpointCounter() *EndpointCounter {
	return counts
}

func NewEndpointCounter() *EndpointCounter {
	return &EndpointCounter{}
}

type EndpointCounter struct {
	m sync.Map
}

func (e *EndpointCounter) Inc(endpoint string) {
	valI, ok := e.m.Load(endpoint)
	var val int64
	if ok {
		val = valI.(int64)
	}
	val++
	e.m.Store(endpoint, val)
}

func (e *EndpointCounter) GetAndReset() map[string]int64 {
	m := map[string]int64{}
	e.m.Range(func(key, value interface{}) bool {
		m[key.(string)] = value.(int64)
		e.m.Delete(key)
		return true
	})
	return m
}
