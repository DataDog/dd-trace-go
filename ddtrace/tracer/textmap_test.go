// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"net/http"
	"sync"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
	"github.com/stretchr/testify/assert"
)

func TestHTTPHeadersCarrierSet(t *testing.T) {
	h := http.Header{}
	c := HTTPHeadersCarrier(h)
	c.Set("A", "x")
	assert.Equal(t, "x", h.Get("A"))
}

const otelHeaderPropagationStyle = "OTEL_PROPAGATORS"

func traceIDFrom64Bits(i uint64) traceID {
	t := traceID{}
	t.SetLower(i)
	return t
}

func traceIDFrom128Bits(u, l uint64) traceID {
	t := traceID{}
	t.SetLower(l)
	t.SetUpper(u)
	return t
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

func TestTextMapExtractTracestatePropagation(t *testing.T) {
	tests := []struct {
		name, propagationStyle, traceparent string
		onlyExtractFirst                    bool // value of DD_TRACE_PROPAGATION_EXTRACT_FIRST
		wantTracestatePropagation           bool
		conflictingParentID                 bool
	}{
		{
			/*
				With only Datadog propagation set, the tracestate header should
				be ignored, and not propagated to the returned trace context.
			*/
			name:             "datadog-only",
			propagationStyle: "datadog",
			traceparent:      "00-00000000000000000000000000000004-2222222222222222-01",
		},
		{
			/*
				With Datadog, B3, AND w3c propagation set, the tracestate header should
				be propagated to the returned trace context. This test also verifies that
				b3 extraction doesn't override the local context value.
			*/
			name:                      "datadog-b3-w3c",
			propagationStyle:          "datadog,b3,tracecontext",
			traceparent:               "00-00000000000000000000000000000004-2222222222222222-01",
			wantTracestatePropagation: true,
			conflictingParentID:       true,
		},
		{
			/*
				With Datadog AND w3c propagation set, the tracestate header should
				be propagated to the returned trace context.
			*/
			name:                      "datadog-and-w3c",
			propagationStyle:          "datadog,tracecontext",
			traceparent:               "00-00000000000000000000000000000004-2222222222222222-01",
			wantTracestatePropagation: true,
			conflictingParentID:       true,
		},
		{
			/*
				With Datadog AND w3c propagation set, but mismatching trace-ids,
				the tracestate header should be ignored and not propagated to
				the returned trace context.
			*/
			name:             "datadog-and-w3c-mismatching-ids",
			propagationStyle: "datadog,tracecontext",
			traceparent:      "00-00000000000000000000000000000088-2222222222222222-01",
		},
		{
			/*
				With Datadog AND w3c propagation set, but the traceparent is malformed,
				the tracestate header should be ignored and not propagated to
				the returned trace context.
			*/
			name:             "datadog-and-w3c-malformed",
			propagationStyle: "datadog,tracecontext",
			traceparent:      "00-00000000000000000000000000000004-22asdf!2-01",
		},
		{
			/*
				With Datadog AND w3c propagation set, but there is no traceparent,
				the tracestate header should be ignored and not propagated to
				the returned trace context.
			*/
			name:             "datadog-and-w3c-no-traceparent",
			propagationStyle: "datadog,tracecontext",
		},
		{
			/*
				With Datadog AND w3c propagation set, but DD_TRACE_PROPAGATION_EXTRACT_FIRST
				is true, the tracestate header should be ignored and not propagated to
				the returned trace context.
			*/
			name:             "datadog-and-w3c-only-extract-first",
			propagationStyle: "datadog,tracecontext",
			traceparent:      "00-00000000000000000000000000000004-2222222222222222-01",
			onlyExtractFirst: true,
		},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("TestTextMapExtractTracestatePropagation-%s", tc.name), func(t *testing.T) {
			t.Setenv(headerPropagationStyle, tc.propagationStyle)
			if tc.onlyExtractFirst {
				t.Setenv("DD_TRACE_PROPAGATION_EXTRACT_FIRST", "true")
			}
			tracer := newTracer()
			assert := assert.New(t)
			headers := TextMapCarrier(map[string]string{
				DefaultTraceIDHeader:  "4",
				DefaultParentIDHeader: "1",
				originHeader:          "synthetics",
				b3TraceIDHeader:       "0021dc1807524785",
				traceparentHeader:     tc.traceparent,
				tracestateHeader:      "dd=s:2;o:rum;p:0000000000000001;t.tid:1230000000000000~~,othervendor=t61rcWkgMzE",
			})

			ctx, err := tracer.Extract(headers)
			assert.Nil(err)
			sctx, ok := ctx.(*spanContext)
			if !ok {
				t.Fail()
			}
			assert.Equal("00000000000000000000000000000004", sctx.traceID.HexEncoded())
			if tc.conflictingParentID == true {
				// tracecontext span id should be used
				assert.Equal(uint64(0x2222222222222222), sctx.spanID)
			} else {
				// should use x-datadog-parent-id, not the id in the tracestate
				assert.Equal(uint64(1), sctx.spanID)
			}
			assert.Equal("synthetics", sctx.origin) // should use x-datadog-origin, not the origin in the tracestate
			if tc.wantTracestatePropagation {
				assert.Equal("0000000000000001", sctx.reparentID)
				assert.Equal("dd=s:0;o:synthetics;p:0000000000000001,othervendor=t61rcWkgMzE", sctx.trace.propagatingTag(tracestateHeader))
			} else if sctx.trace != nil {
				assert.False(sctx.trace.hasPropagatingTag(tracestateHeader))
			}
		})
	}
}

