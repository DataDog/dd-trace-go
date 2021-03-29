// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package opentracer

import (
	"context"
	"testing"

	"github.com/opentracing/opentracing-go"
	
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/stretchr/testify/assert"
)

func TestStart(t *testing.T) {
	assert := assert.New(t)
	ot := New()
	dd, ok := internal.GetGlobalTracer().(ddtrace.Tracer)
	assert.True(ok)
	ott, ok := ot.(*opentracer)
	assert.True(ok)
	assert.Equal(ott.Tracer, dd)
}

func TestSpanWithContext(t *testing.T) {
	assert := assert.New(t)
	ot := New()
	_, ok := internal.GetGlobalTracer().(ddtrace.Tracer)
	assert.True(ok)
	ott, ok := ot.(*opentracer)
	assert.True(ok)
	openTracingSpan := ott.StartSpan("test.operation")
	otWithExt, ok := ot.(opentracing.TracerContextWithSpanExtension)
	assert.True(ok)
	ctx := otWithExt.ContextWithSpanHook(context.Background(), openTracingSpan)

	// check that the span was added to the tracer context
	spanInContext, ok := tracer.SpanFromContext(ctx)
	assert.True(ok)
	assert.Equal(spanInContext, openTracingSpan.(*span).Span)
}
