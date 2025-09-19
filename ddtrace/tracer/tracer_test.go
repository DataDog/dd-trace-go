// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	llog "log"
	"net/http"
	"os"
	"runtime"
	rt "runtime/trace"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	maininternal "gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func id128FromSpan(assert *assert.Assertions, ctx ddtrace.SpanContext) string {
	var w3Cctx ddtrace.SpanContextW3C
	var ok bool
	w3Cctx, ok = ctx.(ddtrace.SpanContextW3C)
	assert.True(ok)
	id := w3Cctx.TraceID128()
	assert.Len(id, 32)
	return id
}

var (
	// timeMultiplicator specifies by how long to extend waiting times.
	// It may be altered in some environments (like AppSec) where things
	// move slower and could otherwise create flaky tests.
	timeMultiplicator = time.Duration(1)

	// integration indicates if the test suite should run integration tests.
	integration bool

	// traceStartSize is the initial size of our trace buffer,
	// by default we allocate for a handful of spans within the trace,
	// reasonable as span is actually way bigger, and avoids re-allocating
	// over and over. Could be fine-tuned at runtime.
	traceStartSize = 10
	// traceMaxSize is the maximum number of spans we keep in memory for a
	// single trace. This is to avoid memory leaks. If more spans than this
	// are added to a trace, then the trace is dropped and the spans are
	// discarded. Adding additional spans after a trace is dropped does
	// nothing.
	traceMaxSize = int(1e5)
)

func TestMain(m *testing.M) {
	if maininternal.BoolEnv("DD_APPSEC_ENABLED", false) {
		// things are slower with AppSec; double wait times
		timeMultiplicator = time.Duration(2)
	}
	_, integration = os.LookupEnv("INTEGRATION")
	os.Exit(m.Run())
}

func TestTracerStart(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		Start()
		defer Stop()
		trc := internal.GetGlobalTracer().(internal.TracerV2Adapter).Tracer
		require.NotNil(t, trc)
	})

	t.Run("dd_tracing_not_enabled", func(t *testing.T) {
		t.Setenv("DD_TRACE_ENABLED", "false")
		Start()
		defer Stop()
		trc := internal.GetGlobalTracer().(internal.TracerV2Adapter).Tracer
		if _, ok := trc.(*v2.NoopTracer); !ok {
			t.Fail()
		}
	})

	t.Run("otel_tracing_not_enabled", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "none")
		Start()
		defer Stop()
		trc := internal.GetGlobalTracer().(internal.TracerV2Adapter).Tracer
		if _, ok := trc.(v2.Tracer); !ok {
			t.Fail()
		}
		if _, ok := trc.(*v2.NoopTracer); !ok {
			t.Fail()
		}
	})
}

