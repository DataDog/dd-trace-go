// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"context"
	"fmt"
	"testing"

	"go.opentelemetry.io/otel/attribute"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	oteltrace "go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

func TestGetTracer(t *testing.T) {
	assert := assert.New(t)
	tp := NewTracerProvider()
	tr := tp.Tracer("ot")
	dd, ok := internal.GetGlobalTracer().(ddtrace.Tracer)
	assert.True(ok)
	ott, ok := tr.(*oteltracer)
	assert.True(ok)
	assert.Equal(ott.Tracer, dd)
}

func TestSpanWithContext(t *testing.T) {
	assert := assert.New(t)
	tp := NewTracerProvider()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("ot", oteltrace.WithInstrumentationVersion("0.1"))
	ctx, sp := tr.Start(context.Background(), "otel.test")
	got, ok := tracer.SpanFromContext(ctx)

	assert.True(ok)
	assert.Equal(got, sp.(*span).Span)
	assert.Equal(fmt.Sprintf("%x", got.Context().SpanID()), sp.SpanContext().SpanID().String())
}

func TestSpanWithNewRoot(t *testing.T) {
	assert := assert.New(t)
	otel.SetTracerProvider(NewTracerProvider())
	tr := otel.Tracer("")

	noopParent, ddCtx := tracer.StartSpanFromContext(context.Background(), "otel.child")

	otelCtx, child := tr.Start(ddCtx, "otel.child", oteltrace.WithNewRoot())
	got, ok := tracer.SpanFromContext(otelCtx)
	assert.True(ok)
	assert.Equal(got, child.(*span).Span)

	var parentBytes oteltrace.TraceID
	uint64ToByte(noopParent.Context().TraceID(), parentBytes[:])
	assert.NotEqual(parentBytes, child.SpanContext().TraceID())
}

func TestSpanWithoutNewRoot(t *testing.T) {
	assert := assert.New(t)
	otel.SetTracerProvider(NewTracerProvider())
	tr := otel.Tracer("")

	parent, ddCtx := tracer.StartSpanFromContext(context.Background(), "otel.child")
	_, child := tr.Start(ddCtx, "otel.child")
	var parentBytes oteltrace.TraceID
	uint64ToByte(parent.Context().TraceID(), parentBytes[:])
	assert.Equal(parentBytes, child.SpanContext().TraceID())
}

func TestTracerOptions(t *testing.T) {
	assert := assert.New(t)
	otel.SetTracerProvider(NewTracerProvider(tracer.WithEnv("wrapper_env")))
	tr := otel.Tracer("ot")
	ctx, sp := tr.Start(context.Background(), "otel.test")
	got, ok := tracer.SpanFromContext(ctx)
	assert.True(ok)
	assert.Equal(got, sp.(*span).Span)
	assert.Contains(fmt.Sprint(sp), "dd.env=wrapper_env")
}

func BenchmarkApiWithNoTags(b *testing.B) {
	testData := struct {
		env, srv, op string
	}{"test_env", "test_srv", "op_name"}

	tp := NewTracerProvider(tracer.WithEnv(testData.env), tracer.WithService(testData.srv))
	defer tp.Shutdown()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")

	b.ResetTimer()
	b.Run("otel_api", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, sp := tr.Start(context.Background(), testData.op)
			sp.End()
		}
	})

	tracer.Start(tracer.WithEnv(testData.env), tracer.WithService(testData.srv))
	defer tracer.Stop()
	b.ResetTimer()
	b.Run("datadog_otel_api", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sp, _ := tracer.StartSpanFromContext(context.Background(), testData.op)
			sp.Finish()
		}
	})
}
func BenchmarkApiWithCustomTags(b *testing.B) {
	testData := struct {
		env, srv, oldOp, newOp, tagKey, tagValue string
	}{"test_env", "test_srv", "old_op", "new_op", "tag_1", "tag_1_val"}

	tp := NewTracerProvider(tracer.WithEnv(testData.env), tracer.WithService(testData.srv))
	defer tp.Shutdown()
	otel.SetTracerProvider(tp)
	tr := otel.Tracer("")

	b.ResetTimer()
	b.Run("otel_api", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_, sp := tr.Start(context.Background(), testData.oldOp)
			sp.SetAttributes(attribute.String(testData.tagKey, testData.tagValue))
			sp.SetName(testData.newOp)
			sp.End()
		}
	})

	tracer.Start(tracer.WithEnv(testData.env), tracer.WithService(testData.srv))
	defer tracer.Stop()
	b.ResetTimer()
	b.Run("datadog_otel_api", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			sp, _ := tracer.StartSpanFromContext(context.Background(), testData.oldOp)
			sp.SetTag(testData.tagKey, testData.tagValue)
			sp.SetOperationName(testData.newOp)
			sp.Finish()
		}
	})
}
