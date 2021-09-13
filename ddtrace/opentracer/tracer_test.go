// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package opentracer

import (
	"context"
	"errors"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

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

func TestTranslateError(t *testing.T) {
	for name, tt := range map[string]struct {
		in, out error
	}{
		"nil":                     {in: nil, out: nil},
		"unrecognized":            {in: errors.New("unrecognized"), out: errors.New("unrecognized")},
		"ErrSpanContextNotFound":  {in: tracer.ErrSpanContextNotFound, out: opentracing.ErrSpanContextNotFound},
		"ErrInvalidCarrier":       {in: tracer.ErrInvalidCarrier, out: opentracing.ErrInvalidCarrier},
		"ErrInvalidSpanContext":   {in: tracer.ErrInvalidSpanContext, out: opentracing.ErrInvalidSpanContext},
		"ErrSpanContextCorrupted": {in: tracer.ErrSpanContextCorrupted, out: opentracing.ErrSpanContextCorrupted},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.out, translateError(tt.in))
		})
	}
}
