// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupteardown(start, max int) func() {
	oldStartSize := traceStartSize
	oldMaxSize := traceMaxSize
	traceStartSize = start
	traceMaxSize = max
	return func() {
		traceStartSize = oldStartSize
		traceMaxSize = oldMaxSize
	}
}

func TestNewSpanContextPushError(t *testing.T) {
	defer setupteardown(2, 2)()

	tp := new(log.RecordLogger)
	tp.Ignore("appsec: ", telemetry.LogPrefix)
	_, _, _, stop := startTestTracer(t, WithLogger(tp), WithLambdaMode(true))
	defer stop()
	parent := newBasicSpan("test1")                  // 1st span in trace
	parent.context.trace.push(newBasicSpan("test2")) // 2nd span in trace
	child := newSpan("child", "", "", 0, 0, 0)

	// new context having a parent with a trace of two spans.
	// One more should overflow.
	child.context = newSpanContext(child, parent.context)

	log.Flush()
	assert.Contains(t, tp.Logs()[0], "ERROR: trace buffer full (2)")
}

func TestAsyncSpanRace(t *testing.T) {
	testAsyncSpanRace(t)
}

func TestAsyncSpanRacePartialFlush(t *testing.T) {
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "1")
	testAsyncSpanRace(t)
}

func testAsyncSpanRace(t *testing.T) {
	// This tests a regression where asynchronously finishing spans would
	// modify a flushing root's sampling priority.
	_, _, _, stop := startTestTracer(t)
	defer stop()

	for i := 0; i < 100; i++ {
		// The test has 100 iterations because it is not easy to reproduce the race.
		t.Run("", func(t *testing.T) {
			root, ctx := StartSpanFromContext(context.Background(), "root", Tag(ext.SamplingPriority, ext.PriorityUserKeep))
			var wg sync.WaitGroup
			done := make(chan struct{})
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-done
				root.Finish()
				for i := 0; i < 500; i++ {
					for range root.(*span).Metrics {
						// this range simulates iterating over the metrics map
						// as we do when encoding msgpack upon flushing.
						continue
					}
				}
			}()
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-done
				root.Finish()
				for i := 0; i < 500; i++ {
					for range root.(*span).Meta {
						// this range simulates iterating over the meta map
						// as we do when encoding msgpack upon flushing.
						continue
					}
				}
			}()
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-done
				for i := 0; i < 50; i++ {
					// to trigger the bug, the child should be created after the root was finished,
					// as its being flushed
					child, _ := StartSpanFromContext(ctx, "child", Tag(ext.SamplingPriority, ext.PriorityUserKeep))
					child.Finish()
				}
			}()
			// closing will attempt trigger the two goroutines at approximately the same time.
			close(done)
			wg.Wait()
		})
	}

	// Test passes if no panic occurs while running.
}

func TestSpanTracePushOne(t *testing.T) {
	defer setupteardown(2, 5)()

	assert := assert.New(t)

	_, transport, flush, stop := startTestTracer(t)
	defer stop()

	traceID := randUint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0)
	trace := root.context.trace

	assert.Len(trace.spans, 1)
	assert.Equal(root, trace.spans[0], "the span is the one pushed before")

	root.Finish()
	flush(1)

	traces := transport.Traces()
	assert.Len(traces, 1)
	trc := traces[0]
	assert.Len(trc, 1, "there was a trace in the channel")
	comparePayloadSpans(t, root, trc[0])
	assert.Equal(0, len(trace.spans), "no more spans in the trace")
}