func TestTextMapPropagatorErrors(t *testing.T) {
	t.Setenv(headerPropagationStyleExtract, "datadog")
	propagator := NewPropagator(nil)
	assert := assert.New(t)

	Start()
	defer Stop()

	span := StartSpan("web.request")
	err := propagator.Inject(span.Context(), 2)
	assert.Equal(ErrInvalidCarrier, err)
	err = propagator.Inject(internal.NoopSpanContext{}, TextMapCarrier(map[string]string{}))
	assert.Equal(ErrInvalidSpanContext, err)
	err = propagator.Inject(internal.SpanContextV2Adapter{}, TextMapCarrier(map[string]string{}))
	assert.Equal(ErrInvalidSpanContext, err) // no traceID and spanID

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

	_, err = propagator.Extract(TextMapCarrier(map[string]string{
		DefaultTraceIDHeader:  "3",
		DefaultParentIDHeader: "0",
	}))
	assert.Equal(ErrSpanContextNotFound, err)
}

func TestTextMapPropagator(t *testing.T) {
	bigMap := make(map[string]string)
	for i := 0; i < 100; i++ {
		bigMap[fmt.Sprintf("someKey%d", i)] = fmt.Sprintf("someValue%d", i)
	}

	t.Run("InjectExtract", func(t *testing.T) {
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "true")
		t.Setenv(headerPropagationStyleExtract, "datadog")
		t.Setenv(headerPropagationStyleInject, "datadog")
		propagator := NewPropagator(&PropagatorConfig{
			BaggagePrefix:    "bg-",
			TraceHeader:      "tid",
			ParentHeader:     "pid",
			MaxTagsHeaderLen: defaultMaxTagsHeaderLen,
		})
		tracer := newTracer(WithPropagator(propagator))
		defer tracer.Stop()
		root := tracer.StartSpan("web.request")
		root.SetTag(ext.SamplingPriority, -1)
		root.SetBaggageItem("item", "x")
		ctx := root.Context().(internal.SpanContextV2Adapter)
		headers := TextMapCarrier(map[string]string{})
		err := tracer.Inject(ctx, headers)

		assert := assert.New(t)
		assert.Nil(err)

		sctx, err := tracer.Extract(headers)
		assert.Nil(err)

		xctx, ok := sctx.(internal.SpanContextV2Adapter)
		assert.True(ok)
		assert.Equal(xctx.Ctx.TraceID(), ctx.Ctx.TraceID())
		assert.Equal(xctx.Ctx.SpanID(), ctx.Ctx.SpanID())
		baggage := make(map[string]string)
		xctx.ForeachBaggageItem(func(k, v string) bool {
			baggage[k] = v
			return true
		})
		ctx.ForeachBaggageItem(func(k, v string) bool {
			assert.Equal(v, baggage[k])
			return true
		})
		xp, _ := xctx.Ctx.SamplingPriority()
		p, _ := ctx.Ctx.SamplingPriority()
		assert.Equal(xp, p)
	})
}

