// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

// BenchmarkTraceContention measures lock contention on trace.mu by running N
// goroutines simultaneously in a tight loop, each doing the canonical outgoing-
// call sequence (StartSpan → Inject → Finish) on the same shared trace.
//
// traceMaxSize is set large so the trace never fills up and every iteration
// goes through the real lock path.
//
// How to read results:
//   - ns/op CONSTANT or RISING with N → lock contention (goroutines waiting)
//   - ns/op FALLING with N           → parallel speedup (no contention)
//
// Copy before benchmarking:
//   cp /tmp/bench_outgoing_call_test.go ddtrace/tracer/
//
// Run: go test -bench=BenchmarkTraceContention -benchmem -benchtime=5s

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

// BenchmarkTraceMuContention isolates contention on trace.mu directly,
// stripping all overhead so goroutines hit the lock as fast as possible.
func BenchmarkTraceMuContention(b *testing.B) {
	for _, n := range []int{1, 2, 4, 8, 16, 32, 64, 128} {
		b.Run(fmt.Sprintf("goroutines_%03d", n), func(b *testing.B) {
			t := newTrace()
			var counter atomic.Int64
			var wg sync.WaitGroup
			b.ResetTimer()
			for range n {
				wg.Add(1)
				go func() {
					defer wg.Done()
					for counter.Add(1) <= int64(b.N) {
						t.mu.Lock()
						t.mu.Unlock()
					}
				}()
			}
			wg.Wait()
		})
	}
}

// BenchmarkInjectMultiG measures how inject throughput scales with goroutine count.
// On main, every inject acquires t.mu.Lock (write lock) unconditionally — goroutines
// serialize. On PR1, inject is lock-free — goroutines scale in parallel.
//
// Signal: ns/op RISING with N → write-lock contention (serialised).
//         ns/op FALLING with N → parallel speedup (lock-free).
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
				wg.Add(1)
				go func() {
					defer wg.Done()
					carrier := HTTPHeadersCarrier(http.Header{})
					for counter.Add(1) <= int64(b.N) {
						_ = trc.Inject(ctx, carrier)
					}
				}()
			}
			wg.Wait()
			root.Finish()
		})
	}
}

// BenchmarkInjectPushMuContention isolates the RW-mutex interaction between
// inject (reader on main, lockless on PR1) and push (writer on both).
// Half goroutines call trc.Inject — on main this acquires t.mu.RLock,
// on PR1 it is completely lockless. Half goroutines acquire t.mu.Lock
// directly, simulating what push() does.
//
// With -cpu=1 this forces goroutines to queue at the mutex, producing
// superlinear growth on main and flat growth on PR1 if contention is reduced.
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
				wg.Add(1)
				isInjector := i%2 != 0
				go func() {
					defer wg.Done()
					carrier := HTTPHeadersCarrier(http.Header{})
					for counter.Add(1) <= int64(b.N) {
						if isInjector {
							_ = trc.Inject(ctx, carrier)
						} else {
							tr.mu.Lock()
							tr.mu.Unlock()
						}
					}
				}()
			}
			wg.Wait()
			root.Finish()
		})
	}
}

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
				wg.Add(1)
				go func() {
					defer wg.Done()
					carrier := HTTPHeadersCarrier(http.Header{})
					for counter.Add(1) <= int64(b.N) {
						child := trc.StartSpan("client", ChildOf(ctx))
						_ = trc.Inject(ctx, carrier)
						child.Finish()
					}
				}()
			}
			wg.Wait()
			root.Finish()
		})
	}
}