func TestPartialFlush(t *testing.T) {
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "2")
	t.Run("WithFlush", func(t *testing.T) {
		telemetryClient := new(telemetrytest.MockClient)
		telemetryClient.ProductChange(telemetry.NamespaceTracers, true, nil)
		defer telemetry.MockGlobalClient(telemetryClient)()
		tracer, transport, flush, stop := startTestTracer(t)
		defer stop()

		root := tracer.StartSpan("root")
		root.(*span).context.trace.setTag("someTraceTag", "someValue")
		var children []*span
		for i := 0; i < 3; i++ { // create 3 child spans
			child := tracer.StartSpan(fmt.Sprintf("child%d", i), ChildOf(root.Context()))
			children = append(children, child.(*span))
			child.Finish()
		}
		flush(1)

		ts := transport.Traces()
		require.Len(t, ts, 1)
		require.Len(t, ts[0], 2)
		assert.Equal(t, "someValue", ts[0][0].Meta["someTraceTag"])
		assert.Equal(t, 1.0, ts[0][0].Metrics[keySamplingPriority])
		assert.Empty(t, ts[0][1].Meta["someTraceTag"])              // the tag should only be on the first span in the chunk
		assert.Equal(t, 1.0, ts[0][1].Metrics[keySamplingPriority]) // the tag should only be on the first span in the chunk
		comparePayloadSpans(t, children[0], ts[0][0])
		comparePayloadSpans(t, children[1], ts[0][1])

		telemetryClient.AssertCalled(t, "Count", telemetry.NamespaceTracers, "trace_partial_flush.count", 1.0, []string{"reason:large_trace"}, true)
		// TODO: (Support MetricKindDist) Re-enable these when we actually support `MetricKindDist`
		//telemetryClient.AssertCalled(t, "Record", telemetry.NamespaceTracers, "trace_partial_flush.spans_closed", 2.0, []string(nil), true) // Typed-nil here to not break usage of reflection in `mock` library.
		//telemetryClient.AssertCalled(t, "Record", telemetry.NamespaceTracers, "trace_partial_flush.spans_remaining", 1.0, []string(nil), true)

		root.Finish()
		flush(1)
		tsRoot := transport.Traces()
		require.Len(t, tsRoot, 1)
		require.Len(t, tsRoot[0], 2)
		assert.Equal(t, "someValue", ts[0][0].Meta["someTraceTag"])
		assert.Equal(t, 1.0, ts[0][0].Metrics[keySamplingPriority])
		assert.Empty(t, ts[0][1].Meta["someTraceTag"])              // the tag should only be on the first span in the chunk
		assert.Equal(t, 1.0, ts[0][1].Metrics[keySamplingPriority]) // the tag should only be on the first span in the chunk
		comparePayloadSpans(t, root.(*span), tsRoot[0][0])
		comparePayloadSpans(t, children[2], tsRoot[0][1])
		telemetryClient.AssertNumberOfCalls(t, "Count", 1)
		// TODO: (Support MetricKindDist) Re-enable this when we actually support `MetricKindDist`
		// telemetryClient.AssertNumberOfCalls(t, "Record", 2)
	})

	// This test covers an issue where partial flushing + a rate sampler would panic
	t.Run("WithRateSamplerNoPanic", func(t *testing.T) {
		tracer, _, _, stop := startTestTracer(t, WithSampler(NewRateSampler(0.000001)))
		defer stop()

		root := tracer.StartSpan("root")
		root.(*span).context.trace.setTag("someTraceTag", "someValue")
		var children []*span
		for i := 0; i < 10; i++ { // create 10 child spans to ensure some aren't sampled
			child := tracer.StartSpan(fmt.Sprintf("child%d", i), ChildOf(root.Context()))
			children = append(children, child.(*span))
			child.Finish()
		}
	})

}

func TestSpanTracePushNoFinish(t *testing.T) {
	defer setupteardown(2, 5)()

	assert := assert.New(t)

	tp := new(log.RecordLogger)
	tp.Ignore("appsec: ", telemetry.LogPrefix)
	_, _, _, stop := startTestTracer(t, WithLogger(tp), WithLambdaMode(true))
	defer stop()

	buffer := newTrace()
	assert.NotNil(buffer)
	assert.Len(buffer.spans, 0)

	traceID := randUint64()
	root := newSpan("name1", "a-service", "a-resource", traceID, traceID, 0)
	root.context.trace = buffer

	buffer.push(root)
	assert.Len(buffer.spans, 1, "there is one span in the buffer")
	assert.Equal(root, buffer.spans[0], "the span is the one pushed before")

	<-time.After(time.Second / 10)
	log.Flush()
	assert.Len(tp.Logs(), 0)
	t.Logf("expected timeout, nothing should show up in buffer as the trace is not finished")
}