func TestNonePropagator(t *testing.T) {
	t.Run("inject/none", func(t *testing.T) {
		t.Setenv(headerPropagationStyleInject, "none")
		tracer := newTracer()
		defer tracer.Stop()
		root := tracer.StartSpan("web.request")
		root.SetTag(ext.SamplingPriority, -1)
		root.SetBaggageItem("item", "x")
		ctx, ok := root.Context().(internal.SpanContextV2Adapter)
		headers := TextMapCarrier(map[string]string{})
		err := tracer.Inject(ctx, headers)

		assert := assert.New(t)
		assert.True(ok)
		assert.Nil(err)
		assert.Len(headers, 0)
	})

	t.Run("extract/none", func(t *testing.T) {
		t.Setenv(headerPropagationStyleExtract, "none")
		assert := assert.New(t)
		tracer := newTracer()
		defer tracer.Stop()
		root := tracer.StartSpan("web.request")
		root.SetTag(ext.SamplingPriority, -1)
		root.SetBaggageItem("item", "x")
		headers := TextMapCarrier(map[string]string{})

		_, err := tracer.Extract(headers)

		assert.Equal(err, ErrSpanContextNotFound)
		assert.Len(headers, 0)
	})

	t.Run("inject,extract/none", func(t *testing.T) {
		t.Run("", func(t *testing.T) {
			t.Setenv(headerPropagationStyle, "NoNe")
			tracer := newTracer()
			defer tracer.Stop()
			root := tracer.StartSpan("web.request")
			root.SetTag(ext.SamplingPriority, -1)
			root.SetBaggageItem("item", "x")
			ctx, ok := root.Context().(internal.SpanContextV2Adapter)
			headers := TextMapCarrier(map[string]string{})
			err := tracer.Inject(ctx, headers)

			assert := assert.New(t)
			assert.True(ok)
			assert.Nil(err)
			assert.Len(headers, 0)

			_, err = tracer.Extract(headers)
			assert.Equal(err, ErrSpanContextNotFound)
		})
		t.Run("", func(t *testing.T) {
			t.Setenv(otelHeaderPropagationStyle, "NoNe")
			tracer := newTracer()
			defer tracer.Stop()
			root := tracer.StartSpan("web.request")
			root.SetTag(ext.SamplingPriority, -1)
			root.SetBaggageItem("item", "x")
			ctx, ok := root.Context().(internal.SpanContextV2Adapter)
			headers := TextMapCarrier(map[string]string{})
			err := tracer.Inject(ctx, headers)

			assert := assert.New(t)
			assert.True(ok)
			assert.Nil(err)
			assert.Len(headers, 0)

			_, err = tracer.Extract(headers)
			assert.Equal(err, ErrSpanContextNotFound)
		})
		t.Run("", func(t *testing.T) {
			//"DD_TRACE_PROPAGATION_STYLE_EXTRACT": "NoNe",
			//	"DD_TRACE_PROPAGATION_STYLE_INJECT": "none",
			t.Setenv(headerPropagationStyleExtract, "NoNe")
			t.Setenv(headerPropagationStyleInject, "NoNe")
			tracer := newTracer()
			defer tracer.Stop()
			root := tracer.StartSpan("web.request")
			root.SetTag(ext.SamplingPriority, -1)
			root.SetBaggageItem("item", "x")
			ctx, ok := root.Context().(internal.SpanContextV2Adapter)
			headers := TextMapCarrier(map[string]string{})
			err := tracer.Inject(ctx, headers)

			assert := assert.New(t)
			assert.True(ok)
			assert.Nil(err)
			assert.Len(headers, 0)

			_, err = tracer.Extract(headers)
			assert.Equal(err, ErrSpanContextNotFound)
		})
	})
}

