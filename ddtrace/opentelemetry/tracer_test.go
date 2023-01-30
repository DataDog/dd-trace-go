// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel"
	oteltrace "go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
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

func TestForceFlush(t *testing.T) {
	testData := []struct {
		timeOut   time.Duration
		flushFail bool
	}{
		{timeOut: 0 * time.Second, flushFail: true},
		{timeOut: 300 * time.Second, flushFail: false},
	}

	flushFail := false
	setFlushFail := func(ok bool) { flushFail = !ok }
	reset := func() { flushFail = false }
	assert := assert.New(t)

	for _, tc := range testData {
		var payload string
		done := make(chan struct{})

		tp, s := getTestTracerProvider(&payload, done, "test_env", "test_srv", t)

		otel.SetTracerProvider((tp))
		tr := otel.Tracer("")

		_, sp := tr.Start(context.Background(), "test_span")
		sp.End()

		tp.ForceFlush(tc.timeOut, setFlushFail)
		if !tc.flushFail {
			select {
			case <-time.After(time.Second):
				t.FailNow()
			case <-done:
				break
			}
			assert.Contains(payload, "test_span")
		}
		assert.Equal(tc.flushFail, flushFail)
		reset()
		s.Close()
	}
}

func TestShutdown(t *testing.T) {
	tp := NewTracerProvider()
	otel.SetTracerProvider(tp)

	testLog := new(log.RecordLogger)
	defer log.UseLogger(testLog)()

	tp.Shutdown()
	tp.ForceFlush(5*time.Second, func(ok bool) {})

	logs := testLog.Logs()
	assert.Contains(t, logs[len(logs)-1], "tracer stopped")
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

func BenchmarkOtelConcurrentTracing(b *testing.B) {
	tp := NewTracerProvider()
	defer tp.Shutdown()
	otel.SetTracerProvider(NewTracerProvider())
	tr := otel.Tracer("")

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		wg := sync.WaitGroup{}
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				ctx := context.Background()
				newCtx, parent := tr.Start(ctx, "parent")
				parent.SetAttributes(attribute.String("ServiceName", "pylons"),
					attribute.String("ResourceName", "/"))
				defer parent.End()

				for i := 0; i < 10; i++ {
					_, child := tr.Start(newCtx, "child")
					child.End()
				}
			}()
		}
	}
}