func TestSpanTracePushSeveral(t *testing.T) {
	defer setupteardown(2, 5)()

	assert := assert.New(t)

	trc, transport, flush, stop := startTestTracer(t)
	defer stop()
	buffer := newTrace()
	assert.NotNil(buffer)
	assert.Len(buffer.spans, 0)

	traceID := randUint64()
	root := trc.StartSpan("name1", WithSpanID(traceID))
	span2 := trc.StartSpan("name2", ChildOf(root.Context()))
	span3 := trc.StartSpan("name3", ChildOf(root.Context()))
	span3a := trc.StartSpan("name3", ChildOf(span3.Context()))

	trace := []*span{root.(*span), span2.(*span), span3.(*span), span3a.(*span)}

	for i, span := range trace {
		span.context.trace = buffer
		buffer.push(span)
		assert.Len(buffer.spans, i+1, "there is one more span in the buffer")
		assert.Equal(span, buffer.spans[i], "the span is the one pushed before")
	}

	for _, span := range trace {
		span.Finish()
	}
	flush(1)

	traces := transport.Traces()
	assert.Len(traces, 1)
	trace = traces[0]
	assert.Len(trace, 4, "there was one trace with the right number of spans in the channel")
	for _, span := range trace {
		assert.Contains(trace, span, "the trace contains the spans")
	}
}

// TestSpanFinishPriority asserts that the root span will have the sampling
// priority metric set by inheriting it from a child.
func TestSpanFinishPriority(t *testing.T) {
	assert := assert.New(t)
	tracer, transport, flush, stop := startTestTracer(t)
	defer stop()

	root := tracer.StartSpan(
		"root",
		Tag(ext.SamplingPriority, 1),
	)
	child := tracer.StartSpan(
		"child",
		ChildOf(root.Context()),
		Tag(ext.SamplingPriority, 2),
	)
	child.Finish()
	root.Finish()

	flush(1)

	traces := transport.Traces()
	assert.Len(traces, 1)
	trace := traces[0]
	assert.Len(trace, 2)
	for _, span := range trace {
		if span.Name == "root" {
			// root should have inherited child's sampling priority
			assert.Equal(span.Metrics[keySamplingPriority], 2.)
			return
		}
	}
	assert.Fail("span not found")
}

