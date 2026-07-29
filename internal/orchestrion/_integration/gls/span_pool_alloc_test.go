// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gls

import (
	"context"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// BenchmarkSpanLifecycleAllocs measures a full activate-and-finish span lifecycle
// under a woven build, with the experimental span pool on and off.
//
// It exists because the gate this change removes made the pool silently inert under
// Orchestrion: on the parent branch, WithSpanPool(true) and WithSpanPool(false)
// measure identically (3056 vs 3057 B/op, 27 vs 27 allocs/op), so a service could
// set DD_TRACER_EXPERIMENTAL_SPAN_POOL_ENABLED and get nothing. Here they diverge.
//
// The second thing it pins is that moving the liveness bit onto the entry does not
// eat the saving. The cell is one allocation per activation, against five saved by
// pooling:
//
//	pool off   3081 B/op   28 allocs/op
//	pool on    2138 B/op   23 allocs/op
//
// Compare BenchmarkSpanLifecycleNoActivation to separate the GLS bookkeeping from
// the pooling itself. Read allocs/op and B/op rather than ns/op: the allocation
// counts are deterministic, while ns/op is not comparable across separate runs.
func BenchmarkSpanLifecycleAllocs(b *testing.B) {
	if !orchestrionEnabled {
		b.Skip("the GLS only exists in orchestrion builds")
	}

	for _, tc := range []struct {
		name string
		pool bool
	}{
		{name: "pool_off", pool: false},
		{name: "pool_on", pool: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			if err := tracer.Start(tracer.WithLogStartup(false), tracer.WithSpanPool(tc.pool)); err != nil {
				b.Fatal(err)
			}
			defer tracer.Stop()

			base := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				s := tracer.StartSpan("bench.op")
				_ = tracer.ContextWithSpan(base, s)
				s.Finish()
			}
		})
	}
}

// BenchmarkSpanLifecycleNoActivation isolates the cost the GLS adds. Without a
// ContextWithSpan there is no activation, so no entry and no liveness cell: the
// difference against BenchmarkSpanLifecycleAllocs is what the GLS bookkeeping costs
// per span.
func BenchmarkSpanLifecycleNoActivation(b *testing.B) {
	if !orchestrionEnabled {
		b.Skip("the GLS only exists in orchestrion builds")
	}

	for _, tc := range []struct {
		name string
		pool bool
	}{
		{name: "pool_off", pool: false},
		{name: "pool_on", pool: true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			if err := tracer.Start(tracer.WithLogStartup(false), tracer.WithSpanPool(tc.pool)); err != nil {
				b.Fatal(err)
			}
			defer tracer.Stop()

			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				s := tracer.StartSpan("bench.op")
				s.Finish()
			}
		})
	}
}