func TestTracerStartSpan(t *testing.T) {
	t.Run("generic", func(t *testing.T) {
		tracer := newTracer()
		defer tracer.Stop()
		span := tracer.StartSpan("web.request").(internal.SpanV2Adapter).Span
		assert := assert.New(t)
		sm := span.AsMap()
		assert.NotEqual(uint64(0), sm[ext.MapSpanTraceID])
		assert.NotEqual(uint64(0), sm[ext.MapSpanID])
		assert.Equal(uint64(0), sm[ext.MapSpanParentID])
		assert.Equal("web.request", sm[ext.SpanName])
		assert.Regexp(`tracer\.test(\.exe)?`, sm[ext.ServiceName])
		assert.Contains([]float64{
			ext.PriorityAutoReject,
			ext.PriorityAutoKeep,
		}, sm[keySamplingPriority])
		// A span is not measured unless made so specifically
		_, ok := sm[keyMeasured]
		assert.False(ok)
		assert.NotEqual("", sm[ext.RuntimeID])
	})

	t.Run("priority", func(t *testing.T) {
		tracer := newTracer()
		defer tracer.Stop()
		span := tracer.StartSpan("web.request", Tag(ext.ManualKeep, true)).(internal.SpanV2Adapter).Span
		sm := span.AsMap()
		assert.Equal(t, float64(ext.PriorityUserKeep), sm[keySamplingPriority])
	})

	t.Run("name", func(t *testing.T) {
		tracer := newTracer()
		defer tracer.Stop()
		span := tracer.StartSpan("/home/user", Tag(ext.SpanName, "db.query")).(internal.SpanV2Adapter).Span
		sm := span.AsMap()
		assert.Equal(t, "db.query", sm[ext.SpanName])
		assert.Equal(t, "/home/user", sm[ext.ResourceName])
	})

	t.Run("measured_top_level", func(t *testing.T) {
		tracer := newTracer()
		defer tracer.Stop()
		span := tracer.StartSpan("/home/user", Measured()).(internal.SpanV2Adapter).Span
		sm := span.AsMap()
		_, ok := sm[keyMeasured]
		assert.False(t, ok)
		assert.Equal(t, 1.0, sm[keyTopLevel])
	})

	t.Run("measured_non_top_level", func(t *testing.T) {
		tracer := newTracer()
		defer tracer.Stop()
		parent := tracer.StartSpan("/home/user")
		child := tracer.StartSpan("home/user", Measured(), ChildOf(parent.Context())).(internal.SpanV2Adapter).Span
		sm := child.AsMap()
		assert.Equal(t, 1.0, sm[keyMeasured])
	})

	t.Run("attribute_schema_is_set_v0", func(t *testing.T) {
		t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
		tracer := newTracer()
		defer tracer.Stop()
		parent := tracer.StartSpan("/home/user").(internal.SpanV2Adapter)
		psm := parent.Span.AsMap()
		child := tracer.StartSpan("home/user", ChildOf(parent.Context())).(internal.SpanV2Adapter)
		csm := child.Span.AsMap()
		assert.Contains(t, psm, "_dd.trace_span_attribute_schema")
		assert.Equal(t, 0.0, psm["_dd.trace_span_attribute_schema"])
		assert.NotContains(t, csm, "_dd.trace_span_attribute_schema")
	})

	t.Run("attribute_schema_is_set_v1", func(t *testing.T) {
		t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
		tracer := newTracer()
		defer tracer.Stop()
		parent := tracer.StartSpan("/home/user")
		child := tracer.StartSpan("home/user", ChildOf(parent.Context()))
		psm := parent.(internal.SpanV2Adapter).Span.AsMap()
		csm := child.(internal.SpanV2Adapter).Span.AsMap()
		assert.Contains(t, psm, "_dd.trace_span_attribute_schema")
		assert.Equal(t, 1.0, psm["_dd.trace_span_attribute_schema"])
		assert.NotContains(t, csm, "_dd.trace_span_attribute_schema")
	})

	t.Run("attribute_schema_is_set_wrong_value", func(t *testing.T) {
		t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "bad-version")
		tracer := newTracer()
		defer tracer.Stop()
		parent := tracer.StartSpan("/home/user")
		child := tracer.StartSpan("home/user", ChildOf(parent.Context()))
		psm := parent.(internal.SpanV2Adapter).Span.AsMap()
		csm := child.(internal.SpanV2Adapter).Span.AsMap()
		assert.Contains(t, psm, "_dd.trace_span_attribute_schema")
		assert.Equal(t, 0.0, psm["_dd.trace_span_attribute_schema"])
		assert.NotContains(t, csm, "_dd.trace_span_attribute_schema")
	})
}

func TestTracerRuntimeMetrics(t *testing.T) {
	t.Run("on", func(t *testing.T) {
		tp := new(log.RecordLogger)
		tp.Ignore("appsec: ", "telemetry")
		tracer := newTracer(WithRuntimeMetrics(), WithLogger(tp), WithDebugMode(true), WithEnv("test"))
		defer tracer.Stop()
		assert.Contains(t, tp.Logs()[1], "DEBUG: Runtime metrics")
	})

	t.Run("dd-env", func(t *testing.T) {
		t.Setenv("DD_RUNTIME_METRICS_ENABLED", "true")
		tp := new(log.RecordLogger)
		tp.Ignore("appsec: ", "telemetry")
		tracer := newTracer(WithLogger(tp), WithDebugMode(true), WithEnv("test"))
		defer tracer.Stop()
		assert.Contains(t, tp.Logs()[1], "DEBUG: Runtime metrics")
	})
}

func TestTracerStartSpanOptions(t *testing.T) {
	tracer := newTracer()
	defer tracer.Stop()
	now := time.Now()
	opts := []StartSpanOption{
		SpanType("test"),
		ServiceName("test.service"),
		ResourceName("test.resource"),
		StartTime(now),
		WithSpanID(420),
	}
	span := tracer.StartSpan("web.request", opts...).(internal.SpanV2Adapter).Span
	sm := span.AsMap()
	assert := assert.New(t)
	assert.Equal("test", sm[ext.SpanType])
	assert.Equal("test.service", sm[ext.ServiceName])
	assert.Equal("test.resource", sm[ext.ResourceName])
	assert.Equal(now.UnixNano(), sm[ext.MapSpanStart])
	assert.Equal(uint64(420), span.Context().SpanID())
	assert.Equal(uint64(420), span.Context().TraceIDLower())
	assert.Equal(1.0, sm[keyTopLevel])
}

