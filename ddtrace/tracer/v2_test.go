// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"testing"

	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

func BenchmarkApplyV1Options(b *testing.B) {
	cfg := new(v2.StartSpanConfig)
	opts := []ddtrace.StartSpanOption{
		WithSpanID(123),
		// Setting tags introduces overhead that is not directly responsability of ApplyV1Options.
		// Tag("key", "value"),
	}
	f := ApplyV1Options(opts...)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f(cfg)
	}
}

func BenchmarkApplyV1FinishOptions(b *testing.B) {
	cfg := new(v2.FinishConfig)
	opts := []ddtrace.FinishOption{
		WithError(fmt.Errorf("error")),
	}
	f := ApplyV1FinishOptions(opts...)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		f(cfg)
	}
}
