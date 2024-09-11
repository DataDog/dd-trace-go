// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	rt "runtime/trace"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/internal"
	maininternal "gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/statsdtest"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tinylib/msgp/msgp"
)

func (t *tracer) newEnvSpan(service, env string) *span {
	return t.StartSpan("test.op", SpanType("test"), ServiceName(service), ResourceName("/"), Tag(ext.Environment, env)).(*span)
}

func (t *tracer) newRootSpan(name, service, resource string) *span {
	return t.StartSpan(name, SpanType("test"), ServiceName(service), ResourceName(resource)).(*span)
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
	transport := newDummyTransport()

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
			Start(withTransport(transport), WithLambdaMode(true), withNoopStats())
			time.Sleep(time.Millisecond)
			Start(withTransport(transport), WithLambdaMode(true), WithSampler(NewRateSampler(0.99)), withNoopStats())
			Start(withTransport(transport), WithLambdaMode(true), WithSampler(NewRateSampler(0.99)), withNoopStats())
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
		if _, ok := internal.GetGlobalTracer().(*tracer); !ok {
			t.Fail()
		}
	})

	t.Run("testing", func(t *testing.T) {
		internal.Testing = true
		Start()
		defer Stop()
		if _, ok := internal.GetGlobalTracer().(*tracer); ok {
			t.Fail()
		}
		if _, ok := internal.GetGlobalTracer().(*internal.NoopTracer); !ok {
			t.Fail()
		}
		internal.Testing = false
	})

	t.Run("dd_tracing_not_enabled", func(t *testing.T) {
		t.Setenv("DD_TRACE_ENABLED", "false")
		Start()
		defer Stop()
		if _, ok := internal.GetGlobalTracer().(*tracer); ok {
			t.Fail()
		}
		if _, ok := internal.GetGlobalTracer().(*internal.NoopTracer); !ok {
			t.Fail()
		}
	})

	t.Run("otel_tracing_not_enabled", func(t *testing.T) {
		t.Setenv("OTEL_TRACES_EXPORTER", "none")
		Start()
		defer Stop()
		if _, ok := internal.GetGlobalTracer().(*tracer); ok {
			t.Fail()
		}
		if _, ok := internal.GetGlobalTracer().(*internal.NoopTracer); !ok {
			t.Fail()
		}
	})

	t.Run("deadlock/api", func(t *testing.T) {
		Stop()
		Stop()

		Start()
		Start()
		Start()

		// ensure at least one worker started and handles requests
		internal.GetGlobalTracer().(*tracer).pushChunk(&chunk{spans: []*span{}})

		Stop()
		Stop()
		Stop()
		Stop()
	})

	t.Run("deadlock/direct", func(t *testing.T) {
		tr, _, _, stop := startTestTracer(t)
		defer stop()
		tr.pushChunk(&chunk{spans: []*span{}}) // blocks until worker is started
		select {
		case <-tr.stop:
			t.Fatal("stopped channel should be open")
		default:
			// OK
		}
		tr.Stop()
		select {
		case <-tr.stop:
			// OK
		default:
			t.Fatal("stopped channel should be closed")
		}
		tr.Stop()
		tr.Stop()
	})
}

func TestTracerStartSpan(t *testing.T) {
	t.Run("generic", func(t *testing.T) {
		tracer := newTracer()
		defer tracer.Stop()
		span := tracer.StartSpan("web.request").(*span)
		assert := assert.New(t)
		assert.NotEqual(uint64(0), span.TraceID)
		assert.NotEqual(uint64(0), span.SpanID)
		assert.Equal(uint64(0), span.ParentID)
		assert.Equal("web.request", span.Name)
		assert.Regexp(`tracer\.test(\.exe)?`, span.Service)
		assert.Contains([]float64{
			ext.PriorityAutoReject,
			ext.PriorityAutoKeep,
		}, span.Metrics[keySamplingPriority])
		assert.Equal("-1", span.context.trace.propagatingTags[keyDecisionMaker])
		// A span is not measured unless made so specifically
		_, ok := span.Meta[keyMeasured]
		assert.False(ok)
		assert.Equal(globalconfig.RuntimeID(), span.Meta[ext.RuntimeID])
		assert.NotEqual("", span.Meta[ext.RuntimeID])
	})

	t.Run("priority", func(t *testing.T) {
		tracer := newTracer()
		defer tracer.Stop()
		span := tracer.StartSpan("web.request", Tag(ext.SamplingPriority, ext.PriorityUserKeep)).(*span)
		assert.Equal(t, float64(ext.PriorityUserKeep), span.Metrics[keySamplingPriority])
		assert.Equal(t, "-4", span.context.trace.propagatingTags[keyDecisionMaker])
	})

	t.Run("name", func(t *testing.T) {
		tracer := newTracer()
		defer tracer.Stop()
		span := tracer.StartSpan("/home/user", Tag(ext.SpanName, "db.query")).(*span)
		assert.Equal(t, "db.query", span.Name)
		assert.Equal(t, "/home/user", span.Resource)
	})

	t.Run("measured_top_level", func(t *testing.T) {
		tracer := newTracer()
		defer tracer.Stop()
		span := tracer.StartSpan("/home/user", Measured()).(*span)
		_, ok := span.Metrics[keyMeasured]
		assert.False(t, ok)
		assert.Equal(t, 1.0, span.Metrics[keyTopLevel])
	})

	t.Run("measured_non_top_level", func(t *testing.T) {
		tracer := newTracer()
		defer tracer.Stop()
		parent := tracer.StartSpan("/home/user").(*span)
		child := tracer.StartSpan("home/user", Measured(), ChildOf(parent.context)).(*span)
		assert.Equal(t, 1.0, child.Metrics[keyMeasured])
	})

	t.Run("attribute_schema_is_set_v0", func(t *testing.T) {
		t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v0")
		tracer := newTracer()
		defer tracer.Stop()
		parent := tracer.StartSpan("/home/user").(*span)
		child := tracer.StartSpan("home/user", ChildOf(parent.context)).(*span)
		assert.Contains(t, parent.Metrics, "_dd.trace_span_attribute_schema")
		assert.Equal(t, 0.0, parent.Metrics["_dd.trace_span_attribute_schema"])
		assert.NotContains(t, child.Metrics, "_dd.trace_span_attribute_schema")
	})

	t.Run("attribute_schema_is_set_v1", func(t *testing.T) {
		t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "v1")
		tracer := newTracer()
		defer tracer.Stop()
		parent := tracer.StartSpan("/home/user").(*span)
		child := tracer.StartSpan("home/user", ChildOf(parent.context)).(*span)
		assert.Contains(t, parent.Metrics, "_dd.trace_span_attribute_schema")
		assert.Equal(t, 1.0, parent.Metrics["_dd.trace_span_attribute_schema"])
		assert.NotContains(t, child.Metrics, "_dd.trace_span_attribute_schema")
	})

	t.Run("attribute_schema_is_set_wrong_value", func(t *testing.T) {
		t.Setenv("DD_TRACE_SPAN_ATTRIBUTE_SCHEMA", "bad-version")
		tracer := newTracer()
		defer tracer.Stop()
		parent := tracer.StartSpan("/home/user").(*span)
		child := tracer.StartSpan("home/user", ChildOf(parent.context)).(*span)
		assert.Contains(t, parent.Metrics, "_dd.trace_span_attribute_schema")
		assert.Equal(t, 0.0, parent.Metrics["_dd.trace_span_attribute_schema"])
		assert.NotContains(t, child.Metrics, "_dd.trace_span_attribute_schema")
	})
}