func TestTracerStartSpanOptions128(t *testing.T) {
	tracer := newTracer()
	defer tracer.Stop()
	t.Run("64-bit-trace-id", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
		opts := []StartSpanOption{
			WithSpanID(987654),
		}
		sa := tracer.StartSpan("web.request", opts...).(internal.SpanV2Adapter)
		s := sa.Span
		sm := s.AsMap()
		assert.Equal(uint64(987654), s.Context().SpanID())
		assert.Equal(uint64(987654), s.Context().TraceIDLower())
		id := id128FromSpan(assert, sa.Context())
		assert.Empty(sm[keyTraceID128])
		idBytes, err := hex.DecodeString(id)
		assert.NoError(err)
		assert.Equal(uint64(0), binary.BigEndian.Uint64(idBytes[:8])) // high 64 bits should be 0
		assert.Equal(s.Context().TraceIDLower(), binary.BigEndian.Uint64(idBytes[8:]))
	})
	t.Run("128-bit-trace-id", func(t *testing.T) {
		assert := assert.New(t)
		// 128-bit trace ids are enabled by default.
		opts128 := []StartSpanOption{
			WithSpanID(987654),
			StartTime(time.Unix(123456, 0)),
		}
		sa := tracer.StartSpan("web.request", opts128...).(internal.SpanV2Adapter)
		s := sa.Span
		assert.Equal(uint64(987654), s.Context().SpanID())
		assert.Equal(uint64(987654), s.Context().TraceIDLower())
		id := id128FromSpan(assert, sa.Context())
		// hex_encoded(<32-bit unix seconds> <32 bits of zero> <64 random bits>)
		// 0001e240 (123456) + 00000000 (zeros) + 00000000000f1206 (987654)
		assert.Equal("0001e2400000000000000000000f1206", id)
		s.Finish()
		sm := s.AsMap()
		assert.Equal(id[:16], sm[keyTraceID128])
	})
}

func TestTracerStartChildSpan(t *testing.T) {
	t.Run("own-service", func(t *testing.T) {
		assert := assert.New(t)
		tracer := newTracer()
		defer tracer.Stop()
		root := tracer.StartSpan("web.request", ServiceName("root-service"))
		child := tracer.StartSpan("db.query",
			ChildOf(root.Context()),
			ServiceName("child-service"),
			WithSpanID(69))

		assert.NotEqual(uint64(0), child.Context().TraceID())
		assert.NotEqual(uint64(0), child.Context().SpanID())
		assert.Equal(root.Context().SpanID(), child.Context().TraceID())
		assert.Equal(root.Context().TraceID(), child.Context().TraceID())
		assert.Equal(uint64(69), child.Context().SpanID())

		rsm := root.(internal.SpanV2Adapter).Span.AsMap()
		csm := child.(internal.SpanV2Adapter).Span.AsMap()
		assert.Equal("child-service", csm[ext.ServiceName])
		// the root and child are both marked as "top level"
		assert.Equal(1.0, rsm[keyTopLevel])
		assert.Equal(1.0, csm[keyTopLevel])
	})

	t.Run("inherit-service", func(t *testing.T) {
		assert := assert.New(t)
		tracer := newTracer()
		defer tracer.Stop()
		root := tracer.StartSpan("web.request", ServiceName("root-service"))
		child := tracer.StartSpan("db.query", ChildOf(root.Context()))

		rsm := root.(internal.SpanV2Adapter).Span.AsMap()
		csm := child.(internal.SpanV2Adapter).Span.AsMap()
		assert.Equal("root-service", csm[ext.ServiceName])
		// the root is marked as "top level", but the child is not
		assert.Equal(1.0, rsm[keyTopLevel])
		assert.NotContains(csm, keyTopLevel)
	})
}

func TestStartSpanOrigin(t *testing.T) {
	t.Setenv(headerPropagationStyleExtract, "datadog")
	t.Setenv(headerPropagationStyleInject, "datadog")
	assert := assert.New(t)

	tracer := newTracer()
	defer tracer.Stop()

	carrier := TextMapCarrier(map[string]string{
		DefaultTraceIDHeader:  "1",
		DefaultParentIDHeader: "1",
		originHeader:          "synthetics",
	})
	ctx, err := tracer.Extract(carrier)
	assert.Nil(err)

	// first child contains tag
	child := tracer.StartSpan("child", ChildOf(ctx))
	sm := child.(internal.SpanV2Adapter).Span.AsMap()
	assert.Equal("synthetics", sm[keyOrigin])

	// secondary child doesn't
	child2 := tracer.StartSpan("child2", ChildOf(child.Context()))
	sm = child2.(internal.SpanV2Adapter).Span.AsMap()
	assert.Empty(sm[keyOrigin])

	// but injecting its context marks origin
	carrier2 := TextMapCarrier(map[string]string{})
	err = tracer.Inject(child2.Context(), carrier2)
	assert.Nil(err)
	assert.Equal("synthetics", carrier2[originHeader])
}

