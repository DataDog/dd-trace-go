// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/DataDog/datadog-go/v5/statsd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/httpmem"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
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
	t.Setenv(headerPropagationStyleExtract, "datadog")
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
	defer tracer.Stop()

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
	t.Setenv(headerPropagationStyleExtract, "datadog")
	t.Setenv(headerPropagationStyleInject, "datadog")
	src := TextMapCarrier(map[string]string{
		originHeader:          "synthetics",
		DefaultTraceIDHeader:  "1",
		DefaultParentIDHeader: "1",
	})
	tracer := newTracer()
	defer tracer.Stop()
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
	t.Setenv(headerPropagationStyleExtract, "datadog")
	t.Setenv(headerPropagationStyleInject, "datadog")
	src := TextMapCarrier(map[string]string{
		DefaultPriorityHeader: "1",
		DefaultTraceIDHeader:  "1",
		DefaultParentIDHeader: "1",
		traceTagsHeader:       "hello=world=,_dd.p.dm=934086a6-4",
	})
	tracer := newTracer()
	defer tracer.Stop()
	ctx, err := tracer.Extract(src)
	assert.Nil(t, err)
	sctx, ok := ctx.(*spanContext)
	assert.True(t, ok)
	child := tracer.StartSpan("test", ChildOf(sctx))
	childSpanID := child.Context().(*spanContext).spanID
	assert.Equal(t, map[string]string{
		"hello":    "world=",
		"_dd.p.dm": "934086a6-4",
	}, sctx.trace.propagatingTags)
	dst := map[string]string{}
	err = tracer.Inject(child.Context(), TextMapCarrier(dst))
	assert.Nil(t, err)
	assert.Len(t, dst, 4)
	assert.Equal(t, strconv.Itoa(int(childSpanID)), dst["x-datadog-parent-id"])
	assert.Equal(t, "1", dst["x-datadog-trace-id"])
	assert.Equal(t, "1", dst["x-datadog-sampling-priority"])
	assertTraceTags(t, "hello=world=,_dd.p.dm=934086a6-4", dst["x-datadog-tags"])
}

