// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package traceprof

import (
	"sync"
	"sync/atomic"

	"github.com/puzpuzpuz/xsync/v3"
)

// globalEndpointCounter is shared between the profiler and the tracer.
var globalEndpointCounter = (func() *EndpointCounter {
	// Create endpoint counter with arbitrary limit.
	// The pathological edge case would be a service with a high rate (10k/s) of
	// short (100ms) spans with unique endpoints (resource names). Over a 60s
	// period this would grow the map to 600k items which may cause noticable
	// memory, GC overhead and lock contention overhead. The pprof endpoint
	// labels are less problematic since there will only be 1000 spans in-flight
	// on average. Using a limit of 1000 will result in a similar overhead of
	// this features compared to the pprof labels. It also seems like a
	// reasonable upper bound for the number of endpoints a normal application
	// may service in a 60s period.
	ec := NewEndpointCounter(1000)
	// Disabled by default ensures almost-zero overhead for tracing users that
	// don't have the profiler turned on.
	ec.SetEnabled(false)
	return ec
})()

// GlobalEndpointCounter returns the endpoint counter that is shared between
// tracing and profiling to support the unit of work feature.
func GlobalEndpointCounter() *EndpointCounter {
	return globalEndpointCounter
}

// NewEndpointCounter returns a new NewEndpointCounter that will track hit
// counts for up to limit endpoints. A limit of <= 0 indicates no limit.
func NewEndpointCounter(limit int) *EndpointCounter {
	return &EndpointCounter{
		enabled: 1,
		limit:   limit,
		counts:  xsync.NewMapOf[string, *xsync.Counter](),
	}
}

// EndpointCounter counts hits per endpoint.
//
// TODO: This is a naive implementation with poor performance, e.g. 125ns/op in
// BenchmarkEndpointCounter on M1. We can do 10-20x better with something more
// complicated [1]. This will be done in a follow-up PR.
// [1] https://github.com/felixge/countermap/blob/main/xsync_map_counter_map.go
type EndpointCounter struct {
	enabled uint64
	mu      sync.Mutex
	counts  *xsync.MapOf[string, *xsync.Counter]
	limit   int
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
	// Fast-path return if endpoint counter is disabled.
	if atomic.LoadUint64(&e.enabled) == 0 {
		return
	}

	count, ok := e.counts.Load(endpoint)
	if !ok {
		// If we haven't seen this endpoint yet, add it. Another
		// goroutine might be racing to add it, so use
		// LoadOrStore: we'll only store if this goroutine
		// "wins" the race to add it, and we'll have a small
		// wasted allocation if the goroutine "loses" the race.
		// In microbenchmarks this seems to be faster than a
		// single LoadOrCompute
		// TODO: our tests pass whether or not we re-set ok
		// here. re-setting seems right because we need to check
		// whether we hit the limit _if_ we added
		// Can we test more thoroughly?
		count, ok = e.counts.LoadOrStore(endpoint, xsync.NewCounter())
	}
	if !ok && e.limit > 0 && e.counts.Size() > e.limit {
		// If we went over the limit when we added the counter,
		// delete it.
		// TODO: this is racy: another goroutine might also add
		// a different endpoint and exceed the limit _after_
		// this one, yet we check the size first end delete our
		// endpoint _before_ the other goroutine.
		// Does it matter in practice?
		e.counts.Delete(endpoint)
		return
	}
	// Increment the endpoint count
	count.Inc()
	return
}

// GetAndReset returns the hit counts for all endpoints and resets their counts
// back to 0.
func (e *EndpointCounter) GetAndReset() map[string]uint64 {
	// Try to right-size the allocation
	counts := make(map[string]uint64, e.counts.Size())
	e.counts.Range(func(key string, _ *xsync.Counter) bool {
		// TODO: in https://github.com/felixge/countermap/blob/main/xsync_map_counter_map.go,
		// Felix reads the input value and then deletes the key.
		// A LoadAndDelete ensures we don't miss updates to the
		// count for the endpoint: either we get them here or in
		// the next cycle. We could also consider not deleting
		// the value, but instead reset it, if we aren't at the
		// size limit? Would be nice if xsync.Counter had a
		// Swap operation for that.
		v, ok := e.counts.LoadAndDelete(key)
		if ok {
			// ok should always be true unless we're calling
			// GetAndReset concurrently somewhere else...
			counts[key] = uint64(v.Value())
		}
		return true
	})
	return counts
}

// boolToUint64 converts b to 0 if false or 1 if true.
func boolToUint64(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}
