// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package testing provides functions to trace the test execution (https://golang.org/pkg/testing).
package testing // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/testing"

import (
	"context"
	"runtime"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext/ci"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const (
	spanKind      = "test"
	testFramework = "golang.org/pkg/testing"
)

var (
	// tags contains information detected from CI/CD environment variables.
	tags map[string]string
)

// StartSpanFromContext returns a new span with the given testing.TB interface and options. It uses
// tracer.StartSpanFromContext function to start the span with automatically detected information.
func StartSpanFromContext(ctx context.Context, tb testing.TB, opts ...tracer.StartSpanOption) (tracer.Span, context.Context) {
	_, suite, _, _ := runtime.Caller(1)
	testOpts := []tracer.StartSpanOption{
		tracer.SpanType(ext.SpanTypeTest),
		tracer.ResourceName(tb.Name()),
		tracer.Tag(ext.SpanKind, spanKind),
		tracer.Tag(ext.TestName, tb.Name()),
		tracer.Tag(ext.TestSuite, suite),
		tracer.Tag(ext.TestFramework, testFramework),
	}
	opts = append(opts, testOpts...)

	switch tb.(type) {
	case *testing.T:
		opts = append(opts, tracer.Tag(ext.TestType, ext.TestTypeTest))
	case *testing.B:
		opts = append(opts, tracer.Tag(ext.TestType, ext.TestTypeBenchmark))
	}

	// Load CI tags
	if tags == nil {
		tags = ci.Tags()
	}

	for k, v := range tags {
		opts = append(opts, tracer.Tag(k, v))
	}

	return tracer.StartSpanFromContext(ctx, ext.SpanTypeTest, opts...)
}

// Finish closes span it finds in context with information extracted from testing.TB interface.
func Finish(ctx context.Context, tb testing.TB, opts ...ddtrace.FinishOption) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}

	span.SetTag(ext.Error, tb.Failed())
	if tb.Failed() {
		span.SetTag(ext.TestStatus, ext.TestStatusFail)
	} else if tb.Skipped() {
		span.SetTag(ext.TestStatus, ext.TestStatusSkip)
	} else {
		span.SetTag(ext.TestStatus, ext.TestStatusPass)
	}
	span.Finish(opts...)
}