func TestTextMapPropagatorTraceTagsWithoutPriority(t *testing.T) {
	t.Setenv(headerPropagationStyleExtract, "datadog")
	t.Setenv(headerPropagationStyleInject, "datadog")
	src := TextMapCarrier(map[string]string{
		DefaultTraceIDHeader:  "1",
		DefaultParentIDHeader: "1",
		traceTagsHeader:       "hello=world,_dd.p.dm=934086a6-4",
	})
	tracer := newTracer()
	defer tracer.Stop()
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
	t.Setenv(headerPropagationStyleExtract, "datadog")
	src := TextMapCarrier(map[string]string{
		originHeader:          "synthetics",
		DefaultTraceIDHeader:  "3",
		DefaultParentIDHeader: "0",
	})
	tracer := newTracer()
	defer tracer.Stop()
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
		t.Setenv(headerPropagationStyleExtract, "datadog")
		src := TextMapCarrier(map[string]string{
			DefaultTraceIDHeader:  "1",
			DefaultParentIDHeader: "1",
			traceTagsHeader:       "hello=world,=", // invalid value
		})
		tracer := newTracer()
		defer tracer.Stop()
		ctx, err := tracer.Extract(src)
		assert.Nil(t, err)
		sctx, ok := ctx.(*spanContext)
		assert.True(t, ok)
		assert.Equal(t, "decoding_error", sctx.trace.tags["_dd.propagation_error"])
	})

	t.Run("ExtractTraceTagsTooLong", func(t *testing.T) {
		t.Setenv(headerPropagationStyleExtract, "datadog")
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
		defer tracer.Stop()
		ctx, err := tracer.Extract(src)
		assert.Nil(t, err)
		sctx, ok := ctx.(*spanContext)
		assert.True(t, ok)
		assert.Equal(t, "extract_max_size", sctx.trace.tags["_dd.propagation_error"])
	})

	t.Run("InjectTraceTagsTooLong", func(t *testing.T) {
		t.Setenv(headerPropagationStyleInject, "datadog")
		tracer := newTracer()
		defer tracer.Stop()
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
		t.Setenv(headerPropagationStyleInject, "datadog")
		tracer := newTracer()
		defer tracer.Stop()
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
		t.Setenv(headerPropagationStyleExtract, "datadog")
		t.Setenv(headerPropagationStyleInject, "datadog")
		propagator := NewPropagator(&PropagatorConfig{
			BaggagePrefix: "bg-",
			TraceHeader:   "tid",
			ParentHeader:  "pid",
		})
		tracer := newTracer(WithPropagator(propagator))
		defer tracer.Stop()
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

func TestEnvVars(t *testing.T) {
	var testEnvs []map[string]string

	s, c := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
	}))
	defer s.Close()

	t.Run("b3/b3multi inject", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleInject: "b3"},
			{headerPropagationStyleInjectDeprecated: "b3,none" /* none should have no affect */},
			{headerPropagationStyle: "b3"},
			{headerPropagationStyleInject: "b3multi", headerPropagationStyleInjectDeprecated: "none" /* none should have no affect */},
			{headerPropagationStyleInject: "b3multi", headerPropagationStyle: "none" /* none should have no affect */},
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
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
				t.Run(fmt.Sprintf("inject with env=%q", testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					root := tracer.StartSpan("web.request").(*span)
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
		}
	})

	t.Run("b3/b3multi extract", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleExtract: "b3"},
			{headerPropagationStyleExtractDeprecated: "b3"},
			{headerPropagationStyle: "b3,none" /* none should have no affect */},
			{headerPropagationStyleExtract: "b3multi", headerPropagationStyleExtractDeprecated: "none" /* none should have no affect */},
			{headerPropagationStyleExtract: "b3multi", headerPropagationStyle: "none" /* none should have no affect */},
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
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
				t.Run(fmt.Sprintf("extract with env=%q", testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					assert := assert.New(t)
					ctx, err := tracer.Extract(test.in)
					assert.Nil(err)
					sctx, ok := ctx.(*spanContext)
					assert.True(ok)

					assert.Equal(sctx.traceID, test.out[0])
					assert.Equal(sctx.spanID, test.out[1])
				})
			}
		}
	})

	t.Run("b3/b3multi extract invalid", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleExtract: "b3"},
			{headerPropagationStyleExtractDeprecated: "b3"},
			{headerPropagationStyle: "b3,none" /* none should have no affect */},
			{headerPropagationStyleExtract: "b3multi", headerPropagationStyleExtractDeprecated: "none" /* none should have no affect */},
			{headerPropagationStyleExtract: "b3multi", headerPropagationStyle: "none" /* none should have no affect */},
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
			var tests = []struct {
				in TextMapCarrier
			}{
				{
					TextMapCarrier{
						b3TraceIDHeader: "0",
						b3SpanIDHeader:  "0",
					},
				},
			}
			for _, test := range tests {
				t.Run(fmt.Sprintf("extract with env=%q", testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					assert := assert.New(t)
					_, err := tracer.Extract(test.in)
					assert.NotNil(err)
				})
			}
		}
	})

	t.Run("b3 single header extract", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleExtract: "B3 single header"},
			{headerPropagationStyleExtractDeprecated: "B3 single header"},
			{headerPropagationStyle: "B3 single header,none" /* none should have no affect */},
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
			var tests = []struct {
				in  TextMapCarrier
				out []uint64 // contains [<trace_id>, <span_id>, <sampling_decision>]
			}{
				{
					TextMapCarrier{
						b3SingleHeader: "1-2",
					},
					[]uint64{1, 2},
				},
				{
					TextMapCarrier{
						b3SingleHeader: "feeb0599801f4700-f8f5c76089ad8da5-1",
					},
					[]uint64{18368781661998368512, 17939463908140879269, 1},
				},
				{
					TextMapCarrier{
						b3SingleHeader: "6e96719ded9c1864a21ba1551789e3f5-a1eb5bf36e56e50e-0",
					},
					[]uint64{11681107445354718197, 11667520360719770894, 0},
				},
				{
					TextMapCarrier{
						b3SingleHeader: "6e96719ded9c1864a21ba1551789e3f5-a1eb5bf36e56e50e-d",
					},
					[]uint64{11681107445354718197, 11667520360719770894, 1},
				},
			}
			for _, test := range tests {
				t.Run(fmt.Sprintf("extract with env=%q", testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					assert := assert.New(t)
					ctx, err := tracer.Extract(test.in)
					require.Nil(t, err)
					sctx, ok := ctx.(*spanContext)
					assert.True(ok)

					assert.Equal(test.out[0], sctx.traceID)
					assert.Equal(test.out[1], sctx.spanID)
					if len(test.out) > 2 {
						require.NotNil(t, sctx.trace)
						assert.Equal(float64(test.out[2]), *sctx.trace.priority)
					}
				})
			}
		}
	})

	t.Run("b3 single header inject", func(t *testing.T) {
		t.Setenv(headerPropagationStyleInject, "b3 single header")
		var tests = []struct {
			in  []uint64
			out string
		}{
			{
				[]uint64{18368781661998368512, 17939463908140879269, 1},
				"feeb0599801f4700-f8f5c76089ad8da5-1",
			},
			{
				[]uint64{11681107445354718197, 11667520360719770894, 0},
				"a21ba1551789e3f5-a1eb5bf36e56e50e-0",
			},
		}
		for i, test := range tests {
			t.Run(fmt.Sprintf("b3 single header inject #%d", i), func(t *testing.T) {
				tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
				defer tracer.Stop()
				root := tracer.StartSpan("myrequest").(*span)
				ctx, ok := root.Context().(*spanContext)
				require.True(t, ok)
				ctx.traceID = test.in[0]
				ctx.spanID = test.in[1]
				ctx.setSamplingPriority(int(test.in[2]), samplernames.Unknown)
				headers := TextMapCarrier(map[string]string{})
				err := tracer.Inject(ctx, headers)
				require.Nil(t, err)
				assert.Equal(t, test.out, headers[b3SingleHeader])
			})
		}
	})

	t.Run("datadog inject", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleInject: "datadog"},
			{headerPropagationStyleInjectDeprecated: "datadog,none" /* none should have no affect */},
			{headerPropagationStyle: "datadog"},
			{headerPropagationStyleInject: "datadog", headerPropagationStyleInjectDeprecated: "none" /* none should have no affect */},
			{headerPropagationStyleInject: "datadog", headerPropagationStyle: "none" /* none should have no affect */},
		}

		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
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
				t.Run(fmt.Sprintf("inject with env=%q", testEnv), func(t *testing.T) {
					tracer := newTracer(WithPropagator(NewPropagator(&PropagatorConfig{B3: true})), WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					root := tracer.StartSpan("web.request").(*span)
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
		}
	})

	t.Run("datadog/b3 extract", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleExtract: "Datadog,b3"},
			{headerPropagationStyleExtractDeprecated: "Datadog,b3multi"},
			{headerPropagationStyle: "Datadog,b3"},
			{headerPropagationStyle: "none,Datadog,b3" /* none should have no affect */},
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
			var tests = []struct {
				in  TextMapCarrier
				out uint64
			}{
				{
					TextMapCarrier{
						b3TraceIDHeader: "1",
						b3SpanIDHeader:  "1",
						b3SampledHeader: "1",
					},
					1,
				},
				{
					TextMapCarrier{
						DefaultTraceIDHeader:  "2",
						DefaultParentIDHeader: "2",
						DefaultPriorityHeader: "2",
					},
					2,
				},
			}
			for _, test := range tests {
				t.Run(fmt.Sprintf("extract with env=%q", testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					assert := assert.New(t)

					ctx, err := tracer.Extract(test.in)
					assert.Nil(err)
					sctx, ok := ctx.(*spanContext)
					assert.True(ok)

					assert.Equal(sctx.traceID, test.out)
					assert.Equal(sctx.spanID, test.out)
					p, ok := sctx.samplingPriority()
					assert.True(ok)
					assert.Equal(int(test.out), p)
				})
			}
		}
	})

	t.Run("datadog inject/extract", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleInject: "datadog", headerPropagationStyleExtract: "datadog"},
			{headerPropagationStyleInjectDeprecated: "datadog", headerPropagationStyleExtractDeprecated: "datadog"},
			{headerPropagationStyleInject: "datadog", headerPropagationStyle: "datadog"},
			{headerPropagationStyle: "datadog"},
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
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
				t.Run(fmt.Sprintf("inject and extract with env=%q", testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
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

					sctx, err := tracer.Extract(headers)
					assert.Nil(err)

					xctx, ok := sctx.(*spanContext)
					assert.True(ok)
					assert.Equal(ctx.traceID, xctx.traceID)
					assert.Equal(ctx.spanID, xctx.spanID)
					assert.Equal(ctx.baggage, xctx.baggage)
					assert.Equal(ctx.trace.priority, xctx.trace.priority)
				})
			}
		}
	})

	t.Run("w3c extract", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleExtract: "traceContext"},
			{headerPropagationStyleExtractDeprecated: "traceContext,none" /* none should have no affect */},
			{headerPropagationStyle: "traceContext"},
			{headerPropagationStyleExtract: "traceContext", headerPropagationStyleExtractDeprecated: "none" /* none should have no affect */},
			{headerPropagationStyleExtract: "traceContext", headerPropagationStyle: "none" /* none should have no affect */},
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
			var tests = []struct {
				in              TextMapCarrier
				traceID         uint64
				fullTraceID     string
				spanID          uint64
				priority        int
				origin          string
				propagatingTags map[string]string
			}{
				{
					in: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111111-2222222222222222-01",
						tracestateHeader:  "dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
					fullTraceID: "00000000000000001111111111111111",
					traceID:     1229782938247303441,
					spanID:      2459565876494606882,
					priority:    2,
					origin:      "rum",
					propagatingTags: map[string]string{
						"w3cTraceID":   "00000000000000001111111111111111",
						"_dd.p.dm":     "-4",
						"_dd.p.usr.id": "baz64==",
						"tracestate":   "dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
				},
				{
					in: TextMapCarrier{
						traceparentHeader: "00-10000000000000000000000000000000-2222222222222222-01",
						tracestateHeader:  "dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
					fullTraceID: "10000000000000000000000000000000",
					traceID:     0x0,
					spanID:      2459565876494606882,
					priority:    2,
					origin:      "rum",
					propagatingTags: map[string]string{
						"w3cTraceID":   "10000000000000000000000000000000",
						"_dd.p.dm":     "-4",
						"_dd.p.usr.id": "baz64==",
						"tracestate":   "dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
				},
				{
					in: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111111-2222222222222222-03",
						tracestateHeader:  "dd=s:0;o:rum;t.dm:-2;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
					fullTraceID: "00000000000000001111111111111111",
					traceID:     1229782938247303441,
					spanID:      2459565876494606882,
					priority:    1,
					origin:      "rum",
					propagatingTags: map[string]string{
						"w3cTraceID":   "00000000000000001111111111111111",
						"_dd.p.dm":     "-2",
						"_dd.p.usr.id": "baz64==",
						"tracestate":   "dd=s:0;o:rum;t.dm:-2;t.usr.id:baz64~~,othervendor=t61rcWkgMzE"},
				},
				{
					in: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111111-2222222222222222-01",
						tracestateHeader:  "dd=s:2;o:rum:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
					fullTraceID: "00000000000000001111111111111111",
					traceID:     1229782938247303441,
					spanID:      2459565876494606882,
					priority:    2, // tracestate priority takes precedence
					origin:      "rum:rum",
					propagatingTags: map[string]string{
						"w3cTraceID":   "00000000000000001111111111111111",
						"_dd.p.dm":     "-4",
						"_dd.p.usr.id": "baz64==",
						"tracestate":   "dd=s:2;o:rum:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
				},
				{
					in: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111111-2222222222222222-01",
						tracestateHeader:  "dd=s:;o:rum:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
					fullTraceID: "00000000000000001111111111111111",
					traceID:     1229782938247303441,
					spanID:      2459565876494606882,
					priority:    1, // traceparent priority takes precedence
					origin:      "rum:rum",
					propagatingTags: map[string]string{
						"w3cTraceID":   "00000000000000001111111111111111",
						"_dd.p.dm":     "-4",
						"_dd.p.usr.id": "baz64==",
						"tracestate":   "dd=s:;o:rum:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
				},
				{
					in: TextMapCarrier{
						traceparentHeader: " \t-00-00000000000000001111111111111111-2222222222222222-01 \t-",
						tracestateHeader:  "othervendor=t61rcWkgMzE,dd=o:rum:rum;s:;t.dm:-4;t.usr.id:baz64~~",
					},
					fullTraceID: "00000000000000001111111111111111",
					traceID:     1229782938247303441,
					spanID:      2459565876494606882,
					priority:    1, // traceparent priority takes precedence
					origin:      "rum:rum",
					propagatingTags: map[string]string{
						"tracestate":   "othervendor=t61rcWkgMzE,dd=o:rum:rum;s:;t.dm:-4;t.usr.id:baz64~~",
						"w3cTraceID":   "00000000000000001111111111111111",
						"_dd.p.dm":     "-4",
						"_dd.p.usr.id": "baz64==",
					},
				},
				{
					in: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111111-2222222222222222-01",
						tracestateHeader:  "othervendor=t61rcWkgMzE,dd=o:2;s:fake_origin;t.dm:-4;t.usr.id:baz64~~,",
					},
					fullTraceID: "00000000000000001111111111111111",
					traceID:     1229782938247303441,
					spanID:      2459565876494606882,
					priority:    1,
					origin:      "2",
					propagatingTags: map[string]string{
						"tracestate":   "othervendor=t61rcWkgMzE,dd=o:2;s:fake_origin;t.dm:-4;t.usr.id:baz64~~,",
						"w3cTraceID":   "00000000000000001111111111111111",
						"_dd.p.dm":     "-4",
						"_dd.p.usr.id": "baz64==",
					},
				},
				{
					in: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111111-2222222222222222-01",
						tracestateHeader:  "othervendor=t61rcWkgMzE,dd=o:~_~;s:fake_origin;t.dm:-4;t.usr.id:baz64~~,",
					},
					fullTraceID: "00000000000000001111111111111111",
					traceID:     1229782938247303441,
					spanID:      2459565876494606882,
					priority:    1,
					origin:      "=_=",
					propagatingTags: map[string]string{
						"tracestate":   "othervendor=t61rcWkgMzE,dd=o:~_~;s:fake_origin;t.dm:-4;t.usr.id:baz64~~,",
						"w3cTraceID":   "00000000000000001111111111111111",
						"_dd.p.dm":     "-4",
						"_dd.p.usr.id": "baz64==",
					},
				},
			}
			for i, test := range tests {
				t.Run(fmt.Sprintf("#%v extract/valid  with env=%q", i, testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					assert := assert.New(t)
					ctx, err := tracer.Extract(test.in)
					if err != nil {
						t.Fatal(err)
					}
					sctx, ok := ctx.(*spanContext)
					assert.True(ok)

					assert.Equal(test.traceID, sctx.traceID)
					assert.Equal(test.spanID, sctx.spanID)
					assert.Equal(test.origin, sctx.origin)
					p, ok := sctx.samplingPriority()
					assert.True(ok)
					assert.Equal(test.priority, p)

					assert.Equal(test.fullTraceID, sctx.trace.propagatingTags[w3cTraceIDTag])
					assert.Equal(test.propagatingTags, sctx.trace.propagatingTags)
				})
			}
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
			var tests = []TextMapCarrier{
				{tracestateHeader: "dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE"},
				{traceparentHeader: "00-.2345678901234567890123456789012-1234567890123456-01"}, // invalid length
				{traceparentHeader: "00-1234567890123456789012345678901.-1234567890123456-01"}, // invalid length
				{traceparentHeader: "00-00000000000000001111111111111111-0000000000000000-01"}, // invalid length
				{traceparentHeader: "00-00000000000000000000000000000000-0001000000000000-01"}, // invalid length
				{traceparentHeader: "00-0000000000000.000000000000000000-0001000000000000-01"}, // invalid length
				{traceparentHeader: "00-1234567890123---ffffffffffffffff--fffffffffffffff-01"}, // invalid length
				{traceparentHeader: "00-_234567890123---ffffffffffffffff--fffffffffffffff-01"}, // invalid length
				{traceparentHeader: "00-12345678901234567890123456789011-1234567890123456-0."}, // invalid length
				{traceparentHeader: "00--2345678901234567890123456789011-1234567890123456-00"}, // invalid length
				{traceparentHeader: "00-2345678-901234567890123456789011-1234567890123456-00"}, // invalid length
				{traceparentHeader: "------------------------------------1234567890123456---"}, // invalid length
				{traceparentHeader: "0"},       // invalid length
				{traceparentHeader: "\t- -\t"}, // invalid length
				{
					traceparentHeader: "00-000000000000000011111111111121111-2222222222222222-01", // invalid length
					tracestateHeader:  "dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
				},
				{
					traceparentHeader: "100-00000000000000001111111111111111-2222222222222222-01", // invalid length
					tracestateHeader:  "dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
				},
				{
					traceparentHeader: "ff-00000000000000001111111111111111-2222222222222222-01", // invalid version
					tracestateHeader:  "dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
				},
			}

			for i, test := range tests {
				t.Run(fmt.Sprintf("#%v extract/invalid  with env=%q", i, testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					assert := assert.New(t)
					ctx, err := tracer.Extract(test)
					assert.NotNil(err)
					assert.Nil(ctx)
				})
			}
		}
	})

	t.Run("w3c extract / w3c,datadog inject", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleExtract: "traceContext"},
			{headerPropagationStyleExtractDeprecated: "traceContext,none" /* none should have no affect */},
			{headerPropagationStyle: "traceContext"},
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
			var tests = []struct {
				inHeaders   TextMapCarrier
				outHeaders  TextMapCarrier
				traceID     uint64
				fullTraceID string
				spanID      uint64
				priority    int
				origin      string
			}{
				{
					inHeaders: TextMapCarrier{
						traceparentHeader: "00-12345678901234567890123456789012-1234567890123456-00",
						tracestateHeader:  "foo=1,dd=s:-1",
					},
					outHeaders: TextMapCarrier{
						traceparentHeader:     "00-12345678901234567890123456789012-1234567890123456-00",
						tracestateHeader:      "dd=s:-1;o:synthetics,foo=1",
						DefaultPriorityHeader: "-1",
						DefaultTraceIDHeader:  "12345678901234567890123456789012",
						DefaultParentIDHeader: "1234567890123456",
					},
					fullTraceID: "12345678901234567890123456789012",
					traceID:     1229782938247303441,
					spanID:      2459565876494606882,
					priority:    -1,
					origin:      "synthetics",
				},
			}
			for i, test := range tests {
				t.Run(fmt.Sprintf("#%v extract/valid  with env=%q", i, testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					assert := assert.New(t)
					ctx, err := tracer.Extract(test.inHeaders)
					if err != nil {
						t.Fatal(err)
					}
					root := tracer.StartSpan("web.request", ChildOf(ctx)).(*span)
					defer root.Finish()
					sctx, ok := ctx.(*spanContext)
					sctx.origin = test.origin
					assert.True(ok)

					headers := TextMapCarrier(map[string]string{})
					err = tracer.Inject(sctx, headers)

					assert.True(ok)
					assert.Nil(err)
					assert.Equal(test.outHeaders[traceparentHeader], headers[traceparentHeader])
					assert.Equal(test.outHeaders[tracestateHeader], headers[tracestateHeader])
					ddTag := strings.SplitN(headers[tracestateHeader], ",", 2)[0]
					assert.LessOrEqual(len(ddTag), 256)
				})
			}
		}
	})

	t.Run("w3c inject", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleInject: "tracecontext", headerPropagationStyleExtract: "tracecontext"},
			{headerPropagationStyleInject: "datadog,tracecontext", headerPropagationStyleExtract: "datadog,tracecontext"},
			{headerPropagationStyleInjectDeprecated: "tracecontext", headerPropagationStyleExtractDeprecated: "tracecontext"},
			{headerPropagationStyleInject: "datadog,tracecontext", headerPropagationStyle: "datadog,tracecontext"},
			{headerPropagationStyle: "datadog,tracecontext"},
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
			var tests = []struct {
				out             TextMapCarrier
				traceID         uint64
				spanID          uint64
				priority        int
				origin          string
				propagatingTags map[string]string
			}{
				{
					out: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111111-2222222222222222-01",
						tracestateHeader:  "dd=s:2;o:rum;t.usr.id: baz64 ~~,othervendor=t61rcWkgMzE",
					},
					traceID:  1229782938247303441,
					spanID:   2459565876494606882,
					priority: 2,
					origin:   "rum",
					propagatingTags: map[string]string{
						"_dd.p.usr.id": " baz64 ==",
						"tracestate":   "othervendor=t61rcWkgMzE,dd=s:2;o:rum;t.dm:-4;t.usr.id:baz64~~",
					},
				},
				{
					out: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111111-2222222222222222-01",
						tracestateHeader:  "dd=s:1;o:rum;t.usr.id:baz64~~",
					},
					traceID:  1229782938247303441,
					spanID:   2459565876494606882,
					priority: 1,
					origin:   "rum",
					propagatingTags: map[string]string{
						"_dd.p.usr.id": "baz64==",
					},
				},
				{
					out: TextMapCarrier{
						traceparentHeader: "00-12300000000000001111111111111111-2222222222222222-01",
						tracestateHeader:  "dd=s:2;o:rum:rum;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
					traceID:  1229782938247303441,
					spanID:   2459565876494606882,
					priority: 2, // tracestate priority takes precedence
					origin:   "rum:rum",
					propagatingTags: map[string]string{
						"_dd.p.usr.id": "baz64==",
						"tracestate":   "dd=s:2;o:rum_rum;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
						w3cTraceIDTag:  "12300000000000001111111111111111",
					},
				},
				{
					out: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111111-2222222222222222-01",
						tracestateHeader:  "dd=s:1;o:rum:rum;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
					traceID:  1229782938247303441,
					spanID:   2459565876494606882,
					priority: 1, // traceparent priority takes precedence
					origin:   "rum:rum",
					propagatingTags: map[string]string{
						"_dd.p.usr.id": "baz64==",
						"tracestate":   "dd=s:1;o:rum:rum;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
				},
				{
					out: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111111-2222222222222222-00",
						tracestateHeader:  "dd=s:-1;o:rum:rum;t.usr.id:baz:64~~,othervendor=t61rcWkgMzE",
					},
					traceID:  1229782938247303441,
					spanID:   2459565876494606882,
					priority: -1, // traceparent priority takes precedence
					origin:   "rum:rum",
					propagatingTags: map[string]string{
						"_dd.p.usr.id": "baz:64==",
						"tracestate":   "dd=s:1;o:rum:rum;t.usr.id:baz64~~,othervendor=t61rcWkgMzE",
					},
				},
				{
					out: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111112-2222222222222222-00",
						tracestateHeader:  "dd=s:0;o:old_tracestate;t.usr.id:baz:64~~ ,a0=a:1,a1=a:1,a2=a:1,a3=a:1,a4=a:1,a5=a:1,a6=a:1,a7=a:1,a8=a:1,a9=a:1,a10=a:1,a11=a:1,a12=a:1,a13=a:1,a14=a:1,a15=a:1,a16=a:1,a17=a:1,a18=a:1,a19=a:1,a20=a:1,a21=a:1,a22=a:1,a23=a:1,a24=a:1,a25=a:1,a26=a:1,a27=a:1,a28=a:1,a29=a:1,a30=a:1",
					},
					traceID: 1229782938247303442,
					spanID:  2459565876494606882,
					origin:  "old_tracestate",
					propagatingTags: map[string]string{
						"_dd.p.usr.id": "baz:64== ",
						"tracestate":   "dd=o:very_long_origin_tag,a0=a:1,a1=a:1,a2=a:1,a3=a:1,a4=a:1,a5=a:1,a6=a:1,a7=a:1,a8=a:1,a9=a:1,a10=a:1,a11=a:1,a12=a:1,a13=a:1,a14=a:1,a15=a:1,a16=a:1,a17=a:1,a18=a:1,a19=a:1,a20=a:1,a21=a:1,a22=a:1,a23=a:1,a24=a:1,a25=a:1,a26=a:1,a27=a:1,a28=a:1,a29=a:1,a30=a:1,a31=a:1,a32=a:1",
					},
				},
				{
					out: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111112-2222222222222222-00",
						tracestateHeader:  "dd=s:0;o:old_tracestate;t.usr.id:baz:64~~,a0=a:1,a1=a:1,a2=a:1,a3=a:1,a4=a:1,a5=a:1,a6=a:1,a7=a:1,a8=a:1,a9=a:1,a10=a:1,a11=a:1,a12=a:1,a13=a:1,a14=a:1,a15=a:1,a16=a:1,a17=a:1,a18=a:1,a19=a:1,a20=a:1,a21=a:1,a22=a:1,a23=a:1,a24=a:1,a25=a:1,a26=a:1,a27=a:1,a28=a:1,a29=a:1,a30=a:1",
					},
					traceID: 1229782938247303442,
					spanID:  2459565876494606882,
					origin:  "old_tracestate",
					propagatingTags: map[string]string{
						"_dd.p.usr.id": "baz:64==",
						"tracestate":   "dd=o:very_long_origin_tag,a0=a:1,a1=a:1,a2=a:1,a3=a:1,a4=a:1,a5=a:1,a6=a:1,a7=a:1,a8=a:1,a9=a:1,a10=a:1,a11=a:1,a12=a:1,a13=a:1,a14=a:1,a15=a:1,a16=a:1,a17=a:1,a18=a:1,a19=a:1,a20=a:1,a21=a:1,a22=a:1,a23=a:1,a24=a:1,a25=a:1,a26=a:1,a27=a:1,a28=a:1,a29=a:1,a30=a:1,a31=a:1,a32=a:1",
					},
				},
				{
					out: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111112-2222222222222222-00",
						tracestateHeader:  "dd=s:0;o:old_tracestate;t.usr.id:baz:64~~,foo=bar",
					},
					traceID: 1229782938247303442,
					spanID:  2459565876494606882,
					origin:  "old_tracestate",
					propagatingTags: map[string]string{
						"_dd.p.usr.id": "baz:64==",
						"tracestate":   "foo=bar ",
					},
				},
				{
					out: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111112-2222222222222222-00",
						tracestateHeader:  "dd=s:0;o:old_tracestate;t.usr.id:baz:64__,foo=bar",
					},
					traceID: 1229782938247303442,
					spanID:  2459565876494606882,
					origin:  "old_tracestate",
					propagatingTags: map[string]string{
						"_dd.p.usr.id": "baz:64~~",
						"tracestate":   "\tfoo=bar\t",
					},
				},
				{
					out: TextMapCarrier{
						traceparentHeader: "00-00000000000000001111111111111112-2222222222222222-00",
						tracestateHeader:  "dd=s:0;o:~~_;t.usr.id:baz:64__,foo=bar",
					},
					traceID: 1229782938247303442,
					spanID:  2459565876494606882,
					origin:  "==~",
					propagatingTags: map[string]string{
						"_dd.p.usr.id": "baz:64~~",
						"tracestate":   "\tfoo=bar\t",
					},
				},
			}
			for i, test := range tests {
				t.Run(fmt.Sprintf("#%d w3c inject with env=%q", i, testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					assert := assert.New(t)
					root := tracer.StartSpan("web.request").(*span)
					root.SetTag(ext.SamplingPriority, test.priority)
					ctx, ok := root.Context().(*spanContext)
					ctx.origin = test.origin
					ctx.traceID = test.traceID
					ctx.spanID = test.spanID
					ctx.trace.propagatingTags = test.propagatingTags
					headers := TextMapCarrier(map[string]string{})
					err := tracer.Inject(ctx, headers)

					assert.True(ok)
					assert.Nil(err)
					assert.Equal(test.out[traceparentHeader], headers[traceparentHeader])
					assert.Equal(test.out[tracestateHeader], headers[tracestateHeader])
					ddTag := strings.SplitN(headers[tracestateHeader], ",", 2)[0]
					assert.LessOrEqual(len(ddTag), 256)
				})

				t.Run(fmt.Sprintf("w3c inject with env=%q / testing tag list-member limit", testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					assert := assert.New(t)
					root := tracer.StartSpan("web.request").(*span)
					root.SetTag(ext.SamplingPriority, ext.PriorityUserKeep)
					ctx, ok := root.Context().(*spanContext)
					ctx.origin = "old_tracestate"
					ctx.traceID = 1229782938247303442
					ctx.spanID = 2459565876494606882
					ctx.trace.propagatingTags = map[string]string{
						"tracestate": "valid_vendor=a:1",
					}
					// dd part of the tracestate must not exceed 256 characters
					for i := 0; i < 32; i++ {
						ctx.trace.propagatingTags[fmt.Sprintf("_dd.p.a%v", i)] = "i"
					}
					headers := TextMapCarrier(map[string]string{})
					err := tracer.Inject(ctx, headers)

					assert.True(ok)
					assert.Nil(err)
					assert.Equal("00-00000000000000001111111111111112-2222222222222222-01", headers[traceparentHeader])
					assert.Contains(headers[tracestateHeader], "valid_vendor=a:1")
					// iterating through propagatingTags map doesn't guarantee order in tracestate header
					ddTag := strings.SplitN(headers[tracestateHeader], ",", 2)[0]
					assert.Contains(ddTag, "s:2")
					assert.Contains(ddTag, "s:2")
					assert.Regexp(regexp.MustCompile("dd=[\\w:,]+"), ddTag)
					assert.LessOrEqual(len(ddTag), 256)
				})
			}
		}
	})

	t.Run("datadog extract / w3c,datadog inject", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleInject: "tracecontext,datadog", headerPropagationStyleExtract: "datadog"},
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
			var tests = []struct {
				outHeaders TextMapCarrier
				inHeaders  TextMapCarrier
			}{
				{
					outHeaders: TextMapCarrier{
						traceparentHeader: "00-000000000000000000000000075bcd15-000000003ade68b1-00",
						tracestateHeader:  "dd=s:-2;o:test.origin",
					},
					inHeaders: TextMapCarrier{
						DefaultTraceIDHeader:  "123456789",
						DefaultParentIDHeader: "987654321",
						DefaultPriorityHeader: "-2",
						originHeader:          "test.origin",
					},
				},
				{
					outHeaders: TextMapCarrier{
						traceparentHeader: "00-000000000000000000000000075bcd15-000000003ade68b1-00",
						tracestateHeader:  "dd=s:-2;o:synthetics___web",
					},
					inHeaders: TextMapCarrier{
						DefaultTraceIDHeader:  "123456789",
						DefaultParentIDHeader: "987654321",
						DefaultPriorityHeader: "-2",
						originHeader:          "synthetics;,~web",
					},
				},
			}
			for i, test := range tests {
				t.Run(fmt.Sprintf("#%d with env=%q", i, testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					assert := assert.New(t)
					ctx, err := tracer.Extract(test.inHeaders)
					assert.Nil(err)

					root := tracer.StartSpan("web.request", ChildOf(ctx)).(*span)
					defer root.Finish()
					sctx, ok := ctx.(*spanContext)
					headers := TextMapCarrier(map[string]string{})
					err = tracer.Inject(sctx, headers)

					assert.True(ok)
					assert.Nil(err)
					assert.Equal(test.outHeaders[traceparentHeader], headers[traceparentHeader])
					assert.Equal(test.outHeaders[tracestateHeader], headers[tracestateHeader])
					ddTag := strings.SplitN(headers[tracestateHeader], ",", 2)[0]
					assert.LessOrEqual(len(ddTag), 256)
				})
			}
		}
	})

	t.Run("w3c inject/extract", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleInject: "tracecontext", headerPropagationStyleExtract: "tracecontext"},
			{headerPropagationStyleInject: "datadog,tracecontext", headerPropagationStyleExtract: "datadog,tracecontext"},
			{headerPropagationStyleInjectDeprecated: "tracecontext", headerPropagationStyleExtractDeprecated: "tracecontext"},
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
			var tests = []struct {
				in       TextMapCarrier
				out      TextMapCarrier
				traceID  uint64
				spanID   uint64
				priority float64
				origin   string
			}{
				{
					in: TextMapCarrier{
						traceparentHeader: "00-12345678901234567890123456789012-1234567890123456-01",
						tracestateHeader:  "dd=s:2;o:rum;t.usr.id:baz64~~",
					},
					out: TextMapCarrier{
						traceparentHeader: "00-12345678901234567890123456789012-1234567890123456-01",
						tracestateHeader:  "dd=s:2;o:rum;t.usr.id:baz64~~",
					},
					traceID:  8687463697196027922,
					spanID:   1311768467284833366,
					priority: 2,
					origin:   "rum",
				},
				{
					in: TextMapCarrier{
						traceparentHeader: "00-12345678901234567890123456789012-1234567890123456-01",
						tracestateHeader:  "foo=1",
					},
					out: TextMapCarrier{
						traceparentHeader: "00-12345678901234567890123456789012-1234567890123456-01",
						tracestateHeader:  "dd=s:1,foo=1",
					},
					traceID:  8687463697196027922,
					spanID:   1311768467284833366,
					priority: 1,
				},
				{
					in: TextMapCarrier{
						traceparentHeader: "00-12345678901234567890123456789012-1234567890123456-01",
						tracestateHeader:  "dd=s:2;o:r~~;t.usr.id:baz64~~",
					},
					out: TextMapCarrier{
						traceparentHeader: "00-12345678901234567890123456789012-1234567890123456-01",
						tracestateHeader:  "dd=s:2;o:r~~;t.usr.id:baz64~~",
					},
					traceID:  8687463697196027922,
					spanID:   1311768467284833366,
					priority: 2,
					origin:   "r==",
				},
			}
			for i, test := range tests {
				t.Run(fmt.Sprintf("#%d w3c inject/extract with env=%q", i, testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					assert := assert.New(t)
					ctx, err := tracer.Extract(test.in)
					if err != nil {
						t.FailNow()
					}
					sctx, ok := ctx.(*spanContext)
					assert.True(ok)

					assert.Equal(test.traceID, sctx.traceID)
					assert.Equal(test.spanID, sctx.spanID)
					// idt this assert is right
					assert.Equal(test.origin, sctx.origin)
					assert.Equal(test.priority, *sctx.trace.priority)

					headers := TextMapCarrier(map[string]string{})
					err = tracer.Inject(ctx, headers)
					assert.Nil(err)

					assert.Equal(test.out[traceparentHeader], headers[traceparentHeader])
					assert.Equal(test.out[tracestateHeader], headers[tracestateHeader])
					ddTag := strings.SplitN(headers[tracestateHeader], ",", 2)[0]
					assert.LessOrEqual(len(ddTag), 256)
				})
			}
		}
	})

	t.Run("w3c extract,update span, inject", func(t *testing.T) {
		testEnvs = []map[string]string{
			{headerPropagationStyleInject: "tracecontext", headerPropagationStyleExtract: "tracecontext"},
			{headerPropagationStyleInject: "datadog,tracecontext", headerPropagationStyleExtract: "datadog,tracecontext"},
			{headerPropagationStyleInjectDeprecated: "tracecontext", headerPropagationStyleExtractDeprecated: "tracecontext"},
		}
		for _, testEnv := range testEnvs {
			for k, v := range testEnv {
				t.Setenv(k, v)
			}
			var tests = []struct {
				in       TextMapCarrier
				out      TextMapCarrier
				traceID  uint64
				spanID   uint64
				parentID uint64
				priority float64
				origin   string
			}{
				{
					in: TextMapCarrier{
						traceparentHeader: "00-12345678901234567890123456789012-1234567890123456-01",
						tracestateHeader:  "dd=s:2;o:rum;t.usr.id:baz64~~",
					},
					out: TextMapCarrier{
						traceparentHeader: "00-12345678901234567890123456789012-0000000000000001-01",
						tracestateHeader:  "dd=s:1;o:rum;t.usr.id:baz64~~",
					},
					traceID:  8687463697196027922,
					spanID:   1,
					parentID: 1311768467284833366,
					priority: 1,
				},
			}
			for i, test := range tests {
				t.Run(fmt.Sprintf("#%d w3c inject/extract with env=%q", i, testEnv), func(t *testing.T) {
					tracer := newTracer(WithHTTPClient(c), withStatsdClient(&statsd.NoOpClient{}))
					defer tracer.Stop()
					assert := assert.New(t)
					pCtx, err := tracer.Extract(test.in)
					if err != nil {
						t.FailNow()
					}
					s := tracer.StartSpan("op", ChildOf(pCtx), WithSpanID(1))
					sctx, ok := s.Context().(*spanContext)
					assert.True(ok)
					// changing priority must set ctx.updated = true
					if test.priority != 0 {
						sctx.setSamplingPriority(int(test.priority), samplernames.Unknown)
					}
					assert.Equal(true, sctx.updated)

					headers := TextMapCarrier(map[string]string{})
					err = tracer.Inject(s.Context(), headers)
					assert.Equal(test.traceID, sctx.traceID)
					assert.Equal(test.parentID, sctx.span.ParentID)
					assert.Equal(test.spanID, sctx.spanID)
					assert.Equal(test.out[traceparentHeader], headers[traceparentHeader])
					assert.Equal(test.out[tracestateHeader], headers[tracestateHeader])
					ddTag := strings.SplitN(headers[tracestateHeader], ",", 2)[0]
					assert.LessOrEqual(len(ddTag), 256)
				})
			}
		}
	})
}

