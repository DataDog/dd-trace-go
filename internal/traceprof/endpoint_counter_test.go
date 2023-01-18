// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package traceprof

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestEndpointCounter verifies the basic behavior of the EndpointCounter
// without concurrency, see BenchmarkEndpointCounter.
func TestEndpointCounter(t *testing.T) {
	t.Run("fixed limit", func(t *testing.T) {
		ec := NewEndpointCounter(2)
		ec.Inc("foo")
		ec.Inc("foo")
		ec.Inc("bar")
		ec.Inc("baz") // Exceeds limit, should be ignore.d
		require.Equal(t, map[string]uint64{"foo": 2, "bar": 1}, ec.GetAndReset())
		ec.Inc("foobar")
		require.Equal(t, map[string]uint64{"foobar": 1}, ec.GetAndReset())
		require.Equal(t, map[string]uint64{}, ec.GetAndReset())
	})

	t.Run("no limit", func(t *testing.T) {
		ec := NewEndpointCounter(-1)
		for i := 0; i < 100; i++ {
			ec.Inc(fmt.Sprint(i))
		}
		require.Equal(t, 100, len(ec.GetAndReset()))
	})

	t.Run("disabled", func(t *testing.T) {
		ec := NewEndpointCounter(-1)
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

// BenchmarkEndpointCounter tests the lock contention overhead of the
// EndpointCounter. It also verifies that the implementation is producing the
// right results under high load.
func BenchmarkEndpointCounter(b *testing.B) {
	// Create 10 endpoint names
	endpoints := make([]string, 10)
	for i := range endpoints {
		endpoints[i] = fmt.Sprintf("endpoint-%d", i)
	}

	// Benchmark with endpoint counting enabled and disabled.
	for _, enabled := range []bool{true, false} {
		name := fmt.Sprintf("enabled=%v", enabled)
		b.Run(name, func(b *testing.B) {
			// Create a new endpoint counter and enable or disable it
			ec := NewEndpointCounter(len(endpoints))
			ec.SetEnabled(enabled)

			// Run GOMAXPROCS goroutines that loop over the endpoints and increment
			// their count by one.
			b.RunParallel(func(p *testing.PB) {
				i := 0
				for p.Next() {
					ec.Inc(endpoints[i%len(endpoints)])
					i++
				}
			})

			// If endpoint counting is disabled, we expect an empty result.
			if !enabled {
				require.Empty(b, ec.GetAndReset())
				return
			}

			// Verify that the endpoint counts are plausible. Based on the
			// RunParallel block above, we know that the each endpoint should have
			// received a higher or equal count than the endpoint after it.
			counts := ec.GetAndReset()
			for i := 0; i < len(endpoints)-1; i++ {
				endpoint := endpoints[i]
				nextEndpoint := endpoints[i+1]
				require.GreaterOrEqual(b, counts[endpoint], counts[nextEndpoint], "endpoint=%s nextEndpoint=%s", endpoint, nextEndpoint)
			}
		})
	}
}
