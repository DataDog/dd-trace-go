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

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

const (
	spanKind      = "test"
	testFramework = "golang.org/pkg/testing"
)

// FinishFunc closes a started span and attaches test status information.
type FinishFunc func()

// StartSpanWithFinish returns a new span with the given testing.TB interface and options. It uses
// tracer.StartSpanFromContext function to start the span with automatically detected information.
func StartSpanWithFinish(ctx context.Context, tb testing.TB, opts ...Option) (context.Context, FinishFunc) {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	_, suite, _, _ := runtime.Caller(cfg.skip)
	testOpts := []tracer.StartSpanOption{
		tracer.ResourceName(tb.Name()),
		tracer.Tag(ext.TestName, tb.Name()),
		tracer.Tag(ext.TestSuite, suite),
		tracer.Tag(ext.TestFramework, testFramework),
	}

	switch tb.(type) {
	case *testing.T:
		testOpts = append(testOpts, tracer.Tag(ext.TestType, ext.TestTypeTest))
	case *testing.B:
		testOpts = append(testOpts, tracer.Tag(ext.TestType, ext.TestTypeBenchmark))
	}

	cfg.spanOpts = append(cfg.spanOpts, testOpts...)

	span, ctx := tracer.StartSpanFromContext(ctx, ext.SpanTypeTest, cfg.spanOpts...)

	// Finish closes span it finds in context with information extracted from testing.TB interface.
	return ctx, func() {
		span.SetTag(ext.Error, tb.Failed())
		if tb.Failed() {
			span.SetTag(ext.TestStatus, ext.TestStatusFail)
		} else if tb.Skipped() {
			span.SetTag(ext.TestStatus, ext.TestStatusSkip)
		} else {
			span.SetTag(ext.TestStatus, ext.TestStatusPass)
		}
		span.Finish(cfg.finishOpts...)
	}
}