func TestPropagationDefaults(t *testing.T) {
	t.Setenv(headerPropagationStyleExtract, "datadog")
	t.Setenv(headerPropagationStyleInject, "datadog")
	assert := assert.New(t)

	tracer := newTracer()
	defer tracer.Stop()
	root := tracer.StartSpan("web.request")
	root.SetBaggageItem("x", "y")
	root.SetTag(ext.ManualDrop, true)
	ctx := root.Context().(internal.SpanContextV2Adapter)
	headers := http.Header{}

	// inject the spanContext
	carrier := HTTPHeadersCarrier(headers)
	err := tracer.Inject(ctx, carrier)
	assert.Nil(err)

	rctx := root.Context()
	tid := strconv.FormatUint(rctx.TraceID(), 10)
	pid := strconv.FormatUint(rctx.SpanID(), 10)

	assert.Equal(headers.Get(DefaultTraceIDHeader), tid)
	assert.Equal(headers.Get(DefaultParentIDHeader), pid)
	assert.Equal(headers.Get(DefaultBaggageHeaderPrefix+"x"), "y")
	assert.Equal(headers.Get(DefaultPriorityHeader), "-1")

	// retrieve the spanContext
	propagated, err := tracer.Extract(carrier)
	assert.Nil(err)
	pctx := propagated.(internal.SpanContextV2Adapter)

	// compare if there is a Context match
	assert.Equal(ctx.TraceID(), pctx.TraceID())
	assert.Equal(ctx.SpanID(), pctx.SpanID())

	pctx.ForeachBaggageItem(func(k, v string) bool {
		assert.Equal(root.BaggageItem(k), v)
		return true
	})
	pr, ok := ctx.Ctx.SamplingPriority()
	assert.True(ok)
	assert.Equal(float64(pr), -1.)

	// ensure a child can be created
	child := tracer.StartSpan("db.query", ChildOf(propagated)).(internal.SpanV2Adapter)
	ctx = child.Context().(internal.SpanContextV2Adapter)

	assert.NotEqual(uint64(0), child.Context().TraceID())
	assert.NotEqual(uint64(0), child.Context().SpanID())
	assert.Equal(rctx.SpanID(), child.Context().TraceID())
	assert.Equal(rctx.TraceID(), child.Context().TraceID())
	pr, ok = ctx.Ctx.SamplingPriority()
	assert.True(ok)
	assert.Equal(float64(pr), -1.)
}

func TestTracerBaggageImmutability(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	defer tracer.Stop()
	root := tracer.StartSpan("web.request")
	root.SetBaggageItem("key", "value")
	child := tracer.StartSpan("db.query", ChildOf(root.Context()))
	child.SetBaggageItem("key", "changed!")
	parentContext := root.Context()
	childContext := child.Context()
	parentContext.ForeachBaggageItem(func(k, v string) bool {
		if k != "key" {
			return true
		}
		assert.Equal("value", v)
		return false
	})
	childContext.ForeachBaggageItem(func(k, v string) bool {
		if k != "key" {
			return true
		}
		assert.Equal("changed!", v)
		return false
	})
}

func TestTracerInjectConcurrency(t *testing.T) {
	tracer, stop := startTestTracer(t)
	defer stop()
	span, _ := StartSpanFromContext(context.Background(), "main")
	defer span.Finish()

	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		i := i
		go func(val int) {
			defer wg.Done()
			span.SetBaggageItem("val", fmt.Sprintf("%d", val))

			traceContext := map[string]string{}
			_ = tracer.Inject(span.Context(), TextMapCarrier(traceContext))
		}(i)
	}

	wg.Wait()
}

func TestTracerSpanTags(t *testing.T) {
	tracer := newTracer()
	defer tracer.Stop()
	tag := Tag("key", "value")
	span := tracer.StartSpan("web.request", tag)
	assert := assert.New(t)
	sm := span.(internal.SpanV2Adapter).Span.AsMap()
	assert.Equal("value", sm["key"])
}

func TestTracerSpanGlobalTags(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(WithGlobalTag("key", "value"))
	defer tracer.Stop()
	s := tracer.StartSpan("web.request")
	assert.Equal("value", s.(internal.SpanV2Adapter).Span.AsMap()["key"])
	child := tracer.StartSpan("db.query", ChildOf(s.Context()))
	assert.Equal("value", child.(internal.SpanV2Adapter).Span.AsMap()["key"])
}