func BenchmarkExtractDatadog(b *testing.B) {
	b.Setenv(headerPropagationStyleExtract, "datadog")
	propagator := NewPropagator(nil)
	carrier := TextMapCarrier(map[string]string{
		DefaultTraceIDHeader:  "1123123132131312313123123",
		DefaultParentIDHeader: "1212321131231312312312312",
		DefaultPriorityHeader: "-1",
		traceTagsHeader: `adad=ada2,adad=ada2,ad1d=ada2,adad=ada2,adad=ada2,
								adad=ada2,adad=aad2,adad=ada2,adad=ada2,adad=ada2,adad=ada2`,
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		propagator.Extract(carrier)
	}
}

func BenchmarkExtractW3C(b *testing.B) {
	b.Setenv(headerPropagationStyleExtract, "tracecontext")
	propagator := NewPropagator(nil)
	carrier := TextMapCarrier(map[string]string{
		traceparentHeader: "00-00000000000000001111111111111111-2222222222222222-01",
		tracestateHeader:  "dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
	})
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		propagator.Extract(carrier)
	}
}

// Regression test for https://github.com/DataDog/dd-trace-go/issues/1944
func TestPropagatingTagsConcurrency(_ *testing.T) {
	// This test ensures Injection can be done concurrently.
	trc := newTracer()
	defer trc.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 1_000; i++ {
		root := trc.StartSpan("test")
		wg.Add(5)
		for i := 0; i < 5; i++ {
			go func() {
				defer wg.Done()
				trc.Inject(root.Context(), TextMapCarrier(make(map[string]string)))
			}()
		}
		wg.Wait()
	}
}

func TestMalformedTID(t *testing.T) {
	tracer := newTracer()
	defer tracer.Stop()

	t.Run("datadog, short tid", func(t *testing.T) {
		headers := TextMapCarrier(map[string]string{
			DefaultTraceIDHeader:  "1234567890123456789",
			DefaultParentIDHeader: "987654321",
			traceTagsHeader:       "_dd.p.tid=1234567890abcde",
		})
		sctx, err := tracer.Extract(headers)
		assert.Nil(t, err)
		root := tracer.StartSpan("web.request", ChildOf(sctx))
		root.Finish()
		rm := root.(internal.SpanV2Adapter).Span.AsMap()
		assert.NotContains(t, rm, keyTraceID128)
	})

	t.Run("datadog, malformed tid", func(t *testing.T) {
		headers := TextMapCarrier(map[string]string{
			DefaultTraceIDHeader:  "1234567890123456789",
			DefaultParentIDHeader: "987654321",
			traceTagsHeader:       "_dd.p.tid=XXXXXXXXXXXXXXXX",
		})
		sctx, err := tracer.Extract(headers)
		assert.Nil(t, err)
		root := tracer.StartSpan("web.request", ChildOf(sctx))
		root.Finish()
		rm := root.(internal.SpanV2Adapter).Span.AsMap()
		assert.NotContains(t, rm, keyTraceID128)
	})

	t.Run("datadog, valid tid", func(t *testing.T) {
		headers := TextMapCarrier(map[string]string{
			DefaultTraceIDHeader:  "1234567890123456789",
			DefaultParentIDHeader: "987654321",
			traceTagsHeader:       "_dd.p.tid=640cfd8d00000000",
		})
		sctx, err := tracer.Extract(headers)
		assert.Nil(t, err)
		root := tracer.StartSpan("web.request", ChildOf(sctx))
		root.Finish()
		rm := root.(internal.SpanV2Adapter).Span.AsMap()
		assert.Equal(t, "640cfd8d00000000", rm[keyTraceID128])
	})
}

func BenchmarkInjectW3C(b *testing.B) {
	b.Setenv(headerPropagationStyleInject, "tracecontext")
	tracer := newTracer(WithLogger(log.DiscardLogger{}))
	defer tracer.Stop()
	root := tracer.StartSpan("test")
	defer root.Finish()

	ctx := root.Context().(internal.SpanContextV2Adapter)
	testutils.SetPropagatingTag(b, ctx.Ctx, tracestateHeader, "othervendor=t61rcWkgMzE,dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~")

	for i := 0; i < 100; i++ {
		// _dd.p. prefix is needed for w3c
		k := fmt.Sprintf("_dd.p.k%d", i)
		v := fmt.Sprintf("v%d", i)
		testutils.SetPropagatingTag(b, ctx.Ctx, k, v)
	}
	dst := map[string]string{}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tracer.Inject(root.Context(), TextMapCarrier(dst))
		assert.GreaterOrEqual(b, len(dst), 1)
	}
}
