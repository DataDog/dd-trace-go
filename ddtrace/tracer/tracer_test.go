// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	rt "runtime/trace"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	v2 "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	maininternal "gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"
)

func (t *tracer) newEnvSpan(service, env string) *span {
	return t.StartSpan("test.op", SpanType("test"), ServiceName(service), ResourceName("/"), Tag(ext.Environment, env)).(*span)
}

func (t *tracer) newChildSpan(name string, parent *span) *span {
	if parent == nil {
		return t.StartSpan(name).(*span)
	}
	return t.StartSpan(name, ChildOf(parent.Context())).(*span)
}

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
)

func TestMain(m *testing.M) {
	if maininternal.BoolEnv("DD_APPSEC_ENABLED", false) {
		// things are slower with AppSec; double wait times
		timeMultiplicator = time.Duration(2)
	}
	_, integration = os.LookupEnv("INTEGRATION")
	os.Exit(m.Run())
}

func (t *tracer) awaitPayload(tst *testing.T, n int) {
	timeout := time.After(time.Second * timeMultiplicator)
loop:
	for {
		select {
		case <-timeout:
			tst.Fatalf("timed out waiting for payload to contain %d", n)
		default:
			if t.traceWriter.(*agentTraceWriter).payload.itemCount() == n {
				break loop
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

// setLogWriter sets the io.Writer that any new logTraceWriter will write to and returns a function
// which will return the io.Writer to its original value.
func setLogWriter(w io.Writer) func() {
	tmp := logWriter
	logWriter = w
	return func() { logWriter = tmp }
}

// TestTracerCleanStop does frenetic testing in a scenario where the tracer is started
// and stopped in parallel with spans being created.
func TestTracerCleanStop(t *testing.T) {
	if testing.Short() {
		return
	}
	if runtime.GOOS == "windows" {
		t.Skip("This test causes windows CI to fail due to out-of-memory issues")
	}
	// avoid CI timeouts due to AppSec and telemetry slowing down this test
	t.Setenv("DD_APPSEC_ENABLED", "")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	t.Setenv("DD_TRACE_STARTUP_LOGS", "0")

	var wg sync.WaitGroup

	n := 5000

	wg.Add(3)
	for j := 0; j < 3; j++ {
		go func() {
			defer wg.Done()
			for i := 0; i < n; i++ {
				span := StartSpan("test.span")
				child := StartSpan("child.span", ChildOf(span.Context()))
				time.Sleep(time.Millisecond)
				child.Finish()
				time.Sleep(time.Millisecond)
				span.Finish()
			}
		}()
	}

	defer setLogWriter(io.Discard)()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			// Lambda mode is used to avoid the startup cost associated with agent discovery.
			Start(v2.WithTestDefaults(nil), WithLambdaMode(true))
			time.Sleep(time.Millisecond)
			Start(v2.WithTestDefaults(nil), WithLambdaMode(true), WithSampler(NewRateSampler(0.99)))
			Start(v2.WithTestDefaults(nil), WithLambdaMode(true), WithSampler(NewRateSampler(0.99)))
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < n; i++ {
			Stop()
			Stop()
			Stop()
			time.Sleep(time.Millisecond)
			Stop()
			Stop()
			Stop()
		}
	}()

	wg.Wait()
}

func TestTracerStart(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		Start()
		defer Stop()
		trc := internal.GetGlobalTracer().(internal.TracerV2Adapter).Tracer
		if _, ok := trc.(v2.Tracer); !ok {
			t.Fail()
		}
	})

	t.Run("tracing_not_enabled", func(t *testing.T) {
		t.Setenv("DD_TRACE_ENABLED", "false")
		Start()
		defer Stop()
		trc := internal.GetGlobalTracer().(internal.TracerV2Adapter).Tracer
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
		span := tracer.StartSpan("web.request", Tag(ext.SamplingPriority, ext.PriorityUserKeep)).(internal.SpanV2Adapter).Span
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
		tp.Ignore("appsec: ", telemetry.LogPrefix)
		tracer := newTracer(WithRuntimeMetrics(), WithLogger(tp), WithDebugMode(true))
		defer tracer.Stop()
		assert.Contains(t, tp.Logs()[0], "DEBUG: Runtime metrics enabled")
	})

	t.Run("env", func(t *testing.T) {
		t.Setenv("DD_RUNTIME_METRICS_ENABLED", "true")
		tp := new(log.RecordLogger)
		tp.Ignore("appsec: ", telemetry.LogPrefix)
		tracer := newTracer(WithLogger(tp), WithDebugMode(true))
		defer tracer.Stop()
		assert.Contains(t, tp.Logs()[0], "DEBUG: Runtime metrics enabled")
	})

	t.Run("overrideEnv", func(t *testing.T) {
		t.Setenv("DD_RUNTIME_METRICS_ENABLED", "false")
		tp := new(log.RecordLogger)
		tp.Ignore("appsec: ", telemetry.LogPrefix)
		tracer := newTracer(WithRuntimeMetrics(), WithLogger(tp), WithDebugMode(true))
		defer tracer.Stop()
		assert.Contains(t, tp.Logs()[0], "DEBUG: Runtime metrics enabled")
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
	internal.SetGlobalTracer(tracer)
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

func TestTracerBaggagePropagation(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	defer tracer.Stop()
	root := tracer.StartSpan("web.request").(*span)
	root.SetBaggageItem("key", "value")
	child := tracer.StartSpan("db.query", ChildOf(root.Context())).(*span)
	context := child.Context().(*spanContext)

	assert.Equal("value", context.baggage["key"])
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
	assert.Equal("synthetics", child.(*span).Meta[keyOrigin])

	// secondary child doesn't
	child2 := tracer.StartSpan("child2", ChildOf(child.Context()))
	assert.Empty(child2.(*span).Meta[keyOrigin])

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
	root.SetTag(ext.SamplingPriority, -1)
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

func TestTracerSamplingPriorityPropagation(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	defer tracer.Stop()
	root := tracer.StartSpan("web.request", Tag(ext.SamplingPriority, 2)).(*span)
	child := tracer.StartSpan("db.query", ChildOf(root.Context())).(*span)
	assert.EqualValues(2, root.Metrics[keySamplingPriority])
	assert.Equal("-4", root.context.trace.propagatingTags[keyDecisionMaker])
	assert.EqualValues(2, child.Metrics[keySamplingPriority])
	assert.EqualValues(2., *root.context.trace.priority)
	assert.EqualValues(2., *child.context.trace.priority)
}

func TestTracerSamplingPriorityEmptySpanCtx(t *testing.T) {
	assert := assert.New(t)
	tracer, _, _, stop := startTestTracer(t)
	defer stop()
	root := newBasicSpan("web.request")
	spanCtx := &spanContext{
		traceID: traceIDFrom64Bits(root.context.TraceID()),
		spanID:  root.context.SpanID(),
		trace:   &trace{},
	}
	child := tracer.StartSpan("db.query", ChildOf(spanCtx)).(*span)
	assert.EqualValues(1, child.Metrics[keySamplingPriority])
	assert.Equal("-1", child.context.trace.propagatingTags[keyDecisionMaker])
}

func TestTracerDDUpstreamServicesManualKeep(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	defer tracer.Stop()
	root := newBasicSpan("web.request")
	spanCtx := &spanContext{
		traceID: traceIDFrom64Bits(root.context.TraceID()),
		spanID:  root.context.SpanID(),
		trace:   &trace{},
	}
	child := tracer.StartSpan("db.query", ChildOf(spanCtx)).(*span)
	grandChild := tracer.StartSpan("db.query", ChildOf(child.Context())).(*span)
	grandChild.SetTag(ext.ManualDrop, true)
	grandChild.SetTag(ext.ManualKeep, true)
	assert.Equal("-4", grandChild.context.trace.propagatingTags[keyDecisionMaker])
}

func TestTracerBaggageImmutability(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer()
	defer tracer.Stop()
	root := tracer.StartSpan("web.request").(*span)
	root.SetBaggageItem("key", "value")
	child := tracer.StartSpan("db.query", ChildOf(root.Context())).(*span)
	child.SetBaggageItem("key", "changed!")
	parentContext := root.Context().(*spanContext)
	childContext := child.Context().(*spanContext)
	assert.Equal("value", parentContext.baggage["key"])
	assert.Equal("changed!", childContext.baggage["key"])
}

func TestTracerInjectConcurrency(t *testing.T) {
	tracer, _, _, stop := startTestTracer(t)
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
	span := tracer.StartSpan("web.request", tag).(*span)
	assert := assert.New(t)
	assert.Equal("value", span.Meta["key"])
}

func TestTracerSpanGlobalTags(t *testing.T) {
	assert := assert.New(t)
	tracer := newTracer(WithGlobalTag("key", "value"))
	defer tracer.Stop()
	s := tracer.StartSpan("web.request").(*span)
	assert.Equal("value", s.Meta["key"])
	child := tracer.StartSpan("db.query", ChildOf(s.Context())).(*span)
	assert.Equal("value", child.Meta["key"])
}

func TestTracerSpanServiceMappings(t *testing.T) {
	assert := assert.New(t)

	t.Run("WithServiceMapping", func(t *testing.T) {
		tracer := newTracer(WithServiceName("initial_service"), WithServiceMapping("initial_service", "new_service"))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request").(*span)
		assert.Equal("new_service", s.Service)
	})

	t.Run("child", func(t *testing.T) {
		tracer := newTracer(WithServiceMapping("initial_service", "new_service"))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request", ServiceName("initial_service")).(*span)
		child := tracer.StartSpan("db.query", ChildOf(s.Context())).(*span)
		assert.Equal("new_service", child.Service)
	})

	t.Run("StartSpanOption", func(t *testing.T) {
		tracer := newTracer(WithServiceMapping("initial_service", "new_service"))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request", ServiceName("initial_service")).(*span)
		assert.Equal("new_service", s.Service)
	})

	t.Run("tag", func(t *testing.T) {
		tracer := newTracer(WithServiceMapping("initial_service", "new_service"))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request", Tag("service.name", "initial_service")).(*span)
		assert.Equal("new_service", s.Service)
	})

	t.Run("globalTags", func(t *testing.T) {
		tracer := newTracer(WithGlobalTag("service.name", "initial_service"), WithServiceMapping("initial_service", "new_service"))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request").(*span)
		assert.Equal("new_service", s.Service)
	})
}

func TestTracerNoDebugStack(t *testing.T) {
	assert := assert.New(t)

	t.Run("Finish", func(t *testing.T) {
		tracer := newTracer(WithDebugStack(false))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request").(*span)
		err := errors.New("test error")
		s.Finish(WithError(err))
		assert.Empty(s.Meta[ext.ErrorStack])
	})

	t.Run("SetTag", func(t *testing.T) {
		tracer := newTracer(WithDebugStack(false))
		defer tracer.Stop()
		s := tracer.StartSpan("web.request").(*span)
		err := errors.New("error value with no trace")
		s.SetTag(ext.Error, err)
		assert.Empty(s.Meta[ext.ErrorStack])
	})
}

// newDefaultTransport return a default transport for this tracing client
func newDefaultTransport() transport {
	return newHTTPTransport(defaultURL, defaultClient)
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
	_, _, _, stop := startTestTracer(t)
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

func TestTracerFlush(t *testing.T) {
	// https://github.com/DataDog/dd-trace-go/issues/377
	tracer, transport, flush, stop := startTestTracer(t)
	defer stop()

	t.Run("direct", func(t *testing.T) {
		defer transport.Reset()
		assert := assert.New(t)
		root := tracer.StartSpan("root")
		tracer.StartSpan("child.direct", ChildOf(root.Context())).Finish()
		root.Finish()
		flush(1)

		list := transport.Traces()
		assert.Len(list, 1)
		assert.Len(list[0], 2)
		assert.Equal("child.direct", list[0][1].Name)
	})

	t.Run("extracted", func(t *testing.T) {
		defer transport.Reset()
		assert := assert.New(t)
		root := tracer.StartSpan("root")
		h := HTTPHeadersCarrier(http.Header{})
		if err := tracer.Inject(root.Context(), h); err != nil {
			t.Fatal(err)
		}
		sctx, err := tracer.Extract(h)
		if err != nil {
			t.Fatal(err)
		}
		tracer.StartSpan("child.extracted", ChildOf(sctx)).Finish()
		flush(1)
		list := transport.Traces()
		assert.Len(list, 1)
		assert.Len(list[0], 1)
		assert.Equal("child.extracted", list[0][0].Name)
	})
}

func TestTracerReportsHostname(t *testing.T) {
	const hostname = "hostname-test"

	testReportHostnameDisabled := func(t *testing.T, name string, withComputeStats bool) {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DD_TRACE_COMPUTE_STATS", fmt.Sprintf("%t", withComputeStats))
			tracer, _, _, stop := startTestTracer(t)
			defer stop()

			root := tracer.StartSpan("root").(*span)
			child := tracer.StartSpan("child", ChildOf(root.Context())).(*span)
			child.Finish()
			root.Finish()

			assert := assert.New(t)

			_, ok := root.Meta[keyHostname]
			assert.False(ok)
			_, ok = child.Meta[keyHostname]
			assert.False(ok)
		})
	}
	testReportHostnameDisabled(t, "DD_TRACE_REPORT_HOSTNAME/unset,DD_TRACE_COMPUTE_STATS/true", true)
	testReportHostnameDisabled(t, "DD_TRACE_REPORT_HOSTNAME/unset,DD_TRACE_COMPUTE_STATS/false", false)

	t.Run("WithHostname", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithHostname(hostname))
		defer stop()

		root := tracer.StartSpan("root").(*span)
		child := tracer.StartSpan("child", ChildOf(root.Context())).(*span)
		child.Finish()
		root.Finish()

		assert := assert.New(t)

		got, ok := root.Meta[keyHostname]
		assert.True(ok)
		assert.Equal(got, hostname)

		got, ok = child.Meta[keyHostname]
		assert.True(ok)
		assert.Equal(got, hostname)
	})

	t.Run("DD_TRACE_SOURCE_HOSTNAME/set", func(t *testing.T) {
		t.Setenv("DD_TRACE_SOURCE_HOSTNAME", "hostname-test")

		tracer, _, _, stop := startTestTracer(t)
		defer stop()

		root := tracer.StartSpan("root").(*span)
		child := tracer.StartSpan("child", ChildOf(root.Context())).(*span)
		child.Finish()
		root.Finish()

		assert := assert.New(t)

		got, ok := root.Meta[keyHostname]
		assert.True(ok)
		assert.Equal(got, hostname)

		got, ok = child.Meta[keyHostname]
		assert.True(ok)
		assert.Equal(got, hostname)
	})

	t.Run("DD_TRACE_SOURCE_HOSTNAME/unset", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t)
		defer stop()

		root := tracer.StartSpan("root").(*span)
		child := tracer.StartSpan("child", ChildOf(root.Context())).(*span)
		child.Finish()
		root.Finish()

		assert := assert.New(t)

		_, ok := root.Meta[keyHostname]
		assert.False(ok)
		_, ok = child.Meta[keyHostname]
		assert.False(ok)
	})
}

