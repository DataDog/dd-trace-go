// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

const N = 2

func main() {
	var (
		_ ddtrace.FinishConfig // want `the declared type is in the ddtrace/tracer package now`
		_ ddtrace.FinishOption // want `the declared type is in the ddtrace/tracer package now`
		_ ddtrace.Logger       // want `the declared type is in the ddtrace/tracer package now`
		_ ddtrace.Span         // want `the declared type is in the ddtrace/tracer package now`
		_ ddtrace.SpanContext
		_ ddtrace.SpanLink        // want `the declared type is in the ddtrace/tracer package now`
		_ ddtrace.StartSpanConfig // want `the declared type is in the ddtrace/tracer package now`
		_ ddtrace.StartSpanOption // want `the declared type is in the ddtrace/tracer package now`
		_ ddtrace.Tracer          // want `the declared type is in the ddtrace/tracer package now`
		_ time.Time

		// Composite type tests: pointer, slice, array
		_ *ddtrace.Span       // want `the declared type is in the ddtrace/tracer package now`
		_ []ddtrace.Span      // want `the declared type is in the ddtrace/tracer package now`
		_ [3]ddtrace.SpanLink // want `the declared type is in the ddtrace/tracer package now`

		// Non-literal array length: diagnostic emitted but no fix (preserves original formatting)
		_ [N + 1]ddtrace.Span // want `the declared type is in the ddtrace/tracer package now`

		// Composite SpanContext types should NOT be migrated (exclusion applies to unwrapped base type)
		_ *ddtrace.SpanContext
		_ []ddtrace.SpanContext
		_ [2]ddtrace.SpanContext
	)
}

func spanConsumer(_ ddtrace.Span) { // want `the declared type is in the ddtrace/tracer package now`
}

func pointerConsumer(_ *ddtrace.Span) { // want `the declared type is in the ddtrace/tracer package now`
}

func sliceConsumer(_ []ddtrace.Span) { // want `the declared type is in the ddtrace/tracer package now`
}
