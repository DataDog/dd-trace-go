// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	v1ext "gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newBasicSpan is the OpenTracing Span constructor
func newBasicSpan(name string, opts ...StartSpanOption) ddtrace.Span {
	opts = append(
		[]StartSpanOption{
			StartTime(time.Now()),
		},
		opts...,
	)
	span := StartSpan(name, opts...)
	return span
}

func TestSpanBaggage(t *testing.T) {
	assert := assert.New(t)
	Start()
	defer Stop()

	span := newBasicSpan("web.request")
	span.SetBaggageItem("key", "value")
	assert.Equal("value", span.BaggageItem("key"))
}

func TestSpanContext(t *testing.T) {
	assert := assert.New(t)
	Start()
	defer Stop()

	span := newBasicSpan("web.request")
	assert.NotNil(span.Context())
}

func TestSpanOperationName(t *testing.T) {
	assert := assert.New(t)
	Start()
	defer Stop()

	span := newBasicSpan("web.request")
	span.SetOperationName("http.request")
	sm := span.(internal.SpanV2Adapter).Span.AsMap()
	assert.Equal("http.request", sm[ext.SpanName])
}

func TestShouldComputeStats(t *testing.T) {
	for _, tt := range []struct {
		metrics map[string]float64
		want    bool
	}{
		{map[string]float64{keyMeasured: 2}, false},
		{map[string]float64{keyMeasured: 1}, true},
		{map[string]float64{keyMeasured: 0}, false},
		{map[string]float64{keyTopLevel: 0}, false},
		{map[string]float64{keyTopLevel: 1}, true},
		{map[string]float64{keyTopLevel: 2}, false},
		{map[string]float64{keyTopLevel: 2, keyMeasured: 1}, true},
		{map[string]float64{keyTopLevel: 1, keyMeasured: 2}, true},
		{map[string]float64{keyTopLevel: 2, keyMeasured: 2}, false},
		{map[string]float64{}, false},
	} {
		t.Run("", func(t *testing.T) {
			assert.Equal(t, shouldComputeStats(&span{Metrics: tt.metrics}), tt.want)
		})
	}
}

func TestSpanFinishWithTime(t *testing.T) {
	assert := assert.New(t)
	Start()
	defer Stop()

	finishTime := time.Now().Add(10 * time.Second)
	span := newBasicSpan("web.request")
	span.Finish(FinishTime(finishTime))

	sm := span.(internal.SpanV2Adapter).Span.AsMap()
	duration := finishTime.UnixNano() - sm[ext.MapSpanStart].(int64)
	assert.Equal(duration, sm[ext.MapSpanDuration].(int64))
}

func TestSpanFinishWithNegativeDuration(t *testing.T) {
	assert := assert.New(t)
	Start()
	defer Stop()

	startTime := time.Now()
	finishTime := startTime.Add(-10 * time.Second)
	span := newBasicSpan("web.request", StartTime(startTime))
	sm := span.(internal.SpanV2Adapter).Span.AsMap()
	span.Finish(FinishTime(finishTime))
	assert.Equal(int64(0), sm[ext.MapSpanDuration].(int64))
}

func TestSpanFinishWithError(t *testing.T) {
	assert := assert.New(t)
	Start()
	defer Stop()

	err := errors.New("test error")
	span := newBasicSpan("web.request")
	span.Finish(WithError(err))
	sm := span.(internal.SpanV2Adapter).Span.AsMap()

	assert.Equal(int32(1), sm[ext.MapSpanError].(int32))
	assert.Equal("test error", sm[ext.ErrorMsg])
	assert.Equal("*errors.errorString", sm[ext.ErrorType])
	assert.NotEmpty(sm[ext.ErrorStack])
}

func TestSpanFinishWithErrorNoDebugStack(t *testing.T) {
	assert := assert.New(t)
	Start()
	defer Stop()

	err := errors.New("test error")
	span := newBasicSpan("web.request")
	span.Finish(WithError(err), NoDebugStack())
	sm := span.(internal.SpanV2Adapter).Span.AsMap()

	assert.Equal(int32(1), sm[ext.MapSpanError].(int32))
	assert.Equal("test error", sm[ext.ErrorMsg])
	assert.Equal("*errors.errorString", sm[ext.ErrorType])
	assert.Empty(sm[ext.ErrorStack])
}

