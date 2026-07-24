// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
)

// BenchmarkInjectOnly measures the cost of a single Inject call with no concurrency.
func BenchmarkInjectOnly(b *testing.B) {
	trc, err := newTracer(withTransport(newDummyTransport()), withNoopStats())
	if err != nil {
		b.Fatal(err)
	}
	defer trc.Stop()
	root := trc.StartSpan("server")
	ctx := root.Context()
	carrier := HTTPHeadersCarrier(http.Header{})
	b.ResetTimer()
	for range b.N {
		_ = trc.Inject(ctx, carrier)
	}
	root.Finish()
}

// BenchmarkTraceMuContention measures raw trace.mu lock/unlock throughput as a
// baseline for other contention benchmarks.
func BenchmarkTraceMuContention(b *testing.B) {
	for _, n := range []int{1, 2, 4, 8, 16, 32, 64, 128} {
		b.Run(fmt.Sprintf("goroutines_%03d", n), func(b *testing.B) {
			t := newTrace()
			var counter atomic.Int64
			var wg sync.WaitGroup
			b.ResetTimer()
			for range n {
				wg.Go(func() {
					for counter.Add(1) <= int64(b.N) {
						t.mu.Lock()
						t.mu.Unlock()
					}
				})
			}
			wg.Wait()
		})
	}
}

// BenchmarkInjectMultiG measures Inject throughput as goroutine count increases.
// ns/op rising with N indicates serialisation; falling indicates parallel scaling.
func BenchmarkInjectMultiG(b *testing.B) {
	for _, n := range []int{1, 2, 4, 8, 16, 32, 64} {
		b.Run(fmt.Sprintf("goroutines_%03d", n), func(b *testing.B) {
			trc, err := newTracer(withTransport(newDummyTransport()), withNoopStats())
			if err != nil {
				b.Fatal(err)
			}
			defer trc.Stop()
			root := trc.StartSpan("server")
			ctx := root.Context()
			var counter atomic.Int64
			var wg sync.WaitGroup
			b.ResetTimer()
			for range n {
				wg.Go(func() {
					carrier := HTTPHeadersCarrier(http.Header{})
					for counter.Add(1) <= int64(b.N) {
						_ = trc.Inject(ctx, carrier)
					}
				})
			}
			wg.Wait()
			root.Finish()
		})
	}
}

// BenchmarkInjectPushMuContention measures Inject throughput when competing with
// concurrent writers (simulating push). Half the goroutines call Inject; the
// other half acquire trace.mu directly, as push does.
func BenchmarkInjectPushMuContention(b *testing.B) {
	for _, n := range []int{1, 2, 4, 8, 16, 32, 64, 128} {
		b.Run(fmt.Sprintf("goroutines_%03d", n), func(b *testing.B) {
			trc, err := newTracer(withTransport(newDummyTransport()), withNoopStats())
			if err != nil {
				b.Fatal(err)
			}
			defer trc.Stop()

			root := trc.StartSpan("server")
			ctx := root.Context()
			tr := root.context.trace

			var counter atomic.Int64
			var wg sync.WaitGroup

			b.ResetTimer()

			for i := range n {
				isInjector := i%2 != 0
				wg.Go(func() {
					carrier := HTTPHeadersCarrier(http.Header{})
					for counter.Add(1) <= int64(b.N) {
						if isInjector {
							_ = trc.Inject(ctx, carrier)
						} else {
							tr.mu.Lock()
							tr.mu.Unlock()
						}
					}
				})
			}
			wg.Wait()
			root.Finish()
		})
	}
}

// BenchmarkTraceContention measures the full outgoing-call sequence
// (StartSpan → Inject → Finish) under increasing concurrency.
func BenchmarkTraceContention(b *testing.B) {
	old := traceMaxSize
	traceMaxSize = 1 << 27
	b.Cleanup(func() { traceMaxSize = old })

	for _, n := range []int{1, 2, 4, 8, 16, 32, 64, 128} {
		b.Run(fmt.Sprintf("goroutines_%03d", n), func(b *testing.B) {
			trc, err := newTracer(withTransport(newDummyTransport()), withNoopStats())
			if err != nil {
				b.Fatal(err)
			}
			defer trc.Stop()

			root := trc.StartSpan("server")
			ctx := root.Context()

			var counter atomic.Int64
			var wg sync.WaitGroup

			b.ReportAllocs()
			b.ResetTimer()

			for range n {
				wg.Go(func() {
					carrier := HTTPHeadersCarrier(http.Header{})
					for counter.Add(1) <= int64(b.N) {
						child := trc.StartSpan("client", ChildOf(ctx))
						_ = trc.Inject(ctx, carrier)
						child.Finish()
					}
				})
			}
			wg.Wait()
			root.Finish()
		})
	}
}