func TestSpanPeerService(t *testing.T) {
	testCases := []struct {
		name                        string
		spanOpts                    []StartSpanOption
		peerServiceDefaultsEnabled  bool
		peerServiceMappings         map[string]string
		wantPeerService             string
		wantPeerServiceSource       string
		wantPeerServiceRemappedFrom string
	}{
		{
			name: "PeerServiceSet",
			spanOpts: []StartSpanOption{
				Tag("span.kind", "client"),
				Tag("peer.service", "peer-service"),
			},
			peerServiceDefaultsEnabled:  true,
			peerServiceMappings:         nil,
			wantPeerService:             "peer-service",
			wantPeerServiceSource:       "peer.service",
			wantPeerServiceRemappedFrom: "",
		},
		{
			name: "PeerServiceSetSpanKindInternal",
			spanOpts: []StartSpanOption{
				Tag("span.kind", "internal"),
				Tag("peer.service", "peer-service-asdkjaskjdajsk"),
			},
			peerServiceDefaultsEnabled:  true,
			peerServiceMappings:         nil,
			wantPeerService:             "peer-service-asdkjaskjdajsk",
			wantPeerServiceSource:       "peer.service",
			wantPeerServiceRemappedFrom: "",
		},
		{
			name: "NotAnOutboundRequestSpan",
			spanOpts: []StartSpanOption{
				Tag("span.kind", "internal"),
			},
			peerServiceDefaultsEnabled:  true,
			peerServiceMappings:         nil,
			wantPeerService:             "",
			wantPeerServiceSource:       "",
			wantPeerServiceRemappedFrom: "",
		},
		{
			name: "AWS",
			spanOpts: []StartSpanOption{
				Tag("span.kind", "client"),
				Tag("aws_service", "S3"),
				Tag("bucketname", "some-bucket"),
				Tag("db.system", "db-system"),
				Tag("db.name", "db-name"),
			},
			peerServiceDefaultsEnabled:  true,
			peerServiceMappings:         nil,
			wantPeerService:             "some-bucket",
			wantPeerServiceSource:       "bucketname",
			wantPeerServiceRemappedFrom: "",
		},
		{
			name: "DBClient",
			spanOpts: []StartSpanOption{
				Tag("span.kind", "client"),
				Tag("db.system", "some-db"),
				Tag("db.instance", "db-instance"),
			},
			peerServiceDefaultsEnabled:  true,
			peerServiceMappings:         nil,
			wantPeerService:             "db-instance",
			wantPeerServiceSource:       "db.instance",
			wantPeerServiceRemappedFrom: "",
		},
		{
			name: "DBClientDefaultsDisabled",
			spanOpts: []StartSpanOption{
				Tag("span.kind", "client"),
				Tag("db.system", "some-db"),
				Tag("db.instance", "db-instance"),
			},
			peerServiceDefaultsEnabled:  false,
			peerServiceMappings:         nil,
			wantPeerService:             "",
			wantPeerServiceSource:       "",
			wantPeerServiceRemappedFrom: "",
		},
		{
			name: "DBCassandra",
			spanOpts: []StartSpanOption{
				Tag("span.kind", "client"),
				Tag("db.system", "cassandra"),
				Tag("db.instance", "db-instance"),
				Tag("db.cassandra.contact.points", "h1,h2,h3"),
				Tag("out.host", "out-host"),
			},
			peerServiceDefaultsEnabled:  true,
			peerServiceMappings:         nil,
			wantPeerService:             "h1,h2,h3",
			wantPeerServiceSource:       "db.cassandra.contact.points",
			wantPeerServiceRemappedFrom: "",
		},
		{
			name: "DBCassandraWithoutContactPoints",
			spanOpts: []StartSpanOption{
				Tag("span.kind", "client"),
				Tag("db.system", "cassandra"),
				Tag("db.instance", "db-instance"),
				Tag("out.host", "out-host"),
			},
			peerServiceDefaultsEnabled:  true,
			peerServiceMappings:         nil,
			wantPeerService:             "",
			wantPeerServiceSource:       "",
			wantPeerServiceRemappedFrom: "",
		},
		{
			name: "GRPCClient",
			spanOpts: []StartSpanOption{
				Tag("span.kind", "client"),
				Tag("rpc.system", "grpc"),
				Tag("rpc.service", "rpc-service"),
				Tag("out.host", "out-host"),
			},
			peerServiceDefaultsEnabled:  true,
			peerServiceMappings:         nil,
			wantPeerService:             "rpc-service",
			wantPeerServiceSource:       "rpc.service",
			wantPeerServiceRemappedFrom: "",
		},
		{
			name: "OtherClients",
			spanOpts: []StartSpanOption{
				Tag("span.kind", "client"),
				Tag("out.host", "out-host"),
			},
			peerServiceDefaultsEnabled:  true,
			peerServiceMappings:         nil,
			wantPeerService:             "out-host",
			wantPeerServiceSource:       "out.host",
			wantPeerServiceRemappedFrom: "",
		},
		{
			name: "WithMapping",
			spanOpts: []StartSpanOption{
				Tag("span.kind", "client"),
				Tag("out.host", "out-host"),
			},
			peerServiceDefaultsEnabled: true,
			peerServiceMappings: map[string]string{
				"out-host": "remapped-out-host",
			},
			wantPeerService:             "remapped-out-host",
			wantPeerServiceSource:       "out.host",
			wantPeerServiceRemappedFrom: "out-host",
		},
		{
			// in this case we skip defaults calculation but track the source and run the remapping.
			name: "WithoutSpanKindAndPeerService",
			spanOpts: []StartSpanOption{
				Tag("peer.service", "peer-service"),
			},
			peerServiceDefaultsEnabled: false,
			peerServiceMappings: map[string]string{
				"peer-service": "remapped-peer-service",
			},
			wantPeerService:             "remapped-peer-service",
			wantPeerServiceSource:       "peer.service",
			wantPeerServiceRemappedFrom: "peer-service",
		},
	}
	for _, tc := range testCases {
		assertSpan := func(t *testing.T, s *span) {
			if tc.wantPeerService == "" {
				assert.NotContains(t, s.Meta, "peer.service")
			} else {
				assert.Equal(t, tc.wantPeerService, s.Meta["peer.service"])
			}
			if tc.wantPeerServiceSource == "" {
				assert.NotContains(t, s.Meta, "_dd.peer.service.source")
			} else {
				assert.Equal(t, tc.wantPeerServiceSource, s.Meta["_dd.peer.service.source"])
			}
			if tc.wantPeerServiceRemappedFrom == "" {
				assert.NotContains(t, s.Meta, "_dd.peer.service.remapped_from")
			} else {
				assert.Equal(t, tc.wantPeerServiceRemappedFrom, s.Meta["_dd.peer.service.remapped_from"])
			}
		}
		t.Run(tc.name, func(t *testing.T) {
			tracer, transport, flush, stop := startTestTracer(t)
			defer stop()

			tracer.config.peerServiceDefaultsEnabled = tc.peerServiceDefaultsEnabled
			tracer.config.peerServiceMappings = tc.peerServiceMappings

			p := tracer.StartSpan("parent-span", tc.spanOpts...)
			opts := append([]StartSpanOption{ChildOf(p.Context())}, tc.spanOpts...)
			s := tracer.StartSpan("child-span", opts...)
			s.Finish()
			p.Finish()

			flush(1)
			traces := transport.Traces()
			require.Len(t, traces, 1)
			require.Len(t, traces[0], 2)

			t.Run("ParentSpan", func(t *testing.T) {
				assertSpan(t, traces[0][0])
			})
			t.Run("ChildSpan", func(t *testing.T) {
				assertSpan(t, traces[0][1])
			})
		})
	}
}