func TestSpanFinishWithErrorStackFrames(t *testing.T) {
	assert := assert.New(t)
	Start()
	defer Stop()

	err := errors.New("test error")
	span := newBasicSpan("web.request")
	span.Finish(WithError(err), StackFrames(3, 1))
	sm := span.(internal.SpanV2Adapter).Span.AsMap()

	assert.Equal(int32(1), sm[ext.MapSpanError].(int32))
	assert.Equal("test error", sm[ext.ErrorMsg])
	assert.Equal("*errors.errorString", sm[ext.ErrorType])
	assert.Contains(sm[ext.ErrorStack], "tracer.TestSpanFinishWithErrorStackFrames")
	assert.Contains(sm[ext.ErrorStack], "tracer.(*Span).Finish")
	assert.Equal(strings.Count(sm[ext.ErrorStack].(string), "\n\t"), 3)
}

// nilStringer is used to test nil detection when setting tags.
type nilStringer struct {
	s string
}

// String incorrectly assumes than n will not be nil in order
// to ensure SetTag identifies nils.
func (n *nilStringer) String() string {
	return n.s
}

type panicStringer struct {
}

// String causes panic which SetTag should not handle.
func (p *panicStringer) String() string {
	panic("This should not be handled.")
}

func assertArray[T, W any](t *testing.T, span ddtrace.Span, key string, value []T, want []W) {
	span.SetTag(key, value)
	sm := span.(internal.SpanV2Adapter).Span.AsMap()
	for i, v := range want {
		assert.Equal(t, v, sm[fmt.Sprintf("%s.%d", key, i)])
	}
}

func TestSpanSetTag(t *testing.T) {
	assert := assert.New(t)
	Start()
	defer Stop()

	span := newBasicSpan("web.request")
	tC := []struct {
		key    string
		value  any
		want   any
		altKey string
	}{
		{key: "component", value: "tracer", want: "tracer"},
		{key: "tagInt", value: 1234, want: float64(1234)},
		{key: "tagStruct", value: struct{ A, B int }{1, 2}, want: "{1 2}"},
		{key: ext.Error, value: true, want: int32(1), altKey: ext.MapSpanError},
		{key: ext.Error, value: false, want: int32(0), altKey: ext.MapSpanError},
		{key: ext.Error, value: nil, want: int32(0), altKey: ext.MapSpanError},
		{key: ext.Error, value: "something else", want: int32(1), altKey: ext.MapSpanError},
		{key: v1ext.SamplingPriority, value: 2, want: float64(2), altKey: keySamplingPriority},
		{key: ext.AnalyticsEvent, value: true, want: 1.0},
		{key: ext.AnalyticsEvent, value: false, want: 0.0},
		{key: ext.ManualDrop, value: true, want: -1.0},
		{key: ext.ManualKeep, value: true, want: 2.0},
		{key: "some.bool", value: true, want: "true"},
		{key: "some.other.bool", value: false, want: "false"},
		{key: "time", value: (*time.Time)(nil), want: "<nil>"},
		{key: "nilStringer", value: (*nilStringer)(nil), want: "<nil>"},
	}
	for _, tc := range tC {
		tc := tc
		t.Run(tc.key, func(t *testing.T) {
			span.SetTag(tc.key, tc.value)
			sm := span.(internal.SpanV2Adapter).Span.AsMap()
			k := tc.key
			switch tc.key {
			case ext.AnalyticsEvent:
				k = ext.EventSampleRate
			case ext.ManualDrop, ext.ManualKeep:
				k = keySamplingPriority
			default:
				if tc.altKey != "" {
					k = tc.altKey
				}
			}
			assert.Equal(tc.want, sm[k])
		})
	}

	t.Run("arrays", func(t *testing.T) {
		assertArray(t, span, "somestrings", []string{"foo", "bar"}, []string{"foo", "bar"})
		assertArray(t, span, "somebools", []bool{true, false}, []string{"true", "false"})
		assertArray(t, span, "somenums", []int{-1, 5, 2}, []float64{-1., 5., 2.})
		assertArray(t, span, "someslices", [][]string{{"a, b, c"}, {"d"}, nil, {"e, f"}}, []string{"[a, b, c]", "[d]", "[]", "[e, f]"})
	})

	t.Run("panic", func(t *testing.T) {
		assert.Panics(func() {
			span.SetTag("panicStringer", &panicStringer{})
		})
	})

	t.Run("goerror", func(t *testing.T) {
		span.SetTag(ext.Error, errors.New("abc"))
		sm := span.(internal.SpanV2Adapter).Span.AsMap()
		assert.Equal(int32(1), sm[ext.MapSpanError].(int32))
		assert.Equal("abc", sm[ext.ErrorMsg])
		assert.Equal("*errors.errorString", sm[ext.ErrorType])
		assert.NotEmpty(sm[ext.ErrorStack])
	})
}

