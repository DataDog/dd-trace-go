// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2021 Datadog, Inc.

// Package traceprof contains shared logic for cross-cutting tracer/profiler features.
package traceprof

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func BenchmarkEndpointCounter(b *testing.B) {
	endpoints := make([]string, 10)
	for i := range endpoints {
		endpoints[i] = fmt.Sprintf("endpoint-%d", i)
	}

	for _, enabled := range []bool{true, false} {
		name := fmt.Sprintf("enabled=%v", enabled)
		b.Run(name, func(b *testing.B) {
			ec := NewEndpointCounter()
			ec.SetEnabled(enabled)

			b.RunParallel(func(p *testing.PB) {
				i := 0
				for p.Next() {
					ec.Inc(endpoints[i%len(endpoints)])
					i++
				}
			})

			if enabled {
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
			} else {
				require.Empty(b, ec.GetAndReset())
			}
		})
	}
}

func TestEndpointCounter(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		ec := NewEndpointCounter()
		ec.Inc("foo")
		ec.Inc("foo")
		ec.Inc("bar")
		require.Equal(t, map[string]uint64{"foo": 2, "bar": 1}, ec.GetAndReset())
		ec.Inc("foobar")
		require.Equal(t, map[string]uint64{"foobar": 1}, ec.GetAndReset())
		require.Equal(t, map[string]uint64{}, ec.GetAndReset())
	})

	t.Run("disabled", func(t *testing.T) {
		ec := NewEndpointCounter()
		ec.SetEnabled(false)
		ec.Inc("foo")
		ec.Inc("foo")
		ec.Inc("bar")
		require.Empty(t, ec.GetAndReset())
		ec.Inc("foobar")
		require.Empty(t, ec.GetAndReset())
		require.Empty(t, ec.GetAndReset())
	})
}