func TestSpanDDBaseService(t *testing.T) {
	run := func(t *testing.T, tracerOpts []StartOption, spanOpts []StartSpanOption) []*span {
		prevSvc := globalconfig.ServiceName()
		t.Cleanup(func() { globalconfig.SetServiceName(prevSvc) })

		tracer, transport, flush, stop := startTestTracer(t, tracerOpts...)
		t.Cleanup(stop)

		p := tracer.StartSpan("parent-span", spanOpts...)
		childSpanOpts := append([]StartSpanOption{ChildOf(p.Context())}, spanOpts...)
		s := tracer.StartSpan("child-span", childSpanOpts...)
		s.Finish()
		p.Finish()

		flush(1)
		traces := transport.Traces()
		require.Len(t, traces, 1)
		require.Len(t, traces[0], 2)

		return traces[0]
	}
	t.Run("span-service-not-equal-global-service", func(t *testing.T) {
		tracerOpts := []StartOption{
			WithService("global-service"),
		}
		spanOpts := []StartSpanOption{
			ServiceName("span-service"),
		}
		spans := run(t, tracerOpts, spanOpts)
		for _, s := range spans {
			assert.Equal(t, "span-service", s.Service)
			assert.Equal(t, "global-service", s.Meta["_dd.base_service"])
		}
	})
	t.Run("span-service-equal-global-service", func(t *testing.T) {
		tracerOpts := []StartOption{
			WithService("global-service"),
		}
		spanOpts := []StartSpanOption{
			ServiceName("global-service"),
		}
		spans := run(t, tracerOpts, spanOpts)
		for _, s := range spans {
			assert.Equal(t, "global-service", s.Service)
			assert.NotContains(t, s.Meta, "_dd.base_service")
		}
	})
	t.Run("span-service-equal-different-case", func(t *testing.T) {
		tracerOpts := []StartOption{
			WithService("global-service"),
		}
		spanOpts := []StartSpanOption{
			ServiceName("GLOBAL-service"),
		}
		spans := run(t, tracerOpts, spanOpts)
		for _, s := range spans {
			assert.Equal(t, "GLOBAL-service", s.Service)
			assert.NotContains(t, s.Meta, "_dd.base_service")
		}
	})
	t.Run("global-service-not-set", func(t *testing.T) {
		spanOpts := []StartSpanOption{
			ServiceName("span-service"),
		}
		spans := run(t, nil, spanOpts)
		for _, s := range spans {
			assert.Equal(t, "span-service", s.Service)
			// in this case we don't assert to a concrete value because the default tracer service name is calculated
			// based on the process name and might change depending on how tests are run.
			assert.NotEmpty(t, s.Meta["_dd.base_service"])
		}
	})
	t.Run("using-tag-option", func(t *testing.T) {
		tracerOpts := []StartOption{
			WithService("global-service"),
		}
		spanOpts := []StartSpanOption{
			Tag("service.name", "span-service"),
		}
		spans := run(t, tracerOpts, spanOpts)
		for _, s := range spans {
			assert.Equal(t, "span-service", s.Service)
			assert.Equal(t, "global-service", s.Meta["_dd.base_service"])
		}
	})
}

