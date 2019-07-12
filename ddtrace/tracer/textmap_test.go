// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package tracer

import (
	"errors"
	"net/http"
	"os"
	"strconv"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"

	"github.com/stretchr/testify/assert"
)

func TestHTTPHeadersCarrierSet(t *testing.T) {
	h := http.Header{}
	c := HTTPHeadersCarrier(h)
	c.Set("A", "x")
	assert.Equal(t, "x", h.Get("A"))
}

func TestHTTPHeadersCarrierForeachKey(t *testing.T) {
	h := http.Header{}
	h.Add("A", "x")
	h.Add("B", "y")
	got := map[string]string{}
	err := HTTPHeadersCarrier(h).ForeachKey(func(k, v string) error {
		got[k] = v
		return nil
	})
	assert := assert.New(t)
	assert.Nil(err)
	assert.Equal("x", h.Get("A"))
	assert.Equal("y", h.Get("B"))
}

func TestHTTPHeadersCarrierForeachKeyError(t *testing.T) {
	want := errors.New("random error")
	h := http.Header{}
	h.Add("A", "x")
	h.Add("B", "y")
	got := HTTPHeadersCarrier(h).ForeachKey(func(k, v string) error {
		if k == "B" {
			return want
		}
		return nil
	})
	assert.Equal(t, want, got)
}

func TestTextMapCarrierSet(t *testing.T) {
	m := map[string]string{}
	c := TextMapCarrier(m)
	c.Set("a", "b")
	assert.Equal(t, "b", m["a"])
}

func TestTextMapCarrierForeachKey(t *testing.T) {
	want := map[string]string{"A": "x", "B": "y"}
	got := map[string]string{}
	err := TextMapCarrier(want).ForeachKey(func(k, v string) error {
		got[k] = v
		return nil
	})
	assert := assert.New(t)
	assert.Nil(err)
	assert.Equal(got, want)
}

func TestTextMapCarrierForeachKeyError(t *testing.T) {
	m := map[string]string{"A": "x", "B": "y"}
	want := errors.New("random error")
	got := TextMapCarrier(m).ForeachKey(func(k, v string) error {
		return want
	})
	assert.Equal(t, got, want)
}

func TestTextMapPropagatorErrors(t *testing.T) {
	propagator := NewPropagator(nil)
	assert := assert.New(t)

	err := propagator.Inject(&spanContext{}, 2)
	assert.Equal(ErrInvalidCarrier, err)
	err = propagator.Inject(internal.NoopSpanContext{}, TextMapCarrier(map[string]string{}))
	assert.Equal(ErrInvalidSpanContext, err)
	err = propagator.Inject(&spanContext{}, TextMapCarrier(map[string]string{}))
	assert.Equal(ErrInvalidSpanContext, err) // no traceID and spanID
	err = propagator.Inject(&spanContext{traceID: 1}, TextMapCarrier(map[string]string{}))
	assert.Equal(ErrInvalidSpanContext, err) // no spanID

	_, err = propagator.Extract(2)
	assert.Equal(ErrInvalidCarrier, err)

	_, err = propagator.Extract(TextMapCarrier(map[string]string{
		DefaultTraceIDHeader:  "1",
		DefaultParentIDHeader: "A",
	}))
	assert.Equal(ErrSpanContextCorrupted, err)

	_, err = propagator.Extract(TextMapCarrier(map[string]string{
		DefaultTraceIDHeader:  "A",
		DefaultParentIDHeader: "2",
	}))
	assert.Equal(ErrSpanContextCorrupted, err)

	_, err = propagator.Extract(TextMapCarrier(map[string]string{
		DefaultTraceIDHeader:  "0",
		DefaultParentIDHeader: "0",
	}))
	assert.Equal(ErrSpanContextNotFound, err)
}

func TestTextMapPropagatorInjectHeader(t *testing.T) {
	assert := assert.New(t)

	propagator := NewPropagator(&PropagatorConfig{
		BaggagePrefix: "bg-",
		TraceHeader:   "tid",
		ParentHeader:  "pid",
	})
	tracer := newTracer(WithPropagator(propagator))

	root := tracer.StartSpan("web.request").(*span)
	root.SetBaggageItem("item", "x")
	root.SetTag(ext.SamplingPriority, 0)
	ctx := root.Context()
	headers := http.Header{}

	carrier := HTTPHeadersCarrier(headers)
	err := tracer.Inject(ctx, carrier)
	assert.Nil(err)

	tid := strconv.FormatUint(root.TraceID, 10)
	pid := strconv.FormatUint(root.SpanID, 10)

	assert.Equal(headers.Get("tid"), tid)
	assert.Equal(headers.Get("pid"), pid)
	assert.Equal(headers.Get("bg-item"), "x")
	assert.Equal(headers.Get(DefaultPriorityHeader), "0")
}

