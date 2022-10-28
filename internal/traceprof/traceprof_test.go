// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

// Package traceprof contains shared logic for cross-cutting tracer/profiler features.
package traceprof

import (
	"fmt"
	"testing"
)

func BenchmarkEndpointCounter(b *testing.B) {
	endpoints := make([]string, 10)
	for i := range endpoints {
		endpoints[i] = fmt.Sprintf("endpoint-%d", i)
	}
	ec := NewEndpointCounter()

	b.RunParallel(func(p *testing.PB) {
		i := 0
		for p.Next() {
			ec.Inc(endpoints[i%len(endpoints)])
			i++
		}
	})

	// The benchmark above is constructed so that endpoints should exhibit
	// monotonically decreasing hit counts. If this invariant is violated
	// the implementation is buggy and we fail the benchmark.
	counts := ec.GetAndReset()
	for i := 1; i < len(endpoints); i++ {
		endpoint := endpoints[i]
		prevEndpoint := endpoints[i-1]
		if counts[endpoint] > counts[prevEndpoint] {
			b.Fatalf("%q: %d > %q:%d", endpoint, counts[endpoint], prevEndpoint, counts[prevEndpoint])
		}
	}
}