func TestVersion(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithServiceVersion("4.5.6"))
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request").(*span)
		v := sp.Meta[ext.Version]
		assert.Equal("4.5.6", v)
	})
	t.Run("service", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithServiceVersion("4.5.6"),
			WithService("servenv"))
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request", ServiceName("otherservenv")).(*span)
		_, ok := sp.Meta[ext.Version]
		assert.False(ok)
	})
	t.Run("universal", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithService("servenv"), WithUniversalVersion("4.5.6"))
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request", ServiceName("otherservenv")).(*span)
		v, ok := sp.Meta[ext.Version]
		assert.True(ok)
		assert.Equal("4.5.6", v)
	})
	t.Run("service/universal", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithServiceVersion("4.5.6"),
			WithService("servenv"), WithUniversalVersion("1.2.3"))
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request", ServiceName("otherservenv")).(*span)
		v, ok := sp.Meta[ext.Version]
		assert.True(ok)
		assert.Equal("1.2.3", v)
	})
	t.Run("universal/service", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithUniversalVersion("1.2.3"),
			WithServiceVersion("4.5.6"), WithService("servenv"))
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request", ServiceName("otherservenv")).(*span)
		_, ok := sp.Meta[ext.Version]
		assert.False(ok)
	})
}

func TestEnvironment(t *testing.T) {
	t.Run("normal", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithEnv("test"))
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request").(*span)
		v := sp.Meta[ext.Environment]
		assert.Equal("test", v)
	})

	t.Run("unset", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t)
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request").(*span)
		_, ok := sp.Meta[ext.Environment]
		assert.False(ok)
	})
}