func TestTracerSpanServiceMappings(t *testing.T) {
	assert := assert.New(t)

	t.Run("WithServiceMapping", func(t *testing.T) {
		tracer := newTracer(WithServiceName("initial_service"), WithServiceMapping("initial_service", "new_service"))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request")
		sm := s.(internal.SpanV2Adapter).Span.AsMap()
		assert.Equal("new_service", sm[ext.ServiceName])
	})

	t.Run("child", func(t *testing.T) {
		tracer := newTracer(WithServiceMapping("initial_service", "new_service"))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request", ServiceName("initial_service"))
		child := tracer.StartSpan("db.query", ChildOf(s.Context()))
		sm := child.(internal.SpanV2Adapter).Span.AsMap()
		assert.Equal("new_service", sm[ext.ServiceName])
	})

	t.Run("StartSpanOption", func(t *testing.T) {
		tracer := newTracer(WithServiceMapping("initial_service", "new_service"))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request", ServiceName("initial_service"))
		sm := s.(internal.SpanV2Adapter).Span.AsMap()
		assert.Equal("new_service", sm[ext.ServiceName])
	})

	t.Run("tag", func(t *testing.T) {
		tracer := newTracer(WithServiceMapping("initial_service", "new_service"))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request", Tag("service.name", "initial_service"))
		sm := s.(internal.SpanV2Adapter).Span.AsMap()
		assert.Equal("new_service", sm[ext.ServiceName])
	})

	t.Run("globalTags", func(t *testing.T) {
		tracer := newTracer(WithGlobalTag("service.name", "initial_service"), WithServiceMapping("initial_service", "new_service"))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request")
		sm := s.(internal.SpanV2Adapter).Span.AsMap()
		assert.Equal("new_service", sm[ext.ServiceName])
	})
}

func TestTracerNoDebugStack(t *testing.T) {
	assert := assert.New(t)

	t.Run("Finish", func(t *testing.T) {
		tracer := newTracer(WithDebugStack(false))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request")
		err := errors.New("test error")
		s.Finish(WithError(err))
		sm := s.(internal.SpanV2Adapter).Span.AsMap()
		assert.Empty(sm[ext.ErrorStack])
	})

	t.Run("SetTag", func(t *testing.T) {
		tracer := newTracer(WithDebugStack(false))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request")
		err := errors.New("error value with no trace")
		s.SetTag(ext.Error, err)
		sm := s.(internal.SpanV2Adapter).Span.AsMap()
		assert.Empty(sm[ext.ErrorStack])
	})
}

// TestTracerTraceMaxSize tests a bug that was encountered in environments
// creating a large volume of spans that reached the trace cap value (traceMaxSize).
// The bug was that once the cap is reached, no more spans are pushed onto
// the buffer, yet they are part of the same trace. The trace is considered
// completed and flushed when the number of finished spans == number of spans
// in buffer. When reaching the cap, this condition might become true too
// early, and some spans in the buffer might still not be finished when flushing.
// Changing these spans at the moment of flush would (and did) cause a race
// condition.
func TestTracerTraceMaxSize(t *testing.T) {
	_, stop := startTestTracer(t)
	defer stop()

	otss, otms := traceStartSize, traceMaxSize
	traceStartSize, traceMaxSize = 3, 3
	defer func() {
		traceStartSize, traceMaxSize = otss, otms
	}()

	spans := make([]ddtrace.Span, 5)
	spans[0] = StartSpan("span0")
	spans[1] = StartSpan("span1", ChildOf(spans[0].Context()))
	spans[2] = StartSpan("span2", ChildOf(spans[0].Context()))
	spans[3] = StartSpan("span3", ChildOf(spans[0].Context()))
	spans[4] = StartSpan("span4", ChildOf(spans[0].Context()))

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 5000; i++ {
			spans[1].SetTag(strconv.Itoa(i), 1)
			spans[2].SetTag(strconv.Itoa(i), 1)
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		spans[0].Finish()
		spans[3].Finish()
		spans[4].Finish()
	}()

	wg.Wait()
}