func TestTextMapPropagatorOrigin(t *testing.T) {
	src := TextMapCarrier(map[string]string{
		originHeader:          "synthetics",
		DefaultTraceIDHeader:  "1",
		DefaultParentIDHeader: "1",
	})
	tracer := newTracer()
	ctx, err := tracer.Extract(src)
	if err != nil {
		t.Fatal(err)
	}
	sctx, ok := ctx.(*spanContext)
	if !ok {
		t.Fatal("not a *spanContext")
	}
	if sctx.origin != "synthetics" {
		t.Fatalf("didn't propagate origin, got: %q", sctx.origin)
	}
	dst := map[string]string{}
	if err := tracer.Inject(ctx, TextMapCarrier(dst)); err != nil {
		t.Fatal(err)
	}
	if dst[originHeader] != "synthetics" {
		t.Fatal("didn't inject header")
	}
}

func TestTextMapPropagatorInjectExtract(t *testing.T) {
	propagator := NewPropagator(&PropagatorConfig{
		BaggagePrefix: "bg-",
		TraceHeader:   "tid",
		ParentHeader:  "pid",
	})
	tracer := newTracer(WithPropagator(propagator))
	root := tracer.StartSpan("web.request").(*span)
	root.SetTag(ext.SamplingPriority, -1)
	root.SetBaggageItem("item", "x")
	ctx := root.Context().(*spanContext)
	headers := TextMapCarrier(map[string]string{})
	err := tracer.Inject(ctx, headers)

	assert := assert.New(t)
	assert.Nil(err)

	sctx, err := tracer.Extract(headers)
	assert.Nil(err)

	xctx, ok := sctx.(*spanContext)
	assert.True(ok)
	assert.Equal(xctx.traceID, ctx.traceID)
	assert.Equal(xctx.spanID, ctx.spanID)
	assert.Equal(xctx.baggage, ctx.baggage)
	assert.Equal(xctx.trace.priority, ctx.trace.priority)
}

func TestB3(t *testing.T) {
	t.Run("inject", func(t *testing.T) {
		os.Setenv("DD_PROPAGATION_STYLE_INJECT", "B3")
		defer os.Unsetenv("DD_PROPAGATION_STYLE_INJECT")

		tracer := newTracer()
		root := tracer.StartSpan("web.request").(*span)
		root.SetTag(ext.SamplingPriority, -1)
		root.SetBaggageItem("item", "x")
		ctx := root.Context().(*spanContext)
		headers := TextMapCarrier(map[string]string{})
		err := tracer.Inject(ctx, headers)

		assert := assert.New(t)
		assert.Nil(err)

		assert.Equal(headers[b3TraceIDHeader], strconv.FormatUint(root.TraceID, 16))
		assert.Equal(headers[b3SpanIDHeader], strconv.FormatUint(root.SpanID, 16))
		assert.Equal(headers[b3SampledHeader], "0")
	})

	t.Run("extract", func(t *testing.T) {
		os.Setenv("DD_PROPAGATION_STYLE_EXTRACT", "b3")
		defer os.Unsetenv("DD_PROPAGATION_STYLE_EXTRACT")

		headers := TextMapCarrier(map[string]string{
			b3TraceIDHeader: "1",
			b3SpanIDHeader:  "1",
		})

		tracer := newTracer()
		assert := assert.New(t)
		ctx, err := tracer.Extract(headers)
		assert.Nil(err)
		sctx, ok := ctx.(*spanContext)
		assert.True(ok)

		assert.Equal(sctx.traceID, uint64(1))
		assert.Equal(sctx.spanID, uint64(1))
	})

	t.Run("multiple", func(t *testing.T) {
		os.Setenv("DD_PROPAGATION_STYLE_EXTRACT", "Datadog,B3")
		defer os.Unsetenv("DD_PROPAGATION_STYLE_EXTRACT")

		b3Headers := TextMapCarrier(map[string]string{
			b3TraceIDHeader: "1",
			b3SpanIDHeader:  "1",
			b3SampledHeader: "1",
		})

		tracer := newTracer()
		assert := assert.New(t)

		ctx, err := tracer.Extract(b3Headers)
		assert.Nil(err)
		sctx, ok := ctx.(*spanContext)
		assert.True(ok)

		assert.Equal(sctx.traceID, uint64(1))
		assert.Equal(sctx.spanID, uint64(1))
		assert.True(sctx.hasSamplingPriority())
		assert.Equal(sctx.samplingPriority(), 1)

		ddHeaders := TextMapCarrier(map[string]string{
			DefaultTraceIDHeader:  "2",
			DefaultParentIDHeader: "2",
			DefaultPriorityHeader: "2",
		})

		ctx, err = tracer.Extract(ddHeaders)
		assert.Nil(err)
		sctx, ok = ctx.(*spanContext)
		assert.True(ok)

		assert.Equal(sctx.traceID, uint64(2))
		assert.Equal(sctx.spanID, uint64(2))
		assert.True(sctx.hasSamplingPriority())
		assert.Equal(sctx.samplingPriority(), 2)
	})
}
