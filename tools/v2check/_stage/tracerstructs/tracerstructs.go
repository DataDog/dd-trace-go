// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package main

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

func main() {
	var (
		_ tracer.Span        // want `the declared type is now a struct, you need to use a pointer`
		_ tracer.SpanContext // want `the declared type is now a struct, you need to use a pointer`
		_ tracer.SpanLink
	)
}

func spanConsumer(_ tracer.Span) { // want `the declared type is now a struct, you need to use a pointer`
}