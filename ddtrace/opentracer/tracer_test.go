// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package opentracer

import (
	"context"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry/telemetrytest"

	"github.com/opentracing/opentracing-go"
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
	ot, ok := New().(*opentracer)
	assert.True(ok)
	opentracing.SetGlobalTracer(ot)
	want, ctx := opentracing.StartSpanFromContext(context.Background(), "test.operation")
	got, ok := tracer.SpanFromContext(ctx)
	assert.True(ok)
	assert.Equal(got, want.(*span).Span)
}

func TestInjectError(t *testing.T) {
	ot := New()

	for name, tt := range map[string]struct {
		spanContext opentracing.SpanContext
		format      interface{}
		carrier     interface{}
		want        error
	}{
		"ErrInvalidSpanContext": {
			spanContext: internal.NoopSpanContext{},
			format:      opentracing.TextMap,
			carrier:     opentracing.TextMapCarrier(map[string]string{}),
			want:        opentracing.ErrInvalidSpanContext,
		},
		"ErrInvalidCarrier": {
			spanContext: ot.StartSpan("test.operation").Context(),
			format:      opentracing.TextMap,
			carrier:     "invalid-carrier",
			want:        opentracing.ErrInvalidCarrier,
		},
		"ErrUnsupportedFormat": {
			format: "unsupported-format",
			want:   opentracing.ErrUnsupportedFormat,
		},
	} {
		t.Run(name, func(t *testing.T) {
			got := ot.Inject(tt.spanContext, tt.format, tt.carrier)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestExtractError(t *testing.T) {
	ot := New()

	for name, tt := range map[string]struct {
		format  interface{}
		carrier interface{}
		want    error
	}{
		"ErrSpanContextNotFound": {
			format:  opentracing.TextMap,
			carrier: opentracing.TextMapCarrier(nil),
			want:    opentracing.ErrSpanContextNotFound,
		},
		"ErrInvalidCarrier": {
			format:  opentracing.TextMap,
			carrier: "invalid-carrier",
			want:    opentracing.ErrInvalidCarrier,
		},
		"ErrSpanContextCorrupted": {
			format: opentracing.TextMap,
			carrier: opentracing.TextMapCarrier(
				map[string]string{
					tracer.DefaultTraceIDHeader:  "-1",
					tracer.DefaultParentIDHeader: "-1",
					tracer.DefaultPriorityHeader: "not-a-number",
				},
			),
			want: opentracing.ErrSpanContextCorrupted,
		},
		"ErrUnsupportedFormat": {
			format: "unsupported-format",
			want:   opentracing.ErrUnsupportedFormat,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, got := ot.Extract(tt.format, tt.carrier)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSpanTelemetry(t *testing.T) {
	telemetryClient := new(telemetrytest.MockClient)
	defer telemetry.MockGlobalClient(telemetryClient)()
	opentracing.SetGlobalTracer(New())
	_ = opentracing.StartSpan("opentracing.span")
	telemetryClient.AssertCalled(t, "Count", telemetry.NamespaceTracers, "spans_created", 1.0, telemetryTags, true)
	telemetryClient.AssertNumberOfCalls(t, "Count", 1)
}