func TestTracerReportsHostname(t *testing.T) {
	const hostname = "hostname-test"

	testReportHostnameDisabled := func(t *testing.T, name string, withComputeStats bool) {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DD_TRACE_COMPUTE_STATS", fmt.Sprintf("%t", withComputeStats))
			tracer, stop := startTestTracer(t)
			defer stop()

			root := tracer.StartSpan("root")
			child := tracer.StartSpan("child", ChildOf(root.Context()))
			child.Finish()
			root.Finish()

			assert := assert.New(t)

			rm := root.(internal.SpanV2Adapter).Span.AsMap()
			_, ok := rm[keyHostname]
			assert.False(ok)
			cm := child.(internal.SpanV2Adapter).Span.AsMap()
			_, ok = cm[keyHostname]
			assert.False(ok)
		})
	}
	testReportHostnameDisabled(t, "DD_TRACE_REPORT_HOSTNAME/unset,DD_TRACE_COMPUTE_STATS/true", true)
	testReportHostnameDisabled(t, "DD_TRACE_REPORT_HOSTNAME/unset,DD_TRACE_COMPUTE_STATS/false", false)

	t.Run("WithHostname", func(t *testing.T) {
		tracer, stop := startTestTracer(t, WithHostname(hostname))
		defer stop()

		root := tracer.StartSpan("root")
		child := tracer.StartSpan("child", ChildOf(root.Context()))
		child.Finish()
		root.Finish()

		assert := assert.New(t)

		rm := root.(internal.SpanV2Adapter).Span.AsMap()
		got, ok := rm[keyHostname]
		assert.True(ok)
		assert.Equal(got, hostname)

		cm := child.(internal.SpanV2Adapter).Span.AsMap()
		got, ok = cm[keyHostname]
		assert.True(ok)
		assert.Equal(got, hostname)
	})

	t.Run("DD_TRACE_SOURCE_HOSTNAME/set", func(t *testing.T) {
		t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "hostname-test")

		tracer, stop := startTestTracer(t)
		defer stop()

		root := tracer.StartSpan("root")
		child := tracer.StartSpan("child", ChildOf(root.Context()))
		child.Finish()
		root.Finish()

		assert := assert.New(t)

		rm := root.(internal.SpanV2Adapter).Span.AsMap()
		got, ok := rm[keyHostname]
		assert.True(ok)
		assert.Equal(got, hostname)

		cm := child.(internal.SpanV2Adapter).Span.AsMap()
		got, ok = cm[keyHostname]
		assert.True(ok)
		assert.Equal(got, hostname)
	})

	t.Run("DD_TRACE_SOURCE_HOSTNAME/unset", func(t *testing.T) {
		tracer, stop := startTestTracer(t)
		defer stop()

		root := tracer.StartSpan("root")
		child := tracer.StartSpan("child", ChildOf(root.Context()))
		child.Finish()
		root.Finish()

		assert := assert.New(t)

		rm := root.(internal.SpanV2Adapter).Span.AsMap()
		_, ok := rm[keyHostname]
		assert.False(ok)
		cm := child.(internal.SpanV2Adapter).Span.AsMap()
		_, ok = cm[keyHostname]
		assert.False(ok)
	})
}

func TestVersion(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		tracer, stop := startTestTracer(t, WithServiceVersion("4.5.6"))
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request")
		spm := sp.(internal.SpanV2Adapter).Span.AsMap()
		v := spm[ext.Version]
		assert.Equal("4.5.6", v)
	})
	t.Run("service", func(t *testing.T) {
		tracer, stop := startTestTracer(t, WithServiceVersion("4.5.6"),
			WithService("servenv"))
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request", ServiceName("otherservenv"))
		spm := sp.(internal.SpanV2Adapter).Span.AsMap()
		_, ok := spm[ext.Version]
		assert.False(ok)
	})
	t.Run("universal", func(t *testing.T) {
		tracer, stop := startTestTracer(t, WithService("servenv"), WithUniversalVersion("4.5.6"))
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request", ServiceName("otherservenv"))
		spm := sp.(internal.SpanV2Adapter).Span.AsMap()
		v, ok := spm[ext.Version]
		assert.True(ok)
		assert.Equal("4.5.6", v)
	})
	t.Run("service/universal", func(t *testing.T) {
		tracer, stop := startTestTracer(t, WithServiceVersion("4.5.6"),
			WithService("servenv"), WithUniversalVersion("1.2.3"))
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request", ServiceName("otherservenv"))
		spm := sp.(internal.SpanV2Adapter).Span.AsMap()
		v, ok := spm[ext.Version]
		assert.True(ok)
		assert.Equal("1.2.3", v)
	})
	t.Run("universal/service", func(t *testing.T) {
		tracer, stop := startTestTracer(t, WithUniversalVersion("1.2.3"),
			WithServiceVersion("4.5.6"), WithService("servenv"))
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request", ServiceName("otherservenv"))
		spm := sp.(internal.SpanV2Adapter).Span.AsMap()
		_, ok := spm[ext.Version]
		assert.False(ok)
	})
}

func TestEnvironment(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		tracer, stop := startTestTracer(t, WithEnv("test"))
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request")
		spm := sp.(internal.SpanV2Adapter).Span.AsMap()
		v := spm[ext.Environment]
		assert.Equal("test", v)
	})

	t.Run("unset", func(t *testing.T) {
		tracer, stop := startTestTracer(t)
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request")
		spm := sp.(internal.SpanV2Adapter).Span.AsMap()
		_, ok := spm[ext.Environment]
		assert.False(ok)
	})
}

