// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package waf

import (
	"context"
	"testing"

	tracelib "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/trace"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec/limiter"
)

// benchOperation returns a context operation with a real limiter. An allowing
// limiter has a very large burst. A denying limiter has no token.
func benchOperation(b *testing.B, allow bool) *ContextOperation {
	b.Helper()
	op, _ := StartContextOperation(context.Background(), tracelib.NoopTagSetter{})
	if allow {
		op.SetLimiter(limiter.NewTokenTicker(1_000_000_000, 1_000_000_000))
	} else {
		op.SetLimiter(limiter.NewTokenTicker(0, 0))
	}
	b.Cleanup(op.Finish)
	return op
}

// runBench runs f in series, and then from all the goroutines of the benchmark
// on the same operation, which is the worst case for a lock.
func runBench(b *testing.B, f func()) {
	b.Run("serial", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			f()
		}
	})
	b.Run("parallel", func(b *testing.B) {
		b.ReportAllocs()
		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				f()
			}
		})
	})
}

// BenchmarkAllowKeep measures the cost of the keep admission. The case
// "limiter-only" is the cost that the code had before: 1 limiter call, no lock
// of the operation.
func BenchmarkAllowKeep(b *testing.B) {
	b.Run("limiter-only", func(b *testing.B) {
		op := benchOperation(b, true)
		runBench(b, func() { _ = op.limiter.Allow() })
	})
	b.Run("admitted", func(b *testing.B) {
		op := benchOperation(b, true)
		if !op.allowKeep() {
			b.Fatal("the first admission must succeed")
		}
		runBench(b, func() { _ = op.allowKeep() })
	})
	b.Run("not-admitted", func(b *testing.B) {
		op := benchOperation(b, false)
		runBench(b, func() { _ = op.allowKeep() })
	})
}

// BenchmarkRunWAFKeep measures a whole runWAF call for a result with keep=true
// and no event, with a fake runner.
func BenchmarkRunWAFKeep(b *testing.B) {
	b.Run("admitted", func(b *testing.B) {
		op := benchOperation(b, true)
		addrs := wafScopeAddresses()
		runBench(b, func() { op.runWAF(op, keepNoEventRunner, addrs, false) })
	})
	b.Run("not-admitted", func(b *testing.B) {
		op := benchOperation(b, false)
		addrs := wafScopeAddresses()
		runBench(b, func() { op.runWAF(op, keepNoEventRunner, addrs, false) })
	})
}