func TestGitMetadata(t *testing.T) {
	maininternal.ResetGitMetadataTags()

	t.Run("git-metadata-from-dd-tags", func(t *testing.T) {
		t.Setenv(maininternal.EnvDDTags, "git.commit.sha:123456789ABCD git.repository_url:github.com/user/repo go_path:somepath")

		tracer, _, _, stop := startTestTracer(t)
		defer stop()
		defer maininternal.ResetGitMetadataTags()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request").(*span)
		sp.context.finish()

		assert.Equal("123456789ABCD", sp.Meta[maininternal.TraceTagCommitSha])
		assert.Equal("github.com/user/repo", sp.Meta[maininternal.TraceTagRepositoryURL])
		assert.Equal("somepath", sp.Meta[maininternal.TraceTagGoPath])
	})

	t.Run("git-metadata-from-dd-tags-with-credentials", func(t *testing.T) {
		t.Setenv(maininternal.EnvDDTags, "git.commit.sha:123456789ABCD git.repository_url:https://user:passwd@github.com/user/repo go_path:somepath")

		tracer, _, _, stop := startTestTracer(t)
		defer stop()
		defer maininternal.ResetGitMetadataTags()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request").(*span)
		sp.context.finish()

		assert.Equal("123456789ABCD", sp.Meta[maininternal.TraceTagCommitSha])
		assert.Equal("https://github.com/user/repo", sp.Meta[maininternal.TraceTagRepositoryURL])
		assert.Equal("somepath", sp.Meta[maininternal.TraceTagGoPath])
	})

	t.Run("git-metadata-from-env", func(t *testing.T) {
		t.Setenv(maininternal.EnvDDTags, "git.commit.sha:123456789ABCD git.repository_url:github.com/user/repo")

		// git metadata env has priority over DD_TAGS
		t.Setenv(maininternal.EnvGitRepositoryURL, "github.com/user/repo_new")
		t.Setenv(maininternal.EnvGitCommitSha, "123456789ABCDE")

		tracer, _, _, stop := startTestTracer(t)
		defer stop()
		defer maininternal.ResetGitMetadataTags()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request").(*span)
		sp.context.finish()

		assert.Equal("123456789ABCDE", sp.Meta[maininternal.TraceTagCommitSha])
		assert.Equal("github.com/user/repo_new", sp.Meta[maininternal.TraceTagRepositoryURL])
	})

	t.Run("git-metadata-from-env-with-credentials", func(t *testing.T) {
		t.Setenv(maininternal.EnvGitRepositoryURL, "https://u:t@github.com/user/repo_new")
		t.Setenv(maininternal.EnvGitCommitSha, "123456789ABCDE")

		tracer, _, _, stop := startTestTracer(t)
		defer stop()
		defer maininternal.ResetGitMetadataTags()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request").(*span)
		sp.context.finish()

		assert.Equal("123456789ABCDE", sp.Meta[maininternal.TraceTagCommitSha])
		assert.Equal("https://github.com/user/repo_new", sp.Meta[maininternal.TraceTagRepositoryURL])
	})

	t.Run("git-metadata-from-env-and-tags", func(t *testing.T) {
		t.Setenv(maininternal.EnvDDTags, "git.commit.sha:123456789ABCD")
		t.Setenv(maininternal.EnvGitRepositoryURL, "github.com/user/repo")

		tracer, _, _, stop := startTestTracer(t)
		defer stop()
		defer maininternal.ResetGitMetadataTags()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request").(*span)
		sp.context.finish()

		assert.Equal("123456789ABCD", sp.Meta[maininternal.TraceTagCommitSha])
		assert.Equal("github.com/user/repo", sp.Meta[maininternal.TraceTagRepositoryURL])
	})

	t.Run("git-metadata-disabled", func(t *testing.T) {
		t.Setenv(maininternal.EnvGitMetadataEnabledFlag, "false")

		t.Setenv(maininternal.EnvDDTags, "git.commit.sha:123456789ABCD git.repository_url:github.com/user/repo")
		t.Setenv(maininternal.EnvGitRepositoryURL, "github.com/user/repo_new")
		t.Setenv(maininternal.EnvGitCommitSha, "123456789ABCDE")

		tracer, _, _, stop := startTestTracer(t)
		defer stop()
		defer maininternal.ResetGitMetadataTags()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request").(*span)
		sp.context.finish()

		assert.Equal("", sp.Meta[maininternal.TraceTagCommitSha])
		assert.Equal("", sp.Meta[maininternal.TraceTagRepositoryURL])
	})
}

