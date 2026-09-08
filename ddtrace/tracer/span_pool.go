// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import "sync"

// spanPool recycles span allocations to reduce GC pressure in high-throughput
// tracing workloads. Reusing span values cuts heap allocation rate and GC
// pause time, which benefits latency-sensitive applications that create many
// short-lived spans. The pool is guarded by an environment variable/tracer option
// so callers that don't opt in allocate spans normally.
// Callers opting in must avoid inspecting the spans by calling any function or
// using it in logging statements.
var spanPool = sync.Pool{
	New: func() any { return &Span{} },
}

func acquireSpan(poolEnabled bool) *Span {
	// maybeTestAcquireSpan is a no-op (returns nil, inlined away) outside the
	// deadlock build; under -tags deadlock it lets the lock-ordering test inject
	// specific *Span instances. See span_pool_testhook*.go.
	if s := maybeTestAcquireSpan(); s != nil {
		return s
	}
	if poolEnabled {
		return spanPool.Get().(*Span)
	}
	return &Span{}
}

func releaseSpans(poolEnabled bool, spans []*Span) {
	if !poolEnabled {
		return
	}
	for _, s := range spans {
		s.clear()
		spanPool.Put(s)
	}
}