func TestNonePropagator(t *testing.T) {
	t.Run("inject/none", func(t *testing.T) {
		t.Setenv(headerPropagationStyleInject, "none")
		tracer := newTracer()
		defer tracer.Stop()
		root := tracer.StartSpan("web.request").(*span)
		root.SetTag(ext.SamplingPriority, -1)
		root.SetBaggageItem("item", "x")
		ctx, ok := root.Context().(*spanContext)
		ctx.traceID = 1
		ctx.spanID = 1
		headers := TextMapCarrier(map[string]string{})
		err := tracer.Inject(ctx, headers)

		assert := assert.New(t)
		assert.True(ok)
		assert.Nil(err)
		assert.Len(headers, 0)
	})

	t.Run("inject/none,b3", func(t *testing.T) {
		t.Setenv(headerPropagationStyleInject, "none,b3")
		tp := new(testLogger)
		tracer := newTracer(WithLogger(tp))
		defer tracer.Stop()
		// reinitializing to capture log output, since propagators are parsed before logger is set
		tracer.config.propagator = NewPropagator(&PropagatorConfig{})
		root := tracer.StartSpan("web.request").(*span)
		root.SetTag(ext.SamplingPriority, -1)
		root.SetBaggageItem("item", "x")
		ctx, ok := root.Context().(*spanContext)
		ctx.traceID = 1
		ctx.spanID = 1
		headers := TextMapCarrier(map[string]string{})
		err := tracer.Inject(ctx, headers)

		assert := assert.New(t)
		assert.True(ok)
		assert.Nil(err)
		assert.Equal("0000000000000001", headers[b3TraceIDHeader])
		assert.Equal("0000000000000001", headers[b3SpanIDHeader])
		assert.Contains(tp.lines[0], "Propagator \"none\" has no effect when combined with other propagators. "+
			"To disable the propagator, set to `none`")
	})

	t.Run("extract/none", func(t *testing.T) {
		t.Setenv(headerPropagationStyleExtract, "none")
		assert := assert.New(t)
		tracer := newTracer()
		defer tracer.Stop()
		root := tracer.StartSpan("web.request").(*span)
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
			root := tracer.StartSpan("web.request").(*span)
			root.SetTag(ext.SamplingPriority, -1)
			root.SetBaggageItem("item", "x")
			ctx, ok := root.Context().(*spanContext)
			ctx.traceID = 1
			ctx.spanID = 1
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
			root := tracer.StartSpan("web.request").(*span)
			root.SetTag(ext.SamplingPriority, -1)
			root.SetBaggageItem("item", "x")
			ctx, ok := root.Context().(*spanContext)
			ctx.traceID = 1
			ctx.spanID = 1
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

func assertTraceTags(t *testing.T, expected, actual string) {
	assert.ElementsMatch(t, strings.Split(expected, ","), strings.Split(actual, ","))
}