func TestNewSpanContext(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		span := &span{
			TraceID:  1,
			SpanID:   2,
			ParentID: 3,
		}
		ctx := newSpanContext(span, nil)
		assert := assert.New(t)
		assert.Equal(ctx.traceID.Lower(), span.TraceID)
		assert.Equal(ctx.spanID, span.SpanID)
		assert.NotNil(ctx.trace)
		assert.Nil(ctx.trace.priority)
		assert.Equal(ctx.trace.root, span)
		assert.Contains(ctx.trace.spans, span)
	})

	t.Run("priority", func(t *testing.T) {
		span := &span{
			TraceID:  1,
			SpanID:   2,
			ParentID: 3,
			Metrics:  map[string]float64{keySamplingPriority: 1},
		}
		ctx := newSpanContext(span, nil)
		assert := assert.New(t)
		assert.Equal(ctx.traceID.Lower(), span.TraceID)
		assert.Equal(ctx.spanID, span.SpanID)
		assert.Equal(ctx.TraceID(), span.TraceID)
		assert.Equal(ctx.SpanID(), span.SpanID)
		assert.Equal(*ctx.trace.priority, 1.)
		assert.Equal(ctx.trace.root, span)
		assert.Contains(ctx.trace.spans, span)
	})

	t.Run("root", func(t *testing.T) {
		t.Setenv(headerPropagationStyleExtract, "datadog")
		_, _, _, stop := startTestTracer(t)
		defer stop()
		assert := assert.New(t)
		ctx, err := NewPropagator(nil).Extract(TextMapCarrier(map[string]string{
			DefaultTraceIDHeader:  "1",
			DefaultParentIDHeader: "2",
			DefaultPriorityHeader: "3",
		}))
		assert.Nil(err)
		sctx, ok := ctx.(*spanContext)
		assert.True(ok)
		span := StartSpan("some-span", ChildOf(ctx))
		assert.EqualValues(uint64(1), sctx.traceID.Lower())
		assert.EqualValues(2, sctx.spanID)
		assert.EqualValues(3, *sctx.trace.priority)
		assert.Equal(sctx.trace.root, span)
	})
}