func TestUniqueTagKeys(t *testing.T) {
	assert := assert.New(t)
	Start()
	defer Stop()

	span := newBasicSpan("web.request")
	// check to see if setMeta correctly wipes out a metric tag
	span.SetTag("foo.bar", 12)
	span.SetTag("foo.bar", "val")

	sm := span.(internal.SpanV2Adapter).Span.AsMap()
	assert.Equal("val", sm["foo.bar"])

	// check to see if setMetric correctly wipes out a meta tag
	span.SetTag("foo.bar", "val")
	span.SetTag("foo.bar", 12)

	sm = span.(internal.SpanV2Adapter).Span.AsMap()
	assert.Equal(12.0, sm["foo.bar"])
}

func TestSpanLog(t *testing.T) {
	// this test is executed multiple times to ensure we clean up global state correctly
	noServiceTest := func(t *testing.T) {
		assert := assert.New(t)
		tracer, stop := startTestTracer(t)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.Context().SpanID(), span.Context().TraceID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	}
	t.Run("noservice_first", noServiceTest)

	t.Run("default", func(t *testing.T) {
		assert := assert.New(t)
		tracer, stop := startTestTracer(t, WithService("tracer.test"))
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.Context().SpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("env", func(t *testing.T) {
		assert := assert.New(t)
		tracer, stop := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.Context().SpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("version", func(t *testing.T) {
		assert := assert.New(t)
		tracer, stop := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"))
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.Context().SpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("full", func(t *testing.T) {
		assert := assert.New(t)
		tracer, stop := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.Context().SpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	// run no_service again: it should have forgotten the global state
	t.Run("no_service_after_full", noServiceTest)

	t.Run("subservice", func(t *testing.T) {
		assert := assert.New(t)
		tracer, stop := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request", ServiceName("subservice name"))
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.Context().SpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "tracer.test")
		t.Setenv("DD_VERSION", "1.2.3")
		t.Setenv("DD_ENV", "testenv")
		assert := assert.New(t)
		tracer, stop := startTestTracer(t)
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.Context().SpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("badformat", func(t *testing.T) {
		assert := assert.New(t)
		tracer, stop := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"))
		defer stop()
		span := tracer.StartSpan("test.request")
		expect := fmt.Sprintf(`%%!b(tracer.Span=dd.service=tracer.test dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0")`, span.Context().TraceID(), span.Context().SpanID())
		assert.Equal(expect, fmt.Sprintf("%b", span))
	})

	t.Run("notracer/options", func(t *testing.T) {
		assert := assert.New(t)
		tracer, stop := startTestTracer(t, WithService("tracer.test"), WithServiceVersion("1.2.3"), WithEnv("testenv"))
		span := tracer.StartSpan("test.request")
		stop()
		// no service, env, or version after the tracer is stopped
		expect := fmt.Sprintf(`dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.Context().SpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("notracer/env", func(t *testing.T) {
		t.Setenv("DD_SERVICE", "tracer.test")
		t.Setenv("DD_VERSION", "1.2.3")
		t.Setenv("DD_ENV", "testenv")
		assert := assert.New(t)
		tracer, stop := startTestTracer(t)
		span := tracer.StartSpan("test.request")
		stop()
		// service is not included: it is cleared when we stop the tracer
		// env, version are included: it reads the environment variable when there is no tracer
		expect := fmt.Sprintf(`dd.env=testenv dd.version=1.2.3 dd.trace_id="%d" dd.span_id="%d" dd.parent_id="0"`, span.Context().TraceID(), span.Context().SpanID())
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("128-bit-generation-only", func(t *testing.T) {
		// Generate 128 bit trace ids, but don't log them. So only the lower
		// 64 bits should be logged in decimal form.
		// DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED is true by default
		// DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED is false by default
		assert := assert.New(t)
		tracer, stop := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request", WithSpanID(87654321))
		span.Finish()
		expect := fmt.Sprintf(
			`dd.service=tracer.test dd.env=testenv dd.trace_id="%d" dd.span_id="87654321" dd.parent_id="0"`,
			span.Context().(internal.SpanContextV2Adapter).Ctx.TraceIDLower(),
		)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("128-bit-logging-only", func(t *testing.T) {
		// Logging 128-bit trace ids is enabled, but it's not present in
		// the span. So only the lower 64 bits should be logged in decimal form.
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
		t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "true")
		assert := assert.New(t)
		tracer, stop := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request", WithSpanID(87654321))
		span.Finish()
		expect := fmt.Sprintf(
			`dd.service=tracer.test dd.env=testenv dd.trace_id="%d" dd.span_id="87654321" dd.parent_id="0"`,
			span.Context().(internal.SpanContextV2Adapter).Ctx.TraceIDLower(),
		)
		assert.Equal(expect, fmt.Sprintf("%v", span))
	})

	t.Run("128-bit-logging-with-generation", func(t *testing.T) {
		// Logging 128-bit trace ids is enabled, and a 128-bit trace id, so
		// a quoted 32 byte hex string should be printed for the dd.trace_id.
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "true")
		t.Setenv("DD_TRACE_128_BIT_TRACEID_LOGGING_ENABLED", "true")
		assert := assert.New(t)
		tracer, stop := startTestTracer(t, WithService("tracer.test"), WithEnv("testenv"))
		defer stop()
		span := tracer.StartSpan("test.request", WithSpanID(87654321))
		span.Finish()
		sa := span.(internal.SpanV2Adapter)
		sctx := sa.Context().(internal.SpanContextV2Adapter)
		sm := sa.Span.AsMap()
		expect := fmt.Sprintf(`dd.service=tracer.test dd.env=testenv dd.trace_id=%q dd.span_id="87654321" dd.parent_id="0"`, sctx.TraceID128())
		assert.Equal(expect, fmt.Sprintf("%v", span))
		v, _ := sm[keyTraceID128]
		assert.NotEmpty(v)
	})
}

func TestRootSpanAccessor(t *testing.T) {
	tracer, stop := startTestTracer(t)
	defer stop()

	t.Run("nil-span", func(t *testing.T) {
		s := internal.SpanV2Adapter{Span: nil}
		require.Nil(t, s.Root())

		s = internal.SpanV2Adapter{Span: &v2.Span{}}
		require.Nil(t, s.Root())
	})

	t.Run("single-span", func(t *testing.T) {
		sp := tracer.StartSpan("root")
		require.Equal(t, sp, sp.(internal.SpanV2Adapter).Root())
		sp.Finish()
	})

	t.Run("single-span-finished", func(t *testing.T) {
		sp := tracer.StartSpan("root")
		sp.Finish()
		require.Equal(t, sp, sp.(internal.SpanV2Adapter).Root())
	})

	t.Run("root-with-children", func(t *testing.T) {
		root := tracer.StartSpan("root")
		defer root.Finish()
		child1 := tracer.StartSpan("child1", ChildOf(root.Context()))
		defer child1.Finish()
		child2 := tracer.StartSpan("child2", ChildOf(root.Context()))
		defer child2.Finish()
		child21 := tracer.StartSpan("child2.1", ChildOf(child2.Context()))
		defer child21.Finish()
		child211 := tracer.StartSpan("child2.1.1", ChildOf(child21.Context()))
		defer child211.Finish()

		require.Equal(t, root, root.(internal.SpanV2Adapter).Root())
		require.Equal(t, root, child1.(internal.SpanV2Adapter).Root())
		require.Equal(t, root, child2.(internal.SpanV2Adapter).Root())
		require.Equal(t, root, child21.(internal.SpanV2Adapter).Root())
		require.Equal(t, root, child211.(internal.SpanV2Adapter).Root())
	})

	t.Run("root-finished-with-children", func(t *testing.T) {
		root := tracer.StartSpan("root")
		root.Finish()
		child1 := tracer.StartSpan("child1", ChildOf(root.Context()))
		defer child1.Finish()
		child2 := tracer.StartSpan("child2", ChildOf(root.Context()))
		defer child2.Finish()
		child21 := tracer.StartSpan("child2.1", ChildOf(child2.Context()))
		defer child21.Finish()
		child211 := tracer.StartSpan("child2.1.1", ChildOf(child21.Context()))
		defer child211.Finish()

		require.Equal(t, root, root.(internal.SpanV2Adapter).Root())
		require.Equal(t, root, child1.(internal.SpanV2Adapter).Root())
		require.Equal(t, root, child2.(internal.SpanV2Adapter).Root())
		require.Equal(t, root, child21.(internal.SpanV2Adapter).Root())
		require.Equal(t, root, child211.(internal.SpanV2Adapter).Root())
	})
}

func TestSpanStartAndFinishLogs(t *testing.T) {
	tp := new(log.RecordLogger)
	tracer, stop := startTestTracer(t, WithLogger(tp), WithDebugMode(true))
	defer stop()

	span := tracer.StartSpan("op")
	time.Sleep(time.Millisecond * 2)
	span.Finish()
	started, finished := false, false
	for _, l := range tp.Logs() {
		if !started {
			started = strings.Contains(l, "DEBUG: Started Span")
		}
		if !finished {
			finished = strings.Contains(l, "DEBUG: Finished Span")
		}
		if started && finished {
			break
		}
	}
	require.True(t, started)
	require.True(t, finished)
}

func TestSetUserPropagatedUserID(t *testing.T) {
	tracer, stop := startTestTracer(t)
	defer stop()

	// Service 1, create span with propagated user
	sa := tracer.StartSpan("op").(internal.SpanV2Adapter)
	s := sa.Span
	s.SetUser("userino", WithPropagation())
	m := make(map[string]string)
	err := tracer.Inject(sa.Context(), TextMapCarrier(m))
	require.NoError(t, err)

	// Service 2, extract user
	c, err := tracer.Extract(TextMapCarrier(m))
	require.NoError(t, err)
	sa = tracer.StartSpan("op", ChildOf(c)).(internal.SpanV2Adapter)
	s = sa.Span
	s.SetUser("userino")
	sm := s.Root().AsMap()
	assert.True(t, sm["usr.id"] == "userino")
}

func BenchmarkSetTagMetric(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := strings.Split("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", "")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%len(keys)]
		span.SetTag(k, float64(12.34))
	}
}

func BenchmarkSetTagString(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := strings.Split("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", "")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := string(keys[i%len(keys)])
		span.SetTag(k, "some text")
	}
}

func BenchmarkSetTagStringPtr(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := strings.Split("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", "")
	str := "some text"
	v := &str

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%len(keys)]
		span.SetTag(k, v)
	}
}

func BenchmarkSetTagStringer(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := strings.Split("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", "")
	value := &stringer{}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%len(keys)]
		span.SetTag(k, value)
	}
}

func BenchmarkSetTagField(b *testing.B) {
	span := newBasicSpan("bench.span")
	keys := []string{ext.ServiceName, ext.ResourceName, ext.SpanType}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		k := keys[i%len(keys)]
		span.SetTag(k, "some text")
	}
}

type boomError struct{}

func (e *boomError) Error() string { return "boom" }

type stringer struct{}

func (s *stringer) String() string {
	return "string"
}

// TestConcurrentSpanSetTag tests that setting tags concurrently on a span directly or
// not (through tracer.Inject when trace sampling rules are in place) does not cause
// concurrent map writes. It seems to only be consistently reproduced with the -count=100
// flag when running go test, but it's a good test to have.
func TestConcurrentSpanSetTag(t *testing.T) {
	testConcurrentSpanSetTag(t)
	testConcurrentSpanSetTag(t)
}

func testConcurrentSpanSetTag(t *testing.T) {
	tracer, stop := startTestTracer(t, WithSamplingRules([]SamplingRule{NameRule("root", 1.0)}))
	defer stop()

	span := tracer.StartSpan("root")
	defer span.Finish()

	const n = 100
	wg := sync.WaitGroup{}
	wg.Add(n * 2)
	for i := 0; i < n; i++ {
		go func() {
			tracer.Inject(span.Context(), TextMapCarrier(map[string]string{}))
			wg.Done()
		}()
		go func() {
			span.SetTag("key", "value")
			wg.Done()
		}()
	}
	wg.Wait()
}
