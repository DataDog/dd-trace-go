// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"sync"
)

var spanPool = sync.Pool{
	New: func() any {
		return &Span{
			meta:    make(map[string]string, 1),
			metrics: make(map[string]float64, 1),
		}
	},
}

func acquireSpan() *Span {
	return spanPool.Get().(*Span)
}

func releaseSpan(s *Span) {
	s.clear()
	spanPool.Put(s)
}

func releaseSpans(spans []*Span) {
	for _, s := range spans {
		releaseSpan(s)
	}
}
