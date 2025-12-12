// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"
	"github.com/stretchr/testify/require"
)

func BenchmarkAgentTraceWriterAdd(b *testing.B) {
	traceSizes := []struct {
		name     string
		numSpans int
	}{
		{"1span", 1},
		{"5spans", 5},
		{"10spans", 10},
		{"50spans", 50},
	}

	for _, size := range traceSizes {
		b.Run(size.name, func(b *testing.B) {
			var statsd statsdtest.TestStatsdClient
			cfg, err := newTestConfig()
			require.NoError(b, err)

			writer := newAgentTraceWriter(cfg, nil, &statsd)

			trace := make([]*Span, size.numSpans)
			for i := 0; i < size.numSpans; i++ {
				trace[i] = newBasicSpan("benchmark-span")
			}

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				writer.add(trace)
			}
		})
	}
}

func BenchmarkAgentTraceWriterFlush(b *testing.B) {
	var statsd statsdtest.TestStatsdClient
	cfg, err := newTestConfig()
	require.NoError(b, err)

	writer := newAgentTraceWriter(cfg, nil, &statsd)
	trace := []*Span{newBasicSpan("flush-test")}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		writer.add(trace)
		writer.flush()
		writer.wg.Wait()
	}
}

func BenchmarkAgentTraceWriterConcurrent(b *testing.B) {
	concurrencyLevels := []int{1, 2, 4, 8}

	for _, concurrency := range concurrencyLevels {
		b.Run(fmt.Sprintf("concurrency_%d", concurrency), func(b *testing.B) {
			var statsd statsdtest.TestStatsdClient
			cfg, err := newTestConfig()
			require.NoError(b, err)

			writer := newAgentTraceWriter(cfg, nil, &statsd)
			trace := []*Span{newBasicSpan("concurrent-test")}

			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				var wg sync.WaitGroup

				for j := 0; j < concurrency; j++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						writer.add(trace)
					}()
				}

				wg.Wait()
			}
		})
	}
}

func BenchmarkAgentTraceWriterStats(b *testing.B) {
	var statsd statsdtest.TestStatsdClient
	cfg, err := newTestConfig()
	require.NoError(b, err)

	writer := newAgentTraceWriter(cfg, nil, &statsd)

	for i := 0; i < 10; i++ {
		trace := []*Span{newBasicSpan("stats-test")}
		writer.add(trace)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		writer.mu.Lock()
		stats := writer.payload.stats()
		writer.mu.Unlock()
		_ = stats.size
		_ = stats.itemCount
	}
}
