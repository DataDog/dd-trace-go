// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"sync"
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/samplernames"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

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
				select {
				case <-done:
					root.Finish()
					for i := 0; i < 500; i++ {
						for range root.(*span).Metrics {
							// this range simulates iterating over the metrics map
							// as we do when encoding msgpack upon flushing.
							continue
						}
					}
					return
				}
			}()
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case <-done:
					root.Finish()
					for i := 0; i < 500; i++ {
						for range root.(*span).Meta {
							// this range simulates iterating over the meta map
							// as we do when encoding msgpack upon flushing.
							continue
						}
					}
					return
				}
			}()
			wg.Add(1)
			go func() {
				defer wg.Done()
				select {
				case <-done:
					for i := 0; i < 50; i++ {
						// to trigger the bug, the child should be created after the root was finished,
						// as its being flushed
						child, _ := StartSpanFromContext(ctx, "child", Tag(ext.SamplingPriority, ext.PriorityUserKeep))
						child.Finish()
					}
					return
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

	traceID := random.Uint64()
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

	traceID := random.Uint64()
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

	traceID := random.Uint64()
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
	t.Run("PriorAndP1IsIgnored", func(t *testing.T) {
		tr := trace{
			propagatingTags: map[string]string{keyDecisionMaker: "-1"},
		}
		tr.setSamplingPriorityLocked(1, samplernames.RemoteRate)
		assert.Equal(t, "-1", tr.propagatingTags[keyDecisionMaker])
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