func TestSpanContextParent(t *testing.T) {
	s := &span{
		TraceID:  1,
		SpanID:   2,
		ParentID: 3,
	}
	for name, parentCtx := range map[string]*spanContext{
		"basic": {
			baggage:    map[string]string{"A": "A", "B": "B"},
			hasBaggage: 1,
			trace:      newTrace(),
		},
		"nil-trace": {},
		"priority": {
			baggage:    map[string]string{"A": "A", "B": "B"},
			hasBaggage: 1,
			trace: &trace{
				spans:    []*span{newBasicSpan("abc")},
				priority: func() *float64 { v := new(float64); *v = 2; return v }(),
			},
		},
		"sampling_decision": {
			baggage:    map[string]string{"A": "A", "B": "B"},
			hasBaggage: 1,
			trace: &trace{
				spans:            []*span{newBasicSpan("abc")},
				samplingDecision: decisionKeep,
			},
		},
		"origin": {
			trace:  &trace{spans: []*span{newBasicSpan("abc")}},
			origin: "synthetics",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := newSpanContext(s, parentCtx)
			assert := assert.New(t)
			assert.Equal(ctx.traceID.Lower(), s.TraceID)
			assert.Equal(ctx.spanID, s.SpanID)
			if parentCtx.trace != nil {
				assert.Equal(len(ctx.trace.spans), len(parentCtx.trace.spans))
			}
			assert.NotNil(ctx.trace)
			assert.Contains(ctx.trace.spans, s)
			if parentCtx.trace != nil {
				assert.Equal(ctx.trace.priority, parentCtx.trace.priority)
				assert.Equal(ctx.trace.samplingDecision, parentCtx.trace.samplingDecision)
			}
			assert.Equal(parentCtx.baggage, ctx.baggage)
			assert.Equal(parentCtx.origin, ctx.origin)
		})
	}
}

func TestSpanContextPushFull(t *testing.T) {
	defer func(old int) { traceMaxSize = old }(traceMaxSize)
	traceMaxSize = 2
	tp := new(log.RecordLogger)
	tp.Ignore("appsec: ", telemetry.LogPrefix)
	_, _, _, stop := startTestTracer(t, WithLogger(tp), WithLambdaMode(true))
	defer stop()

	span1 := newBasicSpan("span1")
	span2 := newBasicSpan("span2")
	span3 := newBasicSpan("span3")

	buffer := newTrace()
	assert := assert.New(t)
	buffer.push(span1)
	log.Flush()
	assert.Len(tp.Logs(), 0)
	buffer.push(span2)
	log.Flush()
	assert.Len(tp.Logs(), 0)
	buffer.push(span3)
	log.Flush()
	assert.Contains(tp.Logs()[0], "ERROR: trace buffer full (2)")
}

func TestSpanContextBaggage(t *testing.T) {
	assert := assert.New(t)

	var ctx spanContext
	ctx.setBaggageItem("key", "value")
	assert.Equal("value", ctx.baggage["key"])
}

func TestSpanContextIterator(t *testing.T) {
	assert := assert.New(t)

	got := make(map[string]string)
	ctx := spanContext{baggage: map[string]string{"key": "value"}, hasBaggage: 1}
	ctx.ForeachBaggageItem(func(k, v string) bool {
		got[k] = v
		return true
	})

	assert.Len(got, 1)
	assert.Equal("value", got["key"])
}

func TestSpanContextIteratorBreak(t *testing.T) {
	got := make(map[string]string)
	ctx := spanContext{baggage: map[string]string{"key": "value"}}
	ctx.ForeachBaggageItem(func(k, v string) bool {
		return false
	})

	assert.Len(t, got, 0)
}

func BenchmarkBaggageItemPresent(b *testing.B) {
	ctx := spanContext{baggage: map[string]string{"key": "value"}, hasBaggage: 1}
	for n := 0; n < b.N; n++ {
		ctx.ForeachBaggageItem(func(k, v string) bool {
			return true
		})
	}
}

func BenchmarkBaggageItemEmpty(b *testing.B) {
	ctx := spanContext{}
	for n := 0; n < b.N; n++ {
		ctx.ForeachBaggageItem(func(k, v string) bool {
			return true
		})
	}
}