func TestMockSpanSetUser(t *testing.T) {
	assert := assert.New(t)
	mt := mocktracer.Start()
	defer mt.Stop()

	sp := StartSpan("http.request")
	SetUser(sp, "testuser")
	sp.Finish()

	r := mt.FinishedSpans()
	assert.Len(r, 1)
	assert.Equal("testuser", r[0].Tag("usr.id"))
}

// BenchmarkConcurrentTracing tests the performance of spawning a lot of
// goroutines where each one creates a trace with a parent and a child.
func BenchmarkConcurrentTracing(b *testing.B) {
	tracer, stop := startTestTracer(b, WithLogger(log.DiscardLogger{}), WithSampler(NewRateSampler(0)))
	defer stop()

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		wg := sync.WaitGroup{}
		for i := 0; i < 100; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				parent := tracer.StartSpan("pylons.request", ServiceName("pylons"), ResourceName("/"))
				defer parent.Finish()

				for i := 0; i < 10; i++ {
					tracer.StartSpan("redis.command", ChildOf(parent.Context())).Finish()
				}
			}()
		}
		wg.Wait()
	}
}

// BenchmarkPartialFlushing tests the performance of creating a lot of spans in a single thread
// while partial flushing is enabled.
func BenchmarkPartialFlushing(b *testing.B) {
	b.Run("Enabled", func(b *testing.B) {
		b.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
		b.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "500")
		genBigTraces(b)
	})
	b.Run("Disabled", func(b *testing.B) {
		genBigTraces(b)
	})
}

func genBigTraces(b *testing.B) {
	tracer, stop := startTestTracer(b, WithLogger(log.DiscardLogger{}))
	flush := tracer.(internal.TracerV2Adapter).Tracer.Flush
	defer stop()

	ctx, cancel := context.WithCancel(context.Background())
	wg := sync.WaitGroup{}
	wg.Add(1)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	m := runtime.MemStats{}
	sumHeapUsageMB := float64(0)
	heapCounts := 0
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				runtime.ReadMemStats(&m)
				heapCounts++
				sumHeapUsageMB += float64(m.HeapInuse) / 1_000_000
			}
		}
	}()

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		for i := 0; i < 10; i++ {
			parent := tracer.StartSpan("pylons.request", ResourceName("/"))
			for i := 0; i < 10_000; i++ {
				sp := tracer.StartSpan("redis.command", ChildOf(parent.Context()))
				sp.SetTag("someKey", "some much larger value to create some fun memory usage here")
				sp.Finish()
			}
			parent.Finish()
			go flush()
		}
	}
	b.StopTimer()
	cancel()
	wg.Wait()
	b.ReportMetric(sumHeapUsageMB/float64(heapCounts), "avgHeapInUse(Mb)")
}

// BenchmarkTracerAddSpans tests the performance of creating and finishing a root
// span. It should include the encoding overhead.
func BenchmarkTracerAddSpans(b *testing.B) {
	tracer, stop := startTestTracer(b, WithLogger(log.DiscardLogger{}), WithSampler(NewRateSampler(0)))
	defer stop()

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		span := tracer.StartSpan("pylons.request", ServiceName("pylons"), ResourceName("/"))
		span.Finish()
	}
}

func BenchmarkStartSpan(b *testing.B) {
	tracer, stop := startTestTracer(b, WithLogger(log.DiscardLogger{}), WithSampler(NewRateSampler(0)))
	defer stop()

	root := tracer.StartSpan("pylons.request", ServiceName("pylons"), ResourceName("/"))
	ctx := ContextWithSpan(context.TODO(), root)

	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		s, ok := SpanFromContext(ctx)
		if !ok {
			b.Fatal("no span")
		}
		StartSpan("op", ChildOf(s.Context()))
	}
}

func BenchmarkStartSpanConcurrent(b *testing.B) {
	tracer, stop := startTestTracer(b, WithLogger(log.DiscardLogger{}), WithSampler(NewRateSampler(0)))
	defer stop()

	var wg sync.WaitGroup
	var wgready sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 10; i++ {
		wg.Add(1)
		wgready.Add(1)
		go func() {
			defer wg.Done()
			root := tracer.StartSpan("pylons.request", ServiceName("pylons"), ResourceName("/"))
			ctx := ContextWithSpan(context.TODO(), root)
			wgready.Done()
			<-start
			for n := 0; n < b.N; n++ {
				s, ok := SpanFromContext(ctx)
				if !ok {
					b.Fatal("no span")
				}
				StartSpan("op", ChildOf(s.Context()))
			}
		}()
	}
	wgready.Wait()
	b.ResetTimer()
	close(start)
	wg.Wait()
}

