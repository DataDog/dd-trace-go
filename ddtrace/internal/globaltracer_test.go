// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package internal // import "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

import (
	"testing"
)

func BenchmarkGetGlobalTracerSerial(b *testing.B) {
	for i := 0; i < b.N; i++ {
		tracer := GetGlobalTracer()
		if tracer == nil {
			b.Fatal("BUG: tracer must not be nil")
		}
	}
}

func BenchmarkGetGlobalTracerParallel(b *testing.B) {
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tracer := GetGlobalTracer()
			if tracer == nil {
				b.Fatal("BUG: tracer must not be nil")
			}
		}
	})
}