// BenchmarkConcurrentTracing tests the performance of spawning a lot of
// goroutines where each one creates a trace with a parent and a child.
func BenchmarkConcurrentTracing(b *testing.B) {
	tracer, _, _, stop := startTestTracer(b, WithLogger(log.DiscardLogger{}), WithSampler(NewRateSampler(0)))
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

// BenchmarkBigTraces tests the performance of creating a lot of spans in a single thread
func BenchmarkBigTraces(b *testing.B) {
	b.Run("Big traces", func(b *testing.B) {
		genBigTraces(b)
	})
}

func genBigTraces(b *testing.B) {
	tracer, transport, flush, stop := startTestTracer(b, WithLogger(log.DiscardLogger{}))
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
			go flush(-1)         // act like a ticker
			go transport.Reset() // pretend we sent any payloads
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
	tracer, _, _, stop := startTestTracer(b, WithLogger(log.DiscardLogger{}), WithSampler(NewRateSampler(0)))
	defer stop()

	for n := 0; n < b.N; n++ {
		span := tracer.StartSpan("pylons.request", ServiceName("pylons"), ResourceName("/"))
		span.Finish()
	}
}

func BenchmarkStartSpan(b *testing.B) {
	tracer, _, _, stop := startTestTracer(b, WithLogger(log.DiscardLogger{}), WithSampler(NewRateSampler(0)))
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

// startTestTracer returns a Tracer with a DummyTransport
func startTestTracer(t testing.TB, opts ...StartOption) (trc ddtrace.Tracer, transport *dummyTransport, flush func(n int), stop func()) {
	transport = newDummyTransport()
	tick := make(chan time.Time)
	o := append([]StartOption{
		v2.WithTestDefaults(nil),
	}, opts...)
	tracer := newTracer(o...)
	flushFunc := func(n int) {
		if n < 0 {
			tick <- time.Now()
			return
		}
		d := time.Second * timeMultiplicator
		expire := time.After(d)
	loop:
		for {
			select {
			case <-expire:
				t.Fatalf("timed out in %s waiting for %d trace(s)", d, n)
			default:
				tick <- time.Now()
				if transport.Len() == n {
					break loop
				}
				time.Sleep(5 * time.Millisecond)
			}
		}
	}
	return tracer, transport, flushFunc, func() {
		tracer.Stop()
		// clear any service name that was set: we want the state to be the same as startup
		globalconfig.SetServiceName("")
	}
}

// Mock Transport with a real Encoder
type dummyTransport struct {
	sync.RWMutex
	traces spanLists
	stats  []*statsPayload
}

func newDummyTransport() *dummyTransport {
	return &dummyTransport{traces: spanLists{}}
}

func (t *dummyTransport) Len() int {
	t.RLock()
	defer t.RUnlock()
	return len(t.traces)
}

func (t *dummyTransport) sendStats(p *statsPayload) error {
	t.Lock()
	t.stats = append(t.stats, p)
	t.Unlock()
	return nil
}

func (t *dummyTransport) Stats() []*statsPayload {
	t.RLock()
	defer t.RUnlock()
	return t.stats
}

func (t *dummyTransport) send(p *payload) (io.ReadCloser, error) {
	traces, err := decode(p)
	if err != nil {
		return nil, err
	}
	t.Lock()
	t.traces = append(t.traces, traces...)
	t.Unlock()
	ok := io.NopCloser(strings.NewReader("OK"))
	return ok, nil
}

func (t *dummyTransport) endpoint() string {
	return "http://localhost:9/v0.4/traces"
}

func decode(p *payload) (spanLists, error) {
	var traces spanLists
	err := msgp.Decode(p, &traces)
	return traces, err
}

func encode(traces [][]*span) (*payload, error) {
	p := newPayload()
	for _, t := range traces {
		if err := p.push(t); err != nil {
			return p, err
		}
	}
	return p, nil
}

func (t *dummyTransport) Reset() {
	t.Lock()
	t.traces = t.traces[:0]
	t.Unlock()
}

func (t *dummyTransport) Traces() spanLists {
	t.Lock()
	defer t.Unlock()

	traces := t.traces
	t.traces = spanLists{}
	return traces
}

// comparePayloadSpans allows comparing two spans which might have been
// read from the msgpack payload. In that case the private fields will
// not be available and the maps (meta & metrics will be nil for lengths
// of 0). This function covers for those cases and correctly compares.
func comparePayloadSpans(t *testing.T, a, b *span) {
	assert.Equal(t, cpspan(a), cpspan(b))
}

func cpspan(s *span) *span {
	if len(s.Metrics) == 0 {
		s.Metrics = nil
	}
	if len(s.Meta) == 0 {
		s.Meta = nil
	}
	return &span{
		Name:     s.Name,
		Service:  s.Service,
		Resource: s.Resource,
		Type:     s.Type,
		Start:    s.Start,
		Duration: s.Duration,
		Meta:     s.Meta,
		Metrics:  s.Metrics,
		SpanID:   s.SpanID,
		TraceID:  s.TraceID,
		ParentID: s.ParentID,
		Error:    s.Error,
	}
}

type testTraceWriter struct {
	mu      sync.RWMutex
	buf     []*span
	flushed []*span
}

func newTestTraceWriter() *testTraceWriter {
	return &testTraceWriter{
		buf:     []*span{},
		flushed: []*span{},
	}
}

func (w *testTraceWriter) add(spans []*span) {
	w.mu.Lock()
	w.buf = append(w.buf, spans...)
	w.mu.Unlock()
}

func (w *testTraceWriter) flush() {
	w.mu.Lock()
	w.flushed = append(w.flushed, w.buf...)
	w.buf = w.buf[:0]
	w.mu.Unlock()
}

func (w *testTraceWriter) stop() {}

func (w *testTraceWriter) reset() {
	w.mu.Lock()
	w.flushed = w.flushed[:0]
	w.buf = w.buf[:0]
	w.mu.Unlock()
}

// Buffered returns the spans buffered by the writer.
func (w *testTraceWriter) Buffered() []*span {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.buf
}

// Flushed returns the spans flushed by the writer.
func (w *testTraceWriter) Flushed() []*span {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.flushed
}

func TestTakeStackTrace(t *testing.T) {
	t.Run("n=12", func(t *testing.T) {
		val := takeStacktrace(12, 0)
		// top frame should be runtime.main or runtime.goexit, in case of tests that's goexit
		assert.Contains(t, val, "runtime.goexit")
		assert.Contains(t, val, "testing.tRunner")
		assert.Contains(t, val, "tracer.TestTakeStackTrace")
	})

	t.Run("n=15,skip=2", func(t *testing.T) {
		val := takeStacktrace(3, 2)
		// top frame should be runtime.main or runtime.goexit, in case of tests that's goexit
		assert.Contains(t, val, "runtime.goexit")
		numFrames := strings.Count(val, "\n\t")
		assert.Equal(t, 1, numFrames)
	})

	t.Run("n=1", func(t *testing.T) {
		val := takeStacktrace(1, 0)
		assert.Contains(t, val, "tracer.TestTakeStackTrace", "should contain this function")
		// each frame consists of two strings separated by \n\t, thus number of frames == number of \n\t
		numFrames := strings.Count(val, "\n\t")
		assert.Equal(t, 1, numFrames)
	})

	t.Run("invalid", func(t *testing.T) {
		assert.Empty(t, takeStacktrace(100, 115))
	})
}

// BenchmarkTracerStackFrames tests the performance of taking stack trace.
func BenchmarkTracerStackFrames(b *testing.B) {
	tracer, _, _, stop := startTestTracer(b, WithSampler(NewRateSampler(0)))
	defer stop()

	for n := 0; n < b.N; n++ {
		span := tracer.StartSpan("test")
		span.Finish(StackFrames(64, 0))
	}
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

	tracer, _, _, stop := startTestTracer(t)
	defer stop()

	tracedSpan := tracer.StartSpan("traced").(*span)
	tracedSpan.Finish()

	partialSpan := tracer.StartSpan("partial").(*span)

	rt.Stop()

	partialSpan.Finish()

	untracedSpan := tracer.StartSpan("untraced").(*span)
	untracedSpan.Finish()

	assert.Equal(t, tracedSpan.Meta["go_execution_traced"], "yes")
	assert.Equal(t, partialSpan.Meta["go_execution_traced"], "partial")
	assert.NotContains(t, untracedSpan.Meta, "go_execution_traced")
}

// newTracer creates a new no-op tracer for testing.
// NOTE: This function does set the global tracer, which is required for
// most finish span/flushing operations to work as expected.
func newTracer(opts ...StartOption) ddtrace.Tracer {
	v2.Start(opts...)
	return internal.GetGlobalTracer()
}