func TestSetSamplingPriorityLocked(t *testing.T) {
	t.Run("NoPriorAndP0IsIgnored", func(t *testing.T) {
		tr := trace{
			propagatingTags: map[string]string{},
		}
		tr.setSamplingPriorityLocked(0, samplernames.RemoteRate)
		assert.Empty(t, tr.propagatingTags[keyDecisionMaker])
	})
	t.Run("UnknownSamplerIsIgnored", func(t *testing.T) {
		tr := trace{
			propagatingTags: map[string]string{},
		}
		tr.setSamplingPriorityLocked(0, samplernames.Unknown)
		assert.Empty(t, tr.propagatingTags[keyDecisionMaker])
	})
	t.Run("NoPriorAndP1IsAccepted", func(t *testing.T) {
		tr := trace{
			propagatingTags: map[string]string{},
		}
		tr.setSamplingPriorityLocked(1, samplernames.RemoteRate)
		assert.Equal(t, "-2", tr.propagatingTags[keyDecisionMaker])
	})
	t.Run("PriorAndP1AndSameDMIsIgnored", func(t *testing.T) {
		tr := trace{
			propagatingTags: map[string]string{keyDecisionMaker: "-1"},
		}
		tr.setSamplingPriorityLocked(1, samplernames.AgentRate)
		assert.Equal(t, "-1", tr.propagatingTags[keyDecisionMaker])
	})
	t.Run("PriorAndP1DifferentDMAccepted", func(t *testing.T) {
		tr := trace{
			propagatingTags: map[string]string{keyDecisionMaker: "-1"},
		}
		tr.setSamplingPriorityLocked(1, samplernames.RemoteRate)
		assert.Equal(t, "-2", tr.propagatingTags[keyDecisionMaker])
	})
}

func TestTraceIDHexEncoded(t *testing.T) {
	tid := traceID([16]byte{})
	tid[15] = 5
	assert.Equal(t, "00000000000000000000000000000005", tid.HexEncoded())
}

func TestTraceIDEmpty(t *testing.T) {
	tid := traceID([16]byte{})
	tid[15] = 5
	assert.False(t, tid.Empty())
}

func TestSpanIDHexEncoded(t *testing.T) {
	sid := spanIDHexEncoded(5, 16)
	assert.Equal(t, fmt.Sprintf("%016x", 5), sid)

	sid = spanIDHexEncoded(5, 32)
	assert.Equal(t, fmt.Sprintf("%032x", 5), sid)

	sid = spanIDHexEncoded(math.MaxInt64, 68)
	assert.Equal(t, fmt.Sprintf("%068x", math.MaxInt64), sid)

	sid = spanIDHexEncoded(math.MaxInt64, 128)
	assert.Equal(t, fmt.Sprintf("%0128x", math.MaxInt64), sid)

	sid = spanIDHexEncoded(math.MaxUint64, -16)
	assert.Equal(t, "ffffffffffffffff", sid)
	assert.Equal(t, spanIDHexEncoded(math.MaxUint64, 0), sid)
	assert.Equal(t, spanIDHexEncoded(math.MaxUint64, 16), sid)
}

func BenchmarkSpanIDHexEncoded(b *testing.B) {
	for n := 0; n < b.N; n++ {
		_ = spanIDHexEncoded(32, 16)
	}
}

func BenchmarkSpanIDSprintf(b *testing.B) {
	for n := 0; n < b.N; n++ {
		_ = fmt.Sprintf("%016x", 32)
	}
}

func FuzzSpanIDHexEncoded(f *testing.F) {
	f.Add(-99, uint64(0))
	f.Add(16, uint64(1))
	f.Add(32, uint64(16))
	f.Add(0, uint64(math.MaxUint64))
	f.Fuzz(func(t *testing.T, p int, v uint64) {
		// We don't support negative padding nor right-padding.
		if p < 0 {
			return
		}
		expected := fmt.Sprintf(
			fmt.Sprintf("%%0%dx", p),
			v,
		)
		actual := spanIDHexEncoded(v, p)
		if actual != expected {
			t.Fatalf("expected %s, got %s", expected, actual)
		}
	})
}