// startTestTracer returns a Tracer with a DummyTransport
func startTestTracer(_ testing.TB, opts ...StartOption) (trc ddtrace.Tracer, stop func()) {
	o := append([]StartOption{
		v2.WithTestDefaults(nil),
	}, opts...)
	tracer := newTracer(o...)
	return tracer, func() {
		internal.SetGlobalTracer(internal.NoopTracerV2)
		internal.SetServiceName("")
		tracer.Stop()
	}
}

// BenchmarkTracerStackFrames tests the performance of taking stack trace.
func BenchmarkTracerStackFrames(b *testing.B) {
	tracer, stop := startTestTracer(b, WithSampler(NewRateSampler(0)))
	defer stop()

	for n := 0; n < b.N; n++ {
		span := tracer.StartSpan("test")
		span.Finish(StackFrames(64, 0))
	}
}

func BenchmarkSingleSpanRetention(b *testing.B) {
	b.Run("no-rules", func(b *testing.B) {
		tracer, stop := startTestTracer(b, v2.WithService("test_service"), v2.WithSampler(v2.NewRateSampler(0)))
		defer stop()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			span := tracer.StartSpan("name_1")
			for i := 0; i < 100; i++ {
				child := tracer.StartSpan("name_2", ChildOf(span.Context()))
				child.Finish()
			}
			span.Finish()
		}
	})

	b.Run("with-rules/match-half", func(b *testing.B) {
		b.Setenv("DD_SPAN_SAMPLING_RULES", `[{"service": "test_*","name":"*_1", "sample_rate": 1.0, "max_per_second": 15.0}]`)
		tracer, stop := startTestTracer(b, v2.WithService("test_service"), v2.WithSampler(v2.NewRateSampler(0)))
		defer stop()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			span := tracer.StartSpan("name_1")
			for i := 0; i < 50; i++ {
				child := tracer.StartSpan("name_2", ChildOf(span.Context()))
				child.Finish()
			}
			for i := 0; i < 50; i++ {
				child := tracer.StartSpan("name", ChildOf(span.Context()))
				child.Finish()
			}
			span.Finish()
		}
	})

	b.Run("with-rules/match-all", func(b *testing.B) {
		b.Setenv("DD_SPAN_SAMPLING_RULES", `[{"service": "test_*","name":"*_1", "sample_rate": 1.0, "max_per_second": 15.0}]`)
		tracer, stop := startTestTracer(b, v2.WithService("test_service"), v2.WithSampler(v2.NewRateSampler(0)))
		defer stop()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			span := tracer.StartSpan("name_1")
			for i := 0; i < 100; i++ {
				child := tracer.StartSpan("name_2", ChildOf(span.Context()))
				child.Finish()
			}
			span.Finish()
		}
	})
}

func TestExecutionTraceSpanTagged(t *testing.T) {
	if rt.IsEnabled() {
		t.Skip("runtime execution tracing is already enabled")
	}

	if err := rt.Start(io.Discard); err != nil {
		t.Fatal(err)
	}
	// Ensure we unconditionally stop tracing. It's safe to call this
	// multiple times.
	defer rt.Stop()

	tracer, stop := startTestTracer(t)
	defer stop()

	tracedSpan := tracer.StartSpan("traced")
	tracedSpan.Finish()

	partialSpan := tracer.StartSpan("partial")

	rt.Stop()

	partialSpan.Finish()

	untracedSpan := tracer.StartSpan("untraced")
	untracedSpan.Finish()

	tsm := tracedSpan.(internal.SpanV2Adapter).Span.AsMap()
	psm := partialSpan.(internal.SpanV2Adapter).Span.AsMap()
	usm := untracedSpan.(internal.SpanV2Adapter).Span.AsMap()
	assert.Equal(t, tsm["go_execution_traced"], "yes")
	assert.Equal(t, psm["go_execution_traced"], "partial")
	assert.NotContains(t, usm, "go_execution_traced")
}

// newTracer creates a new no-op tracer for testing.
// NOTE: This function does set the global tracer, which is required for
// most finish span/flushing operations to work as expected.
func newTracer(opts ...StartOption) ddtrace.Tracer {
	Start(opts...)
	return internal.GetGlobalTracer()
}

func TestNoopTracerStartSpan(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("Failed to create pipe: %v", err)
	}

	undo := log.UseLogger(customLogger{l: llog.New(w, "", llog.LstdFlags)})
	defer undo()

	log.SetLevel(log.LevelDebug)
	defer log.SetLevel(log.LevelWarn)

	StartSpan("abcd")

	w.Close()
	var buf bytes.Buffer
	buf.ReadFrom(r)

	log := buf.String()
	expected := "Tracer must be started before starting a span"
	assert.Contains(t, log, expected)
}

type customLogger struct{ l *llog.Logger }

func (c customLogger) Log(msg string) {
	c.l.Print(msg)
}
