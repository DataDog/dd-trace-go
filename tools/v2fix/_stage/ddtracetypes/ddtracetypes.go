// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

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
	)
}

func spanConsumer(_ ddtrace.Span) { // want `the declared type is in the ddtrace/tracer package now`
}
