// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
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

	_, err = propagator.Extract(TextMapCarrier(map[string]string{
		DefaultTraceIDHeader:  "3",
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

func TestTextMapPropagatorTraceTagsWithPriority(t *testing.T) {
	src := TextMapCarrier(map[string]string{
		DefaultPriorityHeader: "1",
		DefaultTraceIDHeader:  "1",
		DefaultParentIDHeader: "1",
		traceTagsHeader:       "hello=world,_dd.p.dm=934086a6-4",
	})
	tracer := newTracer()
	ctx, err := tracer.Extract(src)
	assert.Nil(t, err)
	sctx, ok := ctx.(*spanContext)
	assert.True(t, ok)
	child := tracer.StartSpan("test", ChildOf(sctx))
	childSpanID := child.Context().(*spanContext).spanID
	assert.Equal(t, map[string]string{
		"hello":    "world",
		"_dd.p.dm": "934086a6-4",
	}, sctx.trace.propagatingTags)
	dst := map[string]string{}
	err = tracer.Inject(child.Context(), TextMapCarrier(dst))
	assert.Nil(t, err)
	assert.Len(t, dst, 4)
	assert.Equal(t, strconv.Itoa(int(childSpanID)), dst["x-datadog-parent-id"])
	assert.Equal(t, "1", dst["x-datadog-trace-id"])
	assert.Equal(t, "1", dst["x-datadog-sampling-priority"])
	assertTraceTags(t, "hello=world,_dd.p.dm=934086a6-4", dst["x-datadog-tags"])
}

func TestTextMapPropagatorTraceTagsWithoutPriority(t *testing.T) {
	src := TextMapCarrier(map[string]string{
		DefaultTraceIDHeader:  "1",
		DefaultParentIDHeader: "1",
		traceTagsHeader:       "hello=world,_dd.p.dm=934086a6-4",
	})
	tracer := newTracer()
	ctx, err := tracer.Extract(src)
	assert.Nil(t, err)
	sctx, ok := ctx.(*spanContext)
	assert.True(t, ok)
	child := tracer.StartSpan("test", ChildOf(sctx))
	childSpanID := child.Context().(*spanContext).spanID
	assert.Equal(t, map[string]string{
		"hello":    "world",
		"_dd.p.dm": "934086a6-4",
	}, sctx.trace.propagatingTags)
	dst := map[string]string{}
	err = tracer.Inject(child.Context(), TextMapCarrier(dst))
	assert.Nil(t, err)
	assert.Len(t, dst, 4)
	assert.Equal(t, strconv.Itoa(int(childSpanID)), dst["x-datadog-parent-id"])
	assert.Equal(t, "1", dst["x-datadog-trace-id"])
	assert.Equal(t, "1", dst["x-datadog-sampling-priority"])
	assertTraceTags(t, "hello=world,_dd.p.dm=934086a6-4", dst["x-datadog-tags"])
}

func TestExtractOriginSynthetics(t *testing.T) {
	src := TextMapCarrier(map[string]string{
		originHeader:          "synthetics",
		DefaultTraceIDHeader:  "3",
		DefaultParentIDHeader: "0",
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
	assert.Equal(t, sctx.spanID, uint64(0))
	assert.Equal(t, sctx.traceID, uint64(3))
	assert.Equal(t, sctx.origin, "synthetics")
}

func TestTextMapPropagator(t *testing.T) {
	t.Run("InvalidTraceTagsHeader", func(t *testing.T) {
		src := TextMapCarrier(map[string]string{
			DefaultTraceIDHeader:  "1",
			DefaultParentIDHeader: "1",
			traceTagsHeader:       "hello=world,=", // invalid value
		})
		tracer := newTracer()
		ctx, err := tracer.Extract(src)
		assert.Nil(t, err)
		sctx, ok := ctx.(*spanContext)
		assert.True(t, ok)
		assert.Equal(t, "decoding_error", sctx.trace.tags["_dd.propagation_error"])
	})

	t.Run("ExtractTraceTagsTooLong", func(t *testing.T) {
		tags := make([]string, 0)
		for i := 0; i < 100; i++ {
			tags = append(tags, fmt.Sprintf("_dd.p.tag%d=value%d", i, i))
		}
		traceTags := strings.Join(tags, ",")
		src := TextMapCarrier(map[string]string{
			DefaultTraceIDHeader:  "1",
			DefaultParentIDHeader: "1",
			traceTagsHeader:       traceTags,
		})
		tracer := newTracer()
		ctx, err := tracer.Extract(src)
		assert.Nil(t, err)
		sctx, ok := ctx.(*spanContext)
		assert.True(t, ok)
		assert.Equal(t, "extract_max_size", sctx.trace.tags["_dd.propagation_error"])
	})

	t.Run("InjectTraceTagsTooLong", func(t *testing.T) {
		tracer := newTracer()
		child := tracer.StartSpan("test")
		for i := 0; i < 100; i++ {
			child.Context().(*spanContext).trace.setPropagatingTag(fmt.Sprintf("someKey%d", i), fmt.Sprintf("someValue%d", i))
		}
		childSpanID := child.Context().(*spanContext).spanID
		dst := map[string]string{}
		err := tracer.Inject(child.Context(), TextMapCarrier(dst))
		assert.Nil(t, err)
		assert.Equal(t, map[string]string{
			"x-datadog-parent-id":         strconv.Itoa(int(childSpanID)),
			"x-datadog-trace-id":          strconv.Itoa(int(childSpanID)),
			"x-datadog-sampling-priority": "1",
		}, dst)
		assert.Equal(t, "inject_max_size", child.Context().(*spanContext).trace.tags["_dd.propagation_error"])
	})

	t.Run("InvalidTraceTags", func(t *testing.T) {
		tracer := newTracer()
		internal.SetGlobalTracer(tracer)
		child := tracer.StartSpan("test")
		child.Context().(*spanContext).trace.setPropagatingTag("_dd.p.hello1", "world")  // valid value
		child.Context().(*spanContext).trace.setPropagatingTag("_dd.p.hello2", "world,") // invalid value
		childSpanID := child.Context().(*spanContext).spanID
		dst := map[string]string{}
		err := tracer.Inject(child.Context(), TextMapCarrier(dst))
		assert.Nil(t, err)
		assert.Len(t, dst, 4)
		assert.Equal(t, strconv.Itoa(int(childSpanID)), dst["x-datadog-parent-id"])
		assert.Equal(t, strconv.Itoa(int(childSpanID)), dst["x-datadog-trace-id"])
		assert.Equal(t, "1", dst["x-datadog-sampling-priority"])
		assertTraceTags(t, "_dd.p.dm=-1,_dd.p.hello1=world", dst["x-datadog-tags"])
		assert.Equal(t, "encoding_error", child.Context().(*spanContext).trace.tags["_dd.propagation_error"])
	})

	t.Run("InjectExtract", func(t *testing.T) {
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
	})
}

func testB3(t *testing.T, b3Header string) {
	t.Run("inject", func(t *testing.T) {
		os.Setenv("DD_PROPAGATION_STYLE_INJECT", b3Header)
		defer os.Unsetenv("DD_PROPAGATION_STYLE_INJECT")

		var tests = []struct {
			in  []uint64
			out map[string]string
		}{
			{
				[]uint64{1412508178991881, 1842642739201064},
				map[string]string{
					b3TraceIDHeader: "000504ab30404b09",
					b3SpanIDHeader:  "00068bdfb1eb0428",
				},
			},
			{
				[]uint64{9530669991610245, 9455715668862222},
				map[string]string{
					b3TraceIDHeader: "0021dc1807524785",
					b3SpanIDHeader:  "002197ec5d8a250e",
				},
			},
			{
				[]uint64{1, 1},
				map[string]string{
					b3TraceIDHeader: "0000000000000001",
					b3SpanIDHeader:  "0000000000000001",
				},
			},
		}

		for _, test := range tests {
			t.Run("", func(t *testing.T) {
				tracer := newTracer()
				root := tracer.StartSpan("web.request").(*span)
				root.SetTag(ext.SamplingPriority, -1)
				root.SetBaggageItem("item", "x")
				ctx, ok := root.Context().(*spanContext)
				ctx.traceID = test.in[0]
				ctx.spanID = test.in[1]
				headers := TextMapCarrier(map[string]string{})
				err := tracer.Inject(ctx, headers)

				assert := assert.New(t)
				assert.True(ok)
				assert.Nil(err)
				assert.Equal(test.out[b3TraceIDHeader], headers[b3TraceIDHeader])
				assert.Equal(test.out[b3SpanIDHeader], headers[b3SpanIDHeader])
			})
		}
	})

	t.Run("extract", func(t *testing.T) {
		os.Setenv("DD_PROPAGATION_STYLE_EXTRACT", b3Header)
		defer os.Unsetenv("DD_PROPAGATION_STYLE_EXTRACT")

		var tests = []struct {
			in  TextMapCarrier
			out []uint64 // contains [<trace_id>, <span_id>]
		}{
			{
				TextMapCarrier{
					b3TraceIDHeader: "1",
					b3SpanIDHeader:  "1",
				},
				[]uint64{1, 1},
			},
			{
				TextMapCarrier{
					b3TraceIDHeader: "feeb0599801f4700",
					b3SpanIDHeader:  "f8f5c76089ad8da5",
				},
				[]uint64{18368781661998368512, 17939463908140879269},
			},
			{
				TextMapCarrier{
					b3TraceIDHeader: "6e96719ded9c1864a21ba1551789e3f5",
					b3SpanIDHeader:  "a1eb5bf36e56e50e",
				},
				[]uint64{11681107445354718197, 11667520360719770894},
			},
		}

		for _, test := range tests {
			t.Run("", func(t *testing.T) {
				tracer := newTracer()
				assert := assert.New(t)
				ctx, err := tracer.Extract(test.in)
				assert.Nil(err)
				sctx, ok := ctx.(*spanContext)
				assert.True(ok)

				assert.Equal(sctx.traceID, test.out[0])
				assert.Equal(sctx.spanID, test.out[1])
			})
		}
	})

	t.Run("multiple", func(t *testing.T) {
		os.Setenv("DD_PROPAGATION_STYLE_EXTRACT", fmt.Sprintf("Datadog,%s", b3Header))
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
		p, ok := sctx.samplingPriority()
		assert.True(ok)
		assert.Equal(1, p)

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
		p, ok = sctx.samplingPriority()
		assert.True(ok)
		assert.Equal(2, p)
	})

}

func TestB3(t *testing.T) {
	testB3(t, "b3")
	testB3(t, "b3multi")

	t.Run("config", func(t *testing.T) {
		os.Setenv("DD_PROPAGATION_STYLE_INJECT", "datadog")
		defer os.Unsetenv("DD_PROPAGATION_STYLE_INJECT")

		var tests = []struct {
			in  []uint64
			out map[string]string
		}{
			{
				[]uint64{1412508178991881, 1842642739201064},
				map[string]string{
					b3TraceIDHeader: "000504ab30404b09",
					b3SpanIDHeader:  "00068bdfb1eb0428",
				},
			},
			{
				[]uint64{9530669991610245, 9455715668862222},
				map[string]string{
					b3TraceIDHeader: "0021dc1807524785",
					b3SpanIDHeader:  "002197ec5d8a250e",
				},
			},
			{
				[]uint64{1, 1},
				map[string]string{
					b3TraceIDHeader: "0000000000000001",
					b3SpanIDHeader:  "0000000000000001",
				},
			},
		}

		for _, test := range tests {
			t.Run("", func(t *testing.T) {
				tracer := newTracer(WithPropagator(NewPropagator(&PropagatorConfig{B3: true})))
				root := tracer.StartSpan("web.request").(*span)
				root.SetTag(ext.SamplingPriority, -1)
				root.SetBaggageItem("item", "x")
				ctx, ok := root.Context().(*spanContext)
				ctx.traceID = test.in[0]
				ctx.spanID = test.in[1]
				headers := TextMapCarrier(map[string]string{})
				err := tracer.Inject(ctx, headers)

				assert := assert.New(t)
				assert.True(ok)
				assert.Nil(err)
				assert.Equal(test.out[b3TraceIDHeader], headers[b3TraceIDHeader])
				assert.Equal(test.out[b3SpanIDHeader], headers[b3SpanIDHeader])
			})
		}
	})
}

func assertTraceTags(t *testing.T, expected, actual string) {
	assert.ElementsMatch(t, strings.Split(expected, ","), strings.Split(actual, ","))
}