func TestSamplingDecision(t *testing.T) {

	t.Run("sampled", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t)
		defer func() {
			// Must check these after tracer is stopped to avoid flakiness
			assert.Equal(t, uint32(0), tracer.droppedP0Traces)
			assert.Equal(t, uint32(2), tracer.droppedP0Spans)
		}()
		defer stop()
		tracer.prioritySampling.defaultRate = 0
		tracer.config.serviceName = "test_service"
		span := tracer.StartSpan("name_1").(*span)
		child := tracer.StartSpan("name_2", ChildOf(span.context))
		child.Finish()
		span.Finish()
		assert.Equal(t, float64(ext.PriorityAutoReject), span.Metrics[keySamplingPriority])
		assert.Equal(t, "", span.context.trace.propagatingTags[keyDecisionMaker])
		assert.Equal(t, decisionKeep, span.context.trace.samplingDecision)
	})

	t.Run("dropped_sent", func(t *testing.T) {
		// Even if DropP0s is enabled, spans should always be kept unless
		// client-side stats are also enabled.
		tracer, _, _, stop := startTestTracer(t)
		defer func() {
			// Must check these after tracer is stopped to avoid flakiness
			assert.Equal(t, uint32(0), tracer.droppedP0Traces)
			assert.Equal(t, uint32(2), tracer.droppedP0Spans)
		}()
		defer stop()
		tracer.config.agent.DropP0s = true
		tracer.prioritySampling.defaultRate = 0
		tracer.config.serviceName = "test_service"
		span := tracer.StartSpan("name_1").(*span)
		child := tracer.StartSpan("name_2", ChildOf(span.context))
		child.Finish()
		span.Finish()
		assert.Equal(t, float64(ext.PriorityAutoReject), span.Metrics[keySamplingPriority])
		assert.Equal(t, "", span.context.trace.propagatingTags[keyDecisionMaker])
		assert.Equal(t, decisionKeep, span.context.trace.samplingDecision)
	})

	t.Run("dropped_stats", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t)
		defer func() {
			// Must check these after tracer is stopped to avoid flakiness
			assert.Equal(t, uint32(1), tracer.droppedP0Traces)
			assert.Equal(t, uint32(2), tracer.droppedP0Spans)
		}()
		defer stop()
		tracer.config.featureFlags = make(map[string]struct{})
		tracer.config.featureFlags["discovery"] = struct{}{}
		tracer.config.agent.DropP0s = true
		tracer.config.agent.Stats = true
		tracer.prioritySampling.defaultRate = 0
		tracer.config.serviceName = "test_service"
		span := tracer.StartSpan("name_1").(*span)
		child := tracer.StartSpan("name_2", ChildOf(span.context))
		child.Finish()
		span.Finish()
		assert.Equal(t, float64(ext.PriorityAutoReject), span.Metrics[keySamplingPriority])
		assert.Equal(t, "", span.context.trace.propagatingTags[keyDecisionMaker])
		assert.Equal(t, decisionNone, span.context.trace.samplingDecision)
	})

	t.Run("events_sampled", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t)
		defer func() {
			// Must check these after tracer is stopped to avoid flakiness
			assert.Equal(t, uint32(0), tracer.droppedP0Traces)
			assert.Equal(t, uint32(2), tracer.droppedP0Spans)
		}()
		defer stop()
		tracer.config.agent.DropP0s = true
		tracer.prioritySampling.defaultRate = 0
		tracer.config.serviceName = "test_service"
		span := tracer.StartSpan("name_1").(*span)
		child := tracer.StartSpan("name_2", ChildOf(span.context))
		child.SetTag(ext.EventSampleRate, 1)
		child.Finish()
		span.Finish()
		assert.Equal(t, float64(ext.PriorityAutoReject), span.Metrics[keySamplingPriority])
		assert.Equal(t, "", span.context.trace.tags[keyDecisionMaker])
		assert.Equal(t, decisionKeep, span.context.trace.samplingDecision)
	})

	t.Run("client_dropped", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t)
		defer func() {
			// Must check these after tracer is stopped to avoid flakiness
			assert.Equal(t, uint32(1), tracer.droppedP0Traces)
			assert.Equal(t, uint32(2), tracer.droppedP0Spans)
		}()
		defer stop()
		tracer.config.sampler = NewRateSampler(0)
		tracer.prioritySampling.defaultRate = 0
		tracer.config.serviceName = "test_service"
		span := tracer.StartSpan("name_1").(*span)
		child := tracer.StartSpan("name_2", ChildOf(span.context))
		child.SetTag(ext.EventSampleRate, 1)
		p, ok := span.context.SamplingPriority()
		require.True(t, ok)
		assert.Equal(t, ext.PriorityAutoReject, p)
		child.Finish()
		span.Finish()
		assert.Equal(t, float64(ext.PriorityAutoReject), span.Metrics[keySamplingPriority])
		// this trace won't be sent to the agent,
		// therefore not necessary to populate keyDecisionMaker
		assert.Equal(t, "", span.context.trace.propagatingTags[keyDecisionMaker])
		assert.Equal(t, decisionDrop, span.context.trace.samplingDecision)
	})

	t.Run("client_dropped_with_single_spans:stats_enabled", func(t *testing.T) {
		t.Setenv("DD_SPAN_SAMPLING_RULES", `[{"service": "test_*","name":"*_1", "sample_rate": 1.0, "max_per_second": 15.0}]`)
		// Stats are enabled, rules are available. Trace sample rate equals 0.
		// Span sample rate equals 1. The trace should be dropped. One single span is extracted.
		tracer, _, _, stop := startTestTracer(t)
		defer func() {
			// Must check these after tracer is stopped to avoid flakiness
			assert.Equal(t, uint32(0), tracer.droppedP0Traces)
			assert.Equal(t, uint32(1), tracer.droppedP0Spans)
		}()
		defer stop()
		tracer.config.agent.DropP0s = true
		tracer.config.featureFlags = make(map[string]struct{})
		tracer.config.featureFlags["discovery"] = struct{}{}
		tracer.config.sampler = NewRateSampler(0)
		tracer.prioritySampling.defaultRate = 0
		tracer.config.serviceName = "test_service"
		parent := tracer.StartSpan("name_1").(*span)
		child := tracer.StartSpan("name_2", ChildOf(parent.context)).(*span)
		child.Finish()
		parent.Finish()
		tracer.Stop()
		assert.Equal(t, float64(ext.PriorityAutoReject), parent.Metrics[keySamplingPriority])
		assert.Equal(t, decisionDrop, parent.context.trace.samplingDecision)
		assert.Equal(t, 8.0, parent.Metrics[keySpanSamplingMechanism])
		assert.Equal(t, 1.0, parent.Metrics[keySingleSpanSamplingRuleRate])
		assert.Equal(t, 15.0, parent.Metrics[keySingleSpanSamplingMPS])
	})

	t.Run("client_dropped_with_single_spans:stats_disabled", func(t *testing.T) {
		t.Setenv("DD_SPAN_SAMPLING_RULES", `[{"service": "test_*","name":"*_1", "sample_rate": 1.0, "max_per_second": 15.0}]`)
		// Stats are disabled, rules are available. Trace sample rate equals 0.
		// Span sample rate equals 1. The trace should be dropped. One span has single span tags set.
		tracer, _, _, stop := startTestTracer(t)
		defer func() {
			// Must check these after tracer is stopped to avoid flakiness
			assert.Equal(t, uint32(0), tracer.droppedP0Traces)
			assert.Equal(t, uint32(1), tracer.droppedP0Spans)
		}()
		defer stop()
		tracer.config.featureFlags = make(map[string]struct{})
		tracer.config.sampler = NewRateSampler(0)
		tracer.prioritySampling.defaultRate = 0
		tracer.config.serviceName = "test_service"
		parent := tracer.StartSpan("name_1").(*span)
		child := tracer.StartSpan("name_2", ChildOf(parent.context)).(*span)
		child.Finish()
		parent.Finish()
		tracer.Stop()
		assert.Equal(t, float64(ext.PriorityAutoReject), parent.Metrics[keySamplingPriority])
		assert.Equal(t, decisionDrop, parent.context.trace.samplingDecision)
		assert.Equal(t, 8.0, parent.Metrics[keySpanSamplingMechanism])
		assert.Equal(t, 1.0, parent.Metrics[keySingleSpanSamplingRuleRate])
		assert.Equal(t, 15.0, parent.Metrics[keySingleSpanSamplingMPS])
	})

	t.Run("client_dropped_with_single_span_rules", func(t *testing.T) {
		t.Setenv("DD_SPAN_SAMPLING_RULES", `[{"service": "match","name":"nothing", "sample_rate": 1.0, "max_per_second": 15.0}]`)
		// Rules are available, but match nothing. Trace sample rate equals 0.
		// The trace should be dropped. No single spans extracted.
		tracer, _, _, stop := startTestTracer(t)
		defer func() {
			// Must check these after tracer is stopped to avoid flakiness
			assert.Equal(t, uint32(1), tracer.droppedP0Traces)
			assert.Equal(t, uint32(2), tracer.droppedP0Spans)
		}()
		defer stop()
		tracer.config.featureFlags = make(map[string]struct{})
		tracer.config.sampler = NewRateSampler(0)
		tracer.prioritySampling.defaultRate = 0
		tracer.config.serviceName = "test_service"
		parent := tracer.StartSpan("name_1").(*span)
		child := tracer.StartSpan("name_2", ChildOf(parent.context)).(*span)
		child.Finish()
		parent.Finish()
		tracer.Stop()
		assert.Equal(t, float64(ext.PriorityAutoReject), parent.Metrics[keySamplingPriority])
		assert.Equal(t, decisionDrop, parent.context.trace.samplingDecision)
		assert.NotContains(t, parent.Metrics, keySpanSamplingMechanism)
		assert.NotContains(t, parent.Metrics, keySingleSpanSamplingRuleRate)
		assert.NotContains(t, parent.Metrics, keySingleSpanSamplingMPS)
	})

	t.Run("client_kept_with_single_spans", func(t *testing.T) {
		t.Setenv("DD_SPAN_SAMPLING_RULES", `[{"service": "test_*","name":"*", "sample_rate": 1.0}]`)
		// Rules are available. Trace sample rate equals 1. Span sample rate equals 1.
		// The trace should be kept. No single spans extracted.
		tracer, _, _, stop := startTestTracer(t)
		defer func() {
			// Must check these after tracer is stopped to avoid flakiness
			assert.Equal(t, uint32(0), tracer.droppedP0Traces)
			assert.Equal(t, uint32(0), tracer.droppedP0Spans)
		}()
		defer stop()
		tracer.config.agent.DropP0s = true
		tracer.config.featureFlags = make(map[string]struct{})
		tracer.config.sampler = NewRateSampler(1)
		tracer.prioritySampling.defaultRate = 1
		tracer.config.serviceName = "test_service"
		parent := tracer.StartSpan("name_1").(*span)
		child := tracer.StartSpan("name_2", ChildOf(parent.context)).(*span)
		child.Finish()
		parent.Finish()
		tracer.Stop()
		// single span sampling should only run on dropped traces
		assert.Equal(t, float64(ext.PriorityAutoKeep), parent.Metrics[keySamplingPriority])
		assert.Equal(t, decisionKeep, parent.context.trace.samplingDecision)
		assert.NotContains(t, parent.Metrics, keySpanSamplingMechanism)
		assert.NotContains(t, parent.Metrics, keySingleSpanSamplingRuleRate)
		assert.NotContains(t, parent.Metrics, keySingleSpanSamplingMPS)
	})

	t.Run("single_spans_with_max_per_second:rate_1.0", func(t *testing.T) {
		t.Setenv("DD_SPAN_SAMPLING_RULES",
			`[{"service": "test_*","name":"name_*", "sample_rate": 1.0,"max_per_second":50}]`)
		t.Setenv("DD_TRACE_SAMPLE_RATE", "0.8")
		tracer, _, _, stop := startTestTracer(t)
		// Don't allow the rate limiter to reset while the test is running.
		current := time.Now()
		nowTime = func() time.Time { return current }
		defer func() {
			nowTime = func() time.Time { return time.Now() }
		}()
		defer stop()
		tracer.config.featureFlags = make(map[string]struct{})
		tracer.config.serviceName = "test_service"
		var spans []*span
		for i := 0; i < 100; i++ {
			s := tracer.StartSpan(fmt.Sprintf("name_%d", i)).(*span)
			for j := 0; j < 9; j++ {
				child := tracer.newChildSpan(fmt.Sprintf("name_%d_%d", i, j), s)
				child.Finish()
				spans = append(spans, child)
			}
			s.Finish()
			spans = append(spans, s)
		}
		tracer.Stop()

		var singleSpans, keptSpans int
		for _, s := range spans {
			if _, ok := s.Metrics[keySpanSamplingMechanism]; ok {
				singleSpans++
				assert.Equal(t, 1.0, s.Metrics[keySingleSpanSamplingRuleRate])
				assert.Equal(t, 50.0, s.Metrics[keySingleSpanSamplingMPS])
			}
			if s.Metrics[keySamplingPriority] == ext.PriorityUserKeep {
				keptSpans++
			}
		}
		assert.Equal(t, 50, singleSpans)
		assert.InDelta(t, 0.8, float64(keptSpans)/float64(len(spans)), 0.19)
		assert.Equal(t, uint32(0), tracer.droppedP0Traces)
	})

	t.Run("single_spans_without_max_per_second:rate_1.0", func(t *testing.T) {
		t.Setenv("DD_SPAN_SAMPLING_RULES", `[{"service": "test_*","name":"name_*", "sample_rate": 1.0}]`)
		t.Setenv("DD_TRACE_SAMPLE_RATE", "0.8")
		tracer, _, _, stop := startTestTracer(t)
		defer stop()
		tracer.config.featureFlags = make(map[string]struct{})
		tracer.config.serviceName = "test_service"
		spans := []*span{}
		for i := 0; i < 100; i++ {
			s := tracer.StartSpan("name_1").(*span)
			for i := 0; i < 9; i++ {
				child := tracer.StartSpan("name_2", ChildOf(s.context))
				child.Finish()
				spans = append(spans, child.(*span))
			}
			spans = append(spans, s)
			s.Finish()
		}
		tracer.Stop()

		singleSpans, keptSpans := 0, 0
		for _, s := range spans {
			if _, ok := s.Metrics[keySpanSamplingMechanism]; ok {
				singleSpans++
				continue
			}
			if s.Metrics[keySamplingPriority] == ext.PriorityUserKeep {
				keptSpans++
			}
		}
		assert.Equal(t, 1000, keptSpans+singleSpans)
		assert.InDelta(t, 0.8, float64(keptSpans)/float64(1000), 0.15)
		assert.Equal(t, uint32(0), tracer.droppedP0Traces)
	})

	t.Run("single_spans_without_max_per_second:rate_0.5", func(t *testing.T) {
		t.Setenv("DD_SPAN_SAMPLING_RULES", `[{"service": "test_*","name":"name_2", "sample_rate": 0.5}]`)
		t.Setenv("DD_TRACE_SAMPLE_RATE", "0.8")
		tracer, _, _, stop := startTestTracer(t)
		defer stop()
		tracer.config.featureFlags = make(map[string]struct{})
		tracer.config.serviceName = "test_service"
		spans := []*span{}
		for i := 0; i < 100; i++ {
			s := tracer.StartSpan("name_1").(*span)
			for i := 0; i < 9; i++ {
				child := tracer.StartSpan("name_2", ChildOf(s.context))
				child.Finish()
				spans = append(spans, child.(*span))
			}
			spans = append(spans, s)
			s.Finish()
		}
		tracer.Stop()
		singleSpans, keptTotal, keptChildren := 0, 0, 0
		for _, s := range spans {
			if _, ok := s.Metrics[keySpanSamplingMechanism]; ok {
				singleSpans++
				continue
			}
			if s.Metrics[keySamplingPriority] == ext.PriorityUserKeep {
				keptTotal++
				if s.context.trace.root.SpanID != s.SpanID {
					keptChildren++
				}
			}
		}
		assert.InDelta(t, 0.5, float64(singleSpans)/(float64(900-keptChildren)), 0.15)
		assert.InDelta(t, 0.8, float64(keptTotal)/1000, 0.15)
		assert.Equal(t, uint32(0), tracer.droppedP0Traces)
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

	t.Run("dd-env", func(t *testing.T) {
		t.Setenv("DD_RUNTIME_METRICS_ENABLED", "true")
		tp := new(log.RecordLogger)
		tp.Ignore("appsec: ", telemetry.LogPrefix)
		tracer := newTracer(WithLogger(tp), WithDebugMode(true))
		defer tracer.Stop()
		assert.Contains(t, tp.Logs()[0], "DEBUG: Runtime metrics enabled")
	})

	t.Run("otel-env", func(t *testing.T) {
		t.Setenv("OTEL_METRICS_EXPORTER", "none")
		c := newConfig()
		assert.False(t, c.runtimeMetrics)
	})

	t.Run("override-chain", func(t *testing.T) {
		// dd env overrides otel env
		t.Setenv("OTEL_METRICS_EXPORTER", "none")
		t.Setenv("DD_RUNTIME_METRICS_ENABLED", "true")
		c := newConfig()
		assert.True(t, c.runtimeMetrics)
		// tracer option overrides dd env
		t.Setenv("DD_RUNTIME_METRICS_ENABLED", "false")
		c = newConfig(WithRuntimeMetrics())
		assert.True(t, c.runtimeMetrics)
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
	span := tracer.StartSpan("web.request", opts...).(*span)
	assert := assert.New(t)
	assert.Equal("test", span.Type)
	assert.Equal("test.service", span.Service)
	assert.Equal("test.resource", span.Resource)
	assert.Equal(now.UnixNano(), span.Start)
	assert.Equal(uint64(420), span.SpanID)
	assert.Equal(uint64(420), span.TraceID)
	assert.Equal(1.0, span.Metrics[keyTopLevel])
}

func TestTracerStartSpanOptions128(t *testing.T) {
	tracer := newTracer()
	internal.SetGlobalTracer(tracer)
	defer tracer.Stop()
	defer internal.SetGlobalTracer(&internal.NoopTracer{})
	t.Run("64-bit-trace-id", func(t *testing.T) {
		assert := assert.New(t)
		t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
		opts := []StartSpanOption{
			WithSpanID(987654),
		}
		s := tracer.StartSpan("web.request", opts...).(*span)
		assert.Equal(uint64(987654), s.SpanID)
		assert.Equal(uint64(987654), s.TraceID)
		id := id128FromSpan(assert, s.Context())
		assert.Empty(s.Meta[keyTraceID128])
		idBytes, err := hex.DecodeString(id)
		assert.NoError(err)
		assert.Equal(uint64(0), binary.BigEndian.Uint64(idBytes[:8])) // high 64 bits should be 0
		assert.Equal(s.Context().TraceID(), binary.BigEndian.Uint64(idBytes[8:]))
	})
	t.Run("128-bit-trace-id", func(t *testing.T) {
		assert := assert.New(t)
		// 128-bit trace ids are enabled by default.
		opts128 := []StartSpanOption{
			WithSpanID(987654),
			StartTime(time.Unix(123456, 0)),
		}
		s := tracer.StartSpan("web.request", opts128...).(*span)
		assert.Equal(uint64(987654), s.SpanID)
		assert.Equal(uint64(987654), s.TraceID)
		id := id128FromSpan(assert, s.Context())
		// hex_encoded(<32-bit unix seconds> <32 bits of zero> <64 random bits>)
		// 0001e240 (123456) + 00000000 (zeros) + 00000000000f1206 (987654)
		assert.Equal("0001e2400000000000000000000f1206", id)
		s.Finish()
		assert.Equal(id[:16], s.Meta[keyTraceID128])
	})
}

func TestTracerStartChildSpan(t *testing.T) {
	t.Run("own-service", func(t *testing.T) {
		assert := assert.New(t)
		tracer := newTracer()
		defer tracer.Stop()
		root := tracer.StartSpan("web.request", ServiceName("root-service")).(*span)
		child := tracer.StartSpan("db.query",
			ChildOf(root.Context()),
			ServiceName("child-service"),
			WithSpanID(69)).(*span)

		assert.NotEqual(uint64(0), child.TraceID)
		assert.NotEqual(uint64(0), child.SpanID)
		assert.Equal(root.SpanID, child.ParentID)
		assert.Equal(root.TraceID, child.ParentID)
		assert.Equal(root.TraceID, child.TraceID)
		assert.Equal(uint64(69), child.SpanID)
		assert.Equal("child-service", child.Service)

		// the root and child are both marked as "top level"
		assert.Equal(1.0, root.Metrics[keyTopLevel])
		assert.Equal(1.0, child.Metrics[keyTopLevel])
	})

	t.Run("inherit-service", func(t *testing.T) {
		assert := assert.New(t)
		tracer := newTracer()
		defer tracer.Stop()
		root := tracer.StartSpan("web.request", ServiceName("root-service")).(*span)
		child := tracer.StartSpan("db.query", ChildOf(root.Context())).(*span)

		assert.Equal("root-service", child.Service)
		// the root is marked as "top level", but the child is not
		assert.Equal(1.0, root.Metrics[keyTopLevel])
		assert.NotContains(child.Metrics, keyTopLevel)
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
	root := tracer.StartSpan("web.request").(*span)
	root.SetBaggageItem("x", "y")
	root.SetTag(ext.SamplingPriority, -1)
	ctx := root.Context().(*spanContext)
	headers := http.Header{}

	// inject the spanContext
	carrier := HTTPHeadersCarrier(headers)
	err := tracer.Inject(ctx, carrier)
	assert.Nil(err)

	tid := strconv.FormatUint(root.TraceID, 10)
	pid := strconv.FormatUint(root.SpanID, 10)

	assert.Equal(headers.Get(DefaultTraceIDHeader), tid)
	assert.Equal(headers.Get(DefaultParentIDHeader), pid)
	assert.Equal(headers.Get(DefaultBaggageHeaderPrefix+"x"), "y")
	assert.Equal(headers.Get(DefaultPriorityHeader), "-1")

	// retrieve the spanContext
	propagated, err := tracer.Extract(carrier)
	assert.Nil(err)
	pctx := propagated.(*spanContext)

	// compare if there is a Context match
	assert.Equal(ctx.traceID, pctx.traceID)
	assert.Equal(ctx.spanID, pctx.spanID)
	assert.Equal(ctx.baggage, pctx.baggage)
	assert.Equal(*ctx.trace.priority, -1.)

	// ensure a child can be created
	child := tracer.StartSpan("db.query", ChildOf(propagated)).(*span)

	assert.NotEqual(uint64(0), child.TraceID)
	assert.NotEqual(uint64(0), child.SpanID)
	assert.Equal(root.SpanID, child.ParentID)
	assert.Equal(root.TraceID, child.ParentID)
	assert.Equal(*child.context.trace.priority, -1.)
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
	return newHTTPTransport(defaultURL, defaultHTTPClient(0))
}

func TestNewSpan(t *testing.T) {
	assert := assert.New(t)

	// the tracer must create root spans
	tracer := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	span := tracer.newRootSpan("pylons.request", "pylons", "/")
	assert.Equal(uint64(0), span.ParentID)
	assert.Equal("pylons", span.Service)
	assert.Equal("pylons.request", span.Name)
	assert.Equal("/", span.Resource)
}

func TestNewSpanChild(t *testing.T) {
	testNewSpanChild(t, false)
	testNewSpanChild(t, true)
}

func testNewSpanChild(t *testing.T, is128 bool) {
	t.Run(fmt.Sprintf("TestNewChildSpan(is128=%t)", is128), func(t *testing.T) {
		if !is128 {
			t.Setenv("DD_TRACE_128_BIT_TRACEID_GENERATION_ENABLED", "false")
		}
		assert := assert.New(t)

		// the tracer must create child spans
		tracer := newTracer(withTransport(newDefaultTransport()))
		internal.SetGlobalTracer(tracer)
		defer tracer.Stop()
		parent := tracer.newRootSpan("pylons.request", "pylons", "/")
		child := tracer.newChildSpan("redis.command", parent)
		// ids and services are inherited
		assert.Equal(parent.SpanID, child.ParentID)
		assert.Equal(parent.TraceID, child.TraceID)
		id := id128FromSpan(assert, child.Context())
		assert.Equal(id128FromSpan(assert, parent.Context()), id)
		assert.Equal(parent.Service, child.Service)
		// the resource is not inherited and defaults to the name
		assert.Equal("redis.command", child.Resource)

		// Meta[keyTraceID128] gets set upon Finish
		parent.Finish()
		child.Finish()
		if is128 {
			assert.Equal(id[:16], parent.Meta[keyTraceID128])
			assert.Empty(child.Meta[keyTraceID128])
		} else {
			assert.Empty(child.Meta[keyTraceID128])
			assert.Empty(parent.Meta[keyTraceID128])
		}
	})
}

func TestNewRootSpanHasPid(t *testing.T) {
	assert := assert.New(t)

	tracer := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	root := tracer.newRootSpan("pylons.request", "pylons", "/")

	assert.Equal(float64(os.Getpid()), root.Metrics[ext.Pid])
}

func TestNewChildHasNoPid(t *testing.T) {
	assert := assert.New(t)

	tracer := newTracer(withTransport(newDefaultTransport()))
	defer tracer.Stop()
	root := tracer.newRootSpan("pylons.request", "pylons", "/")
	child := tracer.newChildSpan("redis.command", root)

	assert.Equal("", child.Meta[ext.Pid])
}

func TestTracerSampler(t *testing.T) {
	assert := assert.New(t)

	sampler := NewRateSampler(0.9999) // high probability of sampling
	tracer := newTracer(
		withTransport(newDefaultTransport()),
		WithSampler(sampler),
	)
	defer tracer.Stop()

	span := tracer.newRootSpan("pylons.request", "pylons", "/")

	if !sampler.Sample(span) {
		t.Skip("wasn't sampled") // no flaky tests
	}
	// only run test if span was sampled to avoid flaky tests
	_, ok := span.Metrics[sampleRateMetricKey]
	assert.True(ok)
}

func TestTracerPrioritySampler(t *testing.T) {
	assert := assert.New(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"rate_by_service":{
				"service:,env:":0.1,
				"service:my-service,env:":0.2,
				"service:my-service,env:default":0.2,
				"service:my-service,env:other":0.3
			}
		}`))
	}))
	url := "http://" + srv.Listener.Addr().String()

	tr, _, flush, stop := startTestTracer(t,
		withTransport(newHTTPTransport(url, defaultHTTPClient(0))),
	)
	defer stop()

	// default rates (1.0)
	s := tr.newEnvSpan("pylons", "")
	assert.Equal(1., s.Metrics[keySamplingPriorityRate])
	assert.Equal(1., s.Metrics[keySamplingPriority])
	assert.Equal("-1", s.context.trace.propagatingTags[keyDecisionMaker])
	p, ok := s.context.SamplingPriority()
	assert.True(ok)
	assert.EqualValues(p, s.Metrics[keySamplingPriority])
	s.Finish()

	tr.awaitPayload(t, 1)
	flush(-1)
	time.Sleep(100 * time.Millisecond)

	for i, tt := range []struct {
		service, env string
		rate         float64
	}{
		{
			service: "pylons",
			rate:    0.1,
		},
		{
			service: "my-service",
			rate:    0.2,
		},
		{
			service: "my-service",
			env:     "default",
			rate:    0.2,
		},
		{
			service: "my-service",
			env:     "other",
			rate:    0.3,
		},
	} {
		s := tr.newEnvSpan(tt.service, tt.env)
		assert.Equal(tt.rate, s.Metrics[keySamplingPriorityRate], strconv.Itoa(i))
		prio, ok := s.Metrics[keySamplingPriority]
		if prio > 0 {
			assert.Equal("-1", s.context.trace.propagatingTags[keyDecisionMaker])
		} else {
			assert.Equal("", s.context.trace.propagatingTags[keyDecisionMaker])
		}
		assert.True(ok)
		assert.Contains([]float64{0, 1}, prio)
		p, ok := s.context.SamplingPriority()
		assert.True(ok)
		assert.EqualValues(p, prio)

		// injectable
		h := make(http.Header)
		tr.Inject(s.Context(), HTTPHeadersCarrier(h))
		assert.Equal(strconv.Itoa(int(prio)), h.Get(DefaultPriorityHeader))
	}
}

func TestTracerEdgeSampler(t *testing.T) {
	assert := assert.New(t)

	// a sample rate of 0 should sample nothing
	tracer0, _, _, stop := startTestTracer(t,
		withTransport(newDefaultTransport()),
		WithSampler(NewRateSampler(0)),
	)
	defer stop()
	// a sample rate of 1 should sample everything
	tracer1, _, _, stop := startTestTracer(t,
		withTransport(newDefaultTransport()),
		WithSampler(NewRateSampler(1)),
	)
	defer stop()

	count := payloadQueueSize / 3

	for i := 0; i < count; i++ {
		span0 := tracer0.StartSpan("pylons.request", SpanType("test"), ServiceName("pylons"), ResourceName("/"))
		span0.Finish()
		span1 := tracer1.StartSpan("pylons.request", SpanType("test"), ServiceName("pylons"), ResourceName("/"))
		span1.Finish()
	}

	assert.Equal(tracer0.traceWriter.(*agentTraceWriter).payload.itemCount(), 0)
	tracer1.awaitPayload(t, count)
}

func TestTracerConcurrent(t *testing.T) {
	assert := assert.New(t)
	tracer, transport, flush, stop := startTestTracer(t)
	defer stop()

	// Wait for three different goroutines that should create
	// three different traces with one child each
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		tracer.newRootSpan("pylons.request", "pylons", "/").Finish()
	}()
	go func() {
		defer wg.Done()
		tracer.newRootSpan("pylons.request", "pylons", "/home").Finish()
	}()
	go func() {
		defer wg.Done()
		tracer.newRootSpan("pylons.request", "pylons", "/trace").Finish()
	}()

	wg.Wait()
	flush(3)
	traces := transport.Traces()
	assert.Len(traces, 3)
	assert.Len(traces[0], 1)
	assert.Len(traces[1], 1)
	assert.Len(traces[2], 1)
}

func TestTracerParentFinishBeforeChild(t *testing.T) {
	assert := assert.New(t)
	tracer, transport, flush, stop := startTestTracer(t)
	defer stop()

	// Testing an edge case: a child refers to a parent that is already closed.

	parent := tracer.newRootSpan("pylons.request", "pylons", "/")
	parent.Finish()

	flush(1)
	traces := transport.Traces()
	assert.Len(traces, 1)
	assert.Len(traces[0], 1)
	comparePayloadSpans(t, parent, traces[0][0])

	child := tracer.newChildSpan("redis.command", parent)
	child.Finish()

	flush(1)

	traces = transport.Traces()
	assert.Len(traces, 1)
	assert.Len(traces[0], 1)
	comparePayloadSpans(t, child, traces[0][0])
	assert.Equal(parent.SpanID, traces[0][0].ParentID, "child should refer to parent, even if they have been flushed separately")
}

func TestTracerConcurrentMultipleSpans(t *testing.T) {
	assert := assert.New(t)
	tracer, transport, flush, stop := startTestTracer(t)
	defer stop()

	// Wait for two different goroutines that should create
	// two traces with two children each
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		parent := tracer.newRootSpan("pylons.request", "pylons", "/")
		child := tracer.newChildSpan("redis.command", parent)
		child.Finish()
		parent.Finish()
	}()
	go func() {
		defer wg.Done()
		parent := tracer.newRootSpan("pylons.request", "pylons", "/")
		child := tracer.newChildSpan("redis.command", parent)
		child.Finish()
		parent.Finish()
	}()

	wg.Wait()
	flush(2)
	traces := transport.Traces()
	assert.Len(traces, 2)
	assert.Len(traces[0], 2)
	assert.Len(traces[1], 2)
}

func TestTracerAtomicFlush(t *testing.T) {
	assert := assert.New(t)
	tracer, transport, flush, stop := startTestTracer(t)
	defer stop()

	// Make sure we don't flush partial bits of traces
	root := tracer.newRootSpan("pylons.request", "pylons", "/")
	span := tracer.newChildSpan("redis.command", root)
	span1 := tracer.newChildSpan("redis.command.1", span)
	span2 := tracer.newChildSpan("redis.command.2", span)
	span.Finish()
	span1.Finish()
	span2.Finish()

	flush(-1)
	time.Sleep(100 * time.Millisecond)
	traces := transport.Traces()
	assert.Len(traces, 0, "nothing should be flushed now as span2 is not finished yet")

	root.Finish()

	flush(1)
	traces = transport.Traces()
	assert.Len(traces, 1)
	assert.Len(traces[0], 4, "all spans should show up at once")
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

func TestTracerRace(t *testing.T) {
	assert := assert.New(t)

	tracer, transport, flush, stop := startTestTracer(t)
	defer stop()

	total := payloadQueueSize / 3
	var wg sync.WaitGroup
	wg.Add(total)

	// Trying to be quite brutal here, firing lots of concurrent things, finishing in
	// different orders, and modifying spans after creation.
	for n := 0; n < total; n++ {
		i := n // keep local copy
		odd := ((i % 2) != 0)
		go func() {
			if i%11 == 0 {
				time.Sleep(time.Microsecond)
			}

			parent := tracer.newRootSpan("pylons.request", "pylons", "/")

			tracer.newChildSpan("redis.command", parent).Finish()
			child := tracer.newChildSpan("async.service", parent)

			if i%13 == 0 {
				time.Sleep(time.Microsecond)
			}

			if odd {
				parent.SetTag("odd", "true")
				parent.SetTag("oddity", 1)
				parent.Finish()
			} else {
				child.SetTag("odd", "false")
				child.SetTag("oddity", 0)
				child.Finish()
			}

			if i%17 == 0 {
				time.Sleep(time.Microsecond)
			}

			if odd {
				child.Resource = "HGETALL"
				child.SetTag("odd", "false")
				child.SetTag("oddity", 0)
			} else {
				parent.Resource = "/" + strconv.Itoa(i) + ".html"
				parent.SetTag("odd", "true")
				parent.SetTag("oddity", 1)
			}

			if i%19 == 0 {
				time.Sleep(time.Microsecond)
			}

			if odd {
				child.Finish()
			} else {
				parent.Finish()
			}

			wg.Done()
		}()
	}

	wg.Wait()

	flush(total)
	traces := transport.Traces()
	assert.Len(traces, total, "we should have exactly as many traces as expected")
	for _, trace := range traces {
		assert.Len(trace, 3, "each trace should have exactly 3 spans")
		var parent, child, redis *span
		for _, span := range trace {
			switch span.Name {
			case "pylons.request":
				parent = span
			case "async.service":
				child = span
			case "redis.command":
				redis = span
			default:
				assert.Fail("unexpected span", span)
			}
		}
		assert.NotNil(parent)
		assert.NotNil(child)
		assert.NotNil(redis)

		assert.Equal(uint64(0), parent.ParentID)
		assert.Equal(parent.TraceID, parent.SpanID)

		assert.Equal(parent.TraceID, redis.TraceID)
		assert.Equal(parent.TraceID, child.TraceID)

		assert.Equal(parent.TraceID, redis.ParentID)
		assert.Equal(parent.TraceID, child.ParentID)
	}
}

// TestWorker is definitely a flaky test, as here we test that the worker
// background task actually does flush things. Most other tests are and should
// be using forceFlush() to make sure things are really sent to transport.
// Here, we just wait until things show up, as we would do with a real program.
func TestWorker(t *testing.T) {
	tracer, transport, flush, stop := startTestTracer(t)
	defer stop()

	n := payloadQueueSize * 10 // put more traces than the chan size, on purpose
	for i := 0; i < n; i++ {
		root := tracer.newRootSpan("pylons.request", "pylons", "/")
		child := tracer.newChildSpan("redis.command", root)
		child.Finish()
		root.Finish()
	}

	flush(-1)
	timeout := time.After(2 * time.Second * timeMultiplicator)
loop:
	for {
		select {
		case <-timeout:
			t.Fatalf("timed out waiting, got %d < %d", transport.Len(), payloadQueueSize)
		default:
			if transport.Len() >= payloadQueueSize {
				break loop
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
}

func TestPushPayload(t *testing.T) {
	tracer, _, flush, stop := startTestTracer(t)
	defer stop()

	s := newBasicSpan("3MB")
	s.Meta["key"] = strings.Repeat("X", payloadSizeLimit/2+10)

	// half payload size reached
	tracer.pushChunk(&chunk{[]*span{s}, true})
	tracer.awaitPayload(t, 1)

	// payload size exceeded
	tracer.pushChunk(&chunk{[]*span{s}, true})
	flush(2)
}

func TestPushTrace(t *testing.T) {
	assert := assert.New(t)

	tp := new(log.RecordLogger)
	log.UseLogger(tp)
	tracer := newUnstartedTracer()
	defer tracer.statsd.Close()
	trace := []*span{
		{
			Name:     "pylons.request",
			Service:  "pylons",
			Resource: "/",
		},
		{
			Name:     "pylons.request",
			Service:  "pylons",
			Resource: "/foo",
		},
	}
	tracer.pushChunk(&chunk{spans: trace})

	assert.Len(tracer.out, 1)

	t0 := <-tracer.out
	assert.Equal(&chunk{spans: trace}, t0)

	many := payloadQueueSize + 2
	for i := 0; i < many; i++ {
		tracer.pushChunk(&chunk{spans: make([]*span, i)})
	}
	assert.Len(tracer.out, payloadQueueSize)
	log.Flush()
	assert.True(len(tp.Logs()) >= 1)
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

	testReportHostnameEnabled := func(t *testing.T, name string, withComputeStats bool) {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DD_TRACE_REPORT_HOSTNAME", "true")
			t.Setenv("DD_TRACE_COMPUTE_STATS", fmt.Sprintf("%t", withComputeStats))

			tracer, _, _, stop := startTestTracer(t)
			defer stop()

			root := tracer.StartSpan("root").(*span)
			child := tracer.StartSpan("child", ChildOf(root.Context())).(*span)
			child.Finish()
			root.Finish()

			assert := assert.New(t)

			name, ok := root.Meta[keyHostname]
			assert.True(ok)
			assert.Equal(name, tracer.config.hostname)

			name, ok = child.Meta[keyHostname]
			assert.True(ok)
			assert.Equal(name, tracer.config.hostname)
		})
	}
	testReportHostnameEnabled(t, "DD_TRACE_REPORT_HOSTNAME/set,DD_TRACE_COMPUTE_STATS/true", true)
	testReportHostnameEnabled(t, "DD_TRACE_REPORT_HOSTNAME/set,DD_TRACE_COMPUTE_STATS/false", false)

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
	t.Run("git-metadata-from-dd-tags", func(t *testing.T) {
		t.Setenv(maininternal.EnvDDTags, "git.commit.sha:123456789ABCD git.repository_url:github.com/user/repo go_path:somepath")
		maininternal.RefreshGitMetadataTags()

		tracer, _, _, stop := startTestTracer(t)
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request").(*span)
		sp.context.finish()

		assert.Equal("123456789ABCD", sp.Meta[maininternal.TraceTagCommitSha])
		assert.Equal("github.com/user/repo", sp.Meta[maininternal.TraceTagRepositoryURL])
		assert.Equal("somepath", sp.Meta[maininternal.TraceTagGoPath])
	})

	t.Run("git-metadata-from-dd-tags-with-credentials", func(t *testing.T) {
		t.Setenv(maininternal.EnvDDTags, "git.commit.sha:123456789ABCD git.repository_url:https://user:passwd@github.com/user/repo go_path:somepath")
		maininternal.RefreshGitMetadataTags()

		tracer, _, _, stop := startTestTracer(t)
		defer stop()

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
		maininternal.RefreshGitMetadataTags()

		tracer, _, _, stop := startTestTracer(t)
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request").(*span)
		sp.context.finish()

		assert.Equal("123456789ABCDE", sp.Meta[maininternal.TraceTagCommitSha])
		assert.Equal("github.com/user/repo_new", sp.Meta[maininternal.TraceTagRepositoryURL])
	})

	t.Run("git-metadata-from-env-with-credentials", func(t *testing.T) {
		t.Setenv(maininternal.EnvGitRepositoryURL, "https://u:t@github.com/user/repo_new")
		t.Setenv(maininternal.EnvGitCommitSha, "123456789ABCDE")
		maininternal.RefreshGitMetadataTags()

		tracer, _, _, stop := startTestTracer(t)
		defer stop()

		assert := assert.New(t)
		sp := tracer.StartSpan("http.request").(*span)
		sp.context.finish()

		assert.Equal("123456789ABCDE", sp.Meta[maininternal.TraceTagCommitSha])
		assert.Equal("https://github.com/user/repo_new", sp.Meta[maininternal.TraceTagRepositoryURL])
	})

	t.Run("git-metadata-from-env-and-tags", func(t *testing.T) {
		t.Setenv(maininternal.EnvDDTags, "git.commit.sha:123456789ABCD")
		t.Setenv(maininternal.EnvGitRepositoryURL, "github.com/user/repo")
		maininternal.RefreshGitMetadataTags()

		tracer, _, _, stop := startTestTracer(t)
		defer stop()

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
		maininternal.RefreshGitMetadataTags()

		tracer, _, _, stop := startTestTracer(t)
		defer stop()

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

	b.ResetTimer()
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

func BenchmarkStartSpanConcurrent(b *testing.B) {
	tracer, _, _, stop := startTestTracer(b, WithLogger(log.DiscardLogger{}), WithSampler(NewRateSampler(0)))
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

func BenchmarkGenSpanID(b *testing.B) {
	b.ResetTimer()
	for n := 0; n < b.N; n++ {
		generateSpanID(0)
	}
}

// startTestTracer returns a Tracer with a DummyTransport
func startTestTracer(t testing.TB, opts ...StartOption) (trc *tracer, transport *dummyTransport, flush func(n int), stop func()) {
	transport = newDummyTransport()
	tick := make(chan time.Time)
	o := append([]StartOption{
		withTransport(transport),
		withTickChan(tick),
	}, opts...)
	tracer := newTracer(o...)
	internal.SetGlobalTracer(tracer)
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
		internal.SetGlobalTracer(&internal.NoopTracer{})
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

func TestFlush(t *testing.T) {
	tr, _, _, stop := startTestTracer(t)
	defer stop()

	tw := newTestTraceWriter()
	tr.traceWriter = tw

	ts := &statsdtest.TestStatsdClient{}
	tr.statsd = ts

	transport := newDummyTransport()
	c := newConcentrator(&config{transport: transport}, defaultStatsBucketSize)
	tr.stats = c
	c.Start()
	defer c.Stop()

	tr.StartSpan("op").Finish()
	timeout := time.After(time.Second)
loop:
	for {
		select {
		case <-timeout:
			t.Fatal("timed out waiting for trace to be added to writer")
		default:
			if len(tw.Buffered()) > 0 {
				// trace got buffered
				break loop
			}
			time.Sleep(time.Millisecond)
		}
	}
	as := &aggregableSpan{
		key: aggregation{
			Name: "http.request",
		},
		// Start must be older than latest bucket to get flushed
		Start:    time.Now().UnixNano() - 3*defaultStatsBucketSize,
		Duration: 1,
	}
	c.add(as)

	assert.Len(t, tw.Flushed(), 0)
	assert.Zero(t, ts.Flushed())
	assert.Len(t, transport.Stats(), 0)
	tr.flushSync()
	assert.Len(t, tw.Flushed(), 1)
	assert.Equal(t, 1, ts.Flushed())
	assert.Len(t, transport.Stats(), 1)
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

func TestUserMonitoring(t *testing.T) {
	const id = "john.doe#12345"
	const name = "John Doe"
	const email = "john.doe@hostname.com"
	const scope = "read:message, write:files"
	const role = "admin"
	const sessionID = "session#12345"
	expected := []struct{ key, value string }{
		{key: keyUserID, value: id},
		{key: keyUserName, value: name},
		{key: keyUserEmail, value: email},
		{key: keyUserScope, value: scope},
		{key: keyUserRole, value: role},
		{key: keyUserSessionID, value: sessionID},
	}
	tr := newTracer()
	defer tr.Stop()
	internal.SetGlobalTracer(tr)
	defer internal.SetGlobalTracer(&internal.NoopTracer{})

	t.Run("root", func(t *testing.T) {
		s := tr.newRootSpan("root", "test", "test")
		SetUser(s, id, WithUserEmail(email), WithUserName(name), WithUserScope(scope),
			WithUserRole(role), WithUserSessionID(sessionID))
		s.Finish()
		for _, pair := range expected {
			assert.Equal(t, pair.value, s.Meta[pair.key])
		}
	})

	t.Run("nested", func(t *testing.T) {
		root := tr.newRootSpan("root", "test", "test")
		child := tr.newChildSpan("child", root)
		SetUser(child, id, WithUserEmail(email), WithUserName(name), WithUserScope(scope),
			WithUserRole(role), WithUserSessionID(sessionID))
		child.Finish()
		root.Finish()
		for _, pair := range expected {
			assert.Equal(t, pair.value, root.Meta[pair.key])
		}
	})

	t.Run("propagation", func(t *testing.T) {
		s := tr.newRootSpan("root", "test", "test")
		SetUser(s, id, WithPropagation())
		s.Finish()
		assert.Equal(t, id, s.Meta[keyUserID])
		encoded := base64.StdEncoding.EncodeToString([]byte(id))
		assert.Equal(t, encoded, s.context.trace.propagatingTags[keyPropagatedUserID])
		assert.Equal(t, encoded, s.Meta[keyPropagatedUserID])
	})

	t.Run("no-propagation", func(t *testing.T) {
		s := tr.newRootSpan("root", "test", "test")
		SetUser(s, id)
		s.Finish()
		_, ok := s.Meta[keyUserID]
		assert.True(t, ok)
		_, ok = s.Meta[keyPropagatedUserID]
		assert.False(t, ok)
		_, ok = s.context.trace.propagatingTags[keyPropagatedUserID]
		assert.False(t, ok)
	})

	// This tests data races for trace.propagatingTags reads/writes through public API.
	// The Go data race detector should not complain when running the test with '-race'.
	t.Run("data-race", func(t *testing.T) {
		wg := &sync.WaitGroup{}
		wg.Add(2)

		root := tr.newRootSpan("root", "test", "test")

		go func() {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				SetUser(root, "test")
			}
		}()
		go func() {
			defer wg.Done()
			for i := 0; i < 10000; i++ {
				tr.StartSpan("test", ChildOf(root.Context())).Finish()
			}
		}()

		root.Finish()
		wg.Wait()
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

func BenchmarkSingleSpanRetention(b *testing.B) {
	b.Run("no-rules", func(b *testing.B) {
		tracer, _, _, stop := startTestTracer(b)
		defer stop()
		tracer.config.agent.DropP0s = true
		tracer.config.featureFlags = make(map[string]struct{})
		tracer.config.featureFlags["discovery"] = struct{}{}
		tracer.config.sampler = NewRateSampler(0)
		tracer.prioritySampling.defaultRate = 0
		tracer.config.serviceName = "test_service"
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			span := tracer.StartSpan("name_1").(*span)
			for i := 0; i < 100; i++ {
				child := tracer.StartSpan("name_2", ChildOf(span.context))
				child.Finish()
			}
			span.Finish()
		}
	})

	b.Run("with-rules/match-half", func(b *testing.B) {
		b.Setenv("DD_SPAN_SAMPLING_RULES", `[{"service": "test_*","name":"*_1", "sample_rate": 1.0, "max_per_second": 15.0}]`)
		tracer, _, _, stop := startTestTracer(b)
		defer stop()
		tracer.config.agent.DropP0s = true
		tracer.config.featureFlags = make(map[string]struct{})
		tracer.config.featureFlags["discovery"] = struct{}{}
		tracer.config.sampler = NewRateSampler(0)
		tracer.prioritySampling.defaultRate = 0
		tracer.config.serviceName = "test_service"
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			span := tracer.StartSpan("name_1").(*span)
			for i := 0; i < 50; i++ {
				child := tracer.StartSpan("name_2", ChildOf(span.context))
				child.Finish()
			}
			for i := 0; i < 50; i++ {
				child := tracer.StartSpan("name", ChildOf(span.context))
				child.Finish()
			}
			span.Finish()
		}
	})

	b.Run("with-rules/match-all", func(b *testing.B) {
		b.Setenv("DD_SPAN_SAMPLING_RULES", `[{"service": "test_*","name":"*_1", "sample_rate": 1.0, "max_per_second": 15.0}]`)
		tracer, _, _, stop := startTestTracer(b)
		defer stop()
		tracer.config.agent.DropP0s = true
		tracer.config.featureFlags = make(map[string]struct{})
		tracer.config.featureFlags["discovery"] = struct{}{}
		tracer.config.sampler = NewRateSampler(0)
		tracer.prioritySampling.defaultRate = 0
		tracer.config.serviceName = "test_service"
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			span := tracer.StartSpan("name_1").(*span)
			for i := 0; i < 100; i++ {
				child := tracer.StartSpan("name_2", ChildOf(span.context))
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
