// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"sync"
	"sync/atomic"
)

// spanPoolEnabled controls whether acquireSpan/releaseSpan use sync.Pool.
// It is set from the tracer config on Start and reset on Stop.
var spanPoolActive atomic.Bool

func init() {
	// Pool is enabled by default until the tracer overrides it.
	spanPoolActive.Store(true)
}

var spanPool = sync.Pool{
	New: func() any {
		return &Span{
			meta:    make(map[string]string, 1),
			metrics: make(map[string]float64, 1),
		}
	},
}

func acquireSpan() *Span {
	if spanPoolActive.Load() {
		return spanPool.Get().(*Span)
	}
	return &Span{
		meta:    make(map[string]string, 1),
		metrics: make(map[string]float64, 1),
	}
}

func releaseSpan(s *Span) {
	s.clear()
	if spanPoolActive.Load() {
		spanPool.Put(s)
	}
}

func releaseSpans(spans []*Span) {
	for _, s := range spans {
		releaseSpan(s)
	}
}
