// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/processtags"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupteardown(startSize, maxSize int) func() {
	oldStartSize := traceStartSize
	oldMaxSize := traceMaxSize
	traceStartSize = startSize
	traceMaxSize = maxSize
	return func() {
		traceStartSize = oldStartSize
		traceMaxSize = oldMaxSize
	}
}

func TestTraceIDZero(t *testing.T) {
	c := SpanContext{}
	assert.Equal(t, c.TraceID(), TraceIDZero)
}

func TestNewSpanContextPushError(t *testing.T) {
	defer setupteardown(2, 2)()

	tp := new(log.RecordLogger)
	tp.Ignore("appsec: ", "telemetry")
	_, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithLambdaMode(true), WithEnv("testEnv"))
	assert.Nil(t, err)
	defer stop()
	parent := newBasicSpan("test1")                  // 1st span in trace
	parent.context.trace.push(newBasicSpan("test2")) // 2nd span in trace
	child := newSpan("child", "", "", 0, 0, 0)

	// new context having a parent with a trace of two spans.
	// One more should overflow.
	child.context = newSpanContext(child, parent.context)

	log.Flush()
	assert.Contains(t, tp.Logs()[0], "ERROR: trace buffer full (2 spans)")
}

/*
This test is an attempt to reproduce one of the panics from incident 37240 [1].
Run the test with -count 100, and you should get a crash like the one shown
below.

[1] https://dd.slack.com/archives/C08NGNZR0C8/p1744390495200339

	fatal error: concurrent map iteration and map write

	goroutine 354 [running]:
	internal/runtime/maps.fatal({0x102db4b48?, 0x14000101d98?})
			/Users/felix.geisendoerfer/.local/share/mise/installs/go/1.24.2/src/runtime/panic.go:1058 +0x20
	internal/runtime/maps.(*Iter).Next(0x1400009dca0?)
			/Users/felix.geisendoerfer/.local/share/mise/installs/go/1.24.2/src/internal/runtime/maps/table.go:683 +0x94
	gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.(*span).EncodeMsg(0x14000140140, 0x14000c1c000)
			/Users/felix.geisendoerfer/go/src/github.com/DataDog/dd-trace-go/ddtrace/tracer/span_msgp.go:392 +0x2e8
	gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.spanList.EncodeMsg({0x140003e0320, 0x1, 0x18?}, 0x14000c1c000)
			/Users/felix.geisendoerfer/go/src/github.com/DataDog/dd-trace-go/ddtrace/tracer/span_msgp.go:596 +0x80
	github.com/tinylib/msgp/msgp.Encode({0x1031ebd00?, 0x14000294d48?}, {0x1031ec060?, 0x1400000e090?})
			/Users/felix.geisendoerfer/go/pkg/mod/github.com/tinylib/msgp@v1.2.5/msgp/write.go:156 +0x60
	gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.(*payload).push(0x14000294d20, {0x140003e0320, 0x1, 0xa})
			/Users/felix.geisendoerfer/go/src/github.com/DataDog/dd-trace-go/ddtrace/tracer/payload.go:76 +0x98
	gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.(*agentTraceWriter).add(0x140003e0000, {0x140003e0320?, 0x0?, 0x0?})
			/Users/felix.geisendoerfer/go/src/github.com/DataDog/dd-trace-go/ddtrace/tracer/writer.go:69 +0x28
	gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.(*tracer).worker(0x140000c40d0, 0x1400012b2d0)
			/Users/felix.geisendoerfer/go/src/github.com/DataDog/dd-trace-go/ddtrace/tracer/tracer.go:457 +0x154
	gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.newTracer.func2()
			/Users/felix.geisendoerfer/go/src/github.com/DataDog/dd-trace-go/ddtrace/tracer/tracer.go:412 +0xa8
	created by gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer.newTracer in goroutine 326
			/Users/felix.geisendoerfer/go/src/github.com/DataDog/dd-trace-go/ddtrace/tracer/tracer.go:404 +0x2b4
*/
func TestIncident37240DoubleFinish(t *testing.T) {
	t.Run("with link", func(_ *testing.T) {
		_, _, _, stop, err := startTestTracer(t)
		assert.Nil(t, err)
		defer stop()

		root, _ := StartSpanFromContext(context.Background(), "root", Tag(ext.ManualKeep, true))
		// My theory is that contrib/aws/internal/span_pointers/span_pointers.go
		// adds a span link which is causes `serializeSpanLinksInMeta` to write to
		// `s.Meta` without holding the lock. This crashes when the span flushing
		// code tries to read `s.Meta` without holding the lock.
		root.AddLink(SpanLink{TraceID: 1, SpanID: 2})
		for i := 0; i < 1000; i++ {
			root.Finish()
		}
	})

	t.Run("with NoDebugStack", func(_ *testing.T) {
		_, _, _, stop, err := startTestTracer(t)
		assert.Nil(t, err)
		defer stop()

		root, _ := StartSpanFromContext(context.Background(), "root", Tag(ext.ManualKeep, true))
		for i := 0; i < 1000; i++ {
			root.Finish(NoDebugStack())
		}
	})

	t.Run("with error", func(_ *testing.T) {
		_, _, _, stop, err := startTestTracer(t)
		assert.Nil(t, err)
		defer stop()

		root, _ := StartSpanFromContext(context.Background(), "root", Tag(ext.ManualKeep, true))
		err = errors.New("test error")
		for i := 0; i < 1000; i++ {
			root.Finish(WithError(err))
		}
	})

	t.Run("with rules sampler", func(t *testing.T) {
		_, _, _, stop, err := startTestTracer(t,
			WithService("svc"),
			WithSamplingRules(TraceSamplingRules(Rule{ServiceGlob: "svc", Rate: 1.0})),
		)
		assert.Nil(t, err)
		defer stop()

		root, _ := StartSpanFromContext(context.Background(), "root")
		for i := 0; i < 1000; i++ {
			root.Finish(WithError(err))
			assert.Equal(t, 1.0, root.metrics[keyRulesSamplerLimiterRate])
			assert.Equal(t, 2.0, root.metrics[keySamplingPriority])
			assert.Empty(t, root.metrics[keySamplingPriorityRate])
		}
	})
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
	_, _, _, stop, err := startTestTracer(t)
	assert.Nil(t, err)
	defer stop()

	for i := 0; i < 100; i++ {
		// The test has 100 iterations because it is not easy to reproduce the race.
		t.Run("", func(_ *testing.T) {
			var (
				wg, finishes sync.WaitGroup
				done         = make(chan struct{})
			)
			root, ctx := StartSpanFromContext(context.Background(), "root", Tag(ext.ManualKeep, true))
			finishes.Add(2)
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 500; i++ {
					// Spamming Finish to emulate concurrent Finish calls.
					root.Finish()
				}

				// Syncing the finishes to ensure the rest of the test is executed after the Finish calls.
				finishes.Done()
				finishes.Wait()

				// Closing will attempt trigger the goroutines at approximately the same time.
				// Closing it in the test function will cause a data race that doesn't happen in the wild.
				// Here it's simulating the real Finish flow, as the meta/metrics iteration happen after
				// the span is pushed to the tracer's t.out channel.
				close(done)

				for i := 0; i < 500; i++ {
					for range root.metrics {
						// this range simulates iterating over the metrics map
						// as we do when encoding msgpack upon flushing.
						continue
					}
				}
			}()
			wg.Add(1)
			go func() {
				defer wg.Done()
				for i := 0; i < 500; i++ {
					// Spamming Finish to emulate concurrent Finish calls.
					root.Finish()
				}

				finishes.Done()
				finishes.Wait()

				for i := 0; i < 500; i++ {
					for range root.meta {
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
					child, _ := StartSpanFromContext(ctx, "child", Tag(ext.ManualKeep, true))
					child.Finish()
				}
			}()
			wg.Wait()
		})
	}

	// Test passes if no panic occurs while running.
}

func TestSpanTracePushOne(t *testing.T) {
	defer setupteardown(2, 5)()

	assert := assert.New(t)

	tracer, transport, flush, stop, err := startTestTracer(t)
	assert.Nil(err)
	defer stop()

	root := tracer.newRootSpan("name1", "a-service", "a-resource")
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

// Tests to confirm that when the payload queue is full, chunks are dropped
// and the associated trace is counted as dropped.
func TestTraceFinishChunk(t *testing.T) {
	assert := assert.New(t)
	tracer, err := newUnstartedTracer()
	assert.Nil(err)
	defer tracer.statsd.Close()

	root := newSpan("name", "service", "resource", 0, 0, 0)
	trace := root.context.trace

	for i := 0; i < payloadQueueSize+1; i++ {
		trace.mu.Lock()
		c := chunk{spans: make([]*Span, 1)}
		trace.finishChunk(tracer, &c)
		trace.mu.Unlock()
	}
	assert.Equal(uint32(1), tracer.totalTracesDropped)
}

func TestPartialFlush(t *testing.T) {
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "2")
	t.Run("WithFlush", func(t *testing.T) {
		telemetryClient := new(telemetrytest.RecordClient)
		telemetryClient.ProductStarted(telemetry.NamespaceTracers)
		defer telemetry.MockClient(telemetryClient)()
		tracer, transport, flush, stop, err := startTestTracer(t)
		assert.Nil(t, err)
		defer stop()

		root := tracer.StartSpan("root")
		root.context.trace.setTag("someTraceTag", "someValue")
		var children []*Span
		for i := 0; i < 3; i++ { // create 3 child spans
			child := tracer.StartSpan(fmt.Sprintf("child%d", i), ChildOf(root.Context()))
			children = append(children, child)
			child.Finish()
		}
		flush(1)

		ts := transport.Traces()
		require.Len(t, ts, 1)
		require.Len(t, ts[0], 2)
		assert.Equal(t, "someValue", ts[0][0].meta["someTraceTag"])
		assert.Equal(t, 1.0, ts[0][0].metrics[keySamplingPriority])
		assert.Empty(t, ts[0][1].meta["someTraceTag"])              // the tag should only be on the first span in the chunk
		assert.Equal(t, 1.0, ts[0][1].metrics[keySamplingPriority]) // the tag should only be on the first span in the chunk
		comparePayloadSpans(t, children[0], ts[0][0])
		comparePayloadSpans(t, children[1], ts[0][1])

		assert.Equal(t, 1.0, telemetryClient.Count(telemetry.NamespaceTracers, "trace_partial_flush.count", []string{"reason:large_trace"}).Get())
		assert.Equal(t, 2.0, telemetryClient.Distribution(telemetry.NamespaceTracers, "trace_partial_flush.spans_closed", nil).Get())
		assert.Equal(t, 1.0, telemetryClient.Distribution(telemetry.NamespaceTracers, "trace_partial_flush.spans_remaining", nil).Get())

		root.Finish()
		flush(1)
		tsRoot := transport.Traces()
		require.Len(t, tsRoot, 1)
		require.Len(t, tsRoot[0], 2)
		assert.Equal(t, "someValue", ts[0][0].meta["someTraceTag"])
		assert.Equal(t, 1.0, ts[0][0].metrics[keySamplingPriority])
		assert.Empty(t, ts[0][1].meta["someTraceTag"])              // the tag should only be on the first span in the chunk
		assert.Equal(t, 1.0, ts[0][1].metrics[keySamplingPriority]) // the tag should only be on the first span in the chunk
		comparePayloadSpans(t, root, tsRoot[0][0])
		comparePayloadSpans(t, children[2], tsRoot[0][1])
	})

	// This test covers an issue where partial flushing + a rate sampler would panic
	t.Run("WithRateSamplerNoPanic", func(t *testing.T) {
		tracer, _, _, stop, err := startTestTracer(t, WithSamplerRate(0.000001))
		assert.Nil(t, err)
		defer stop()

		root := tracer.StartSpan("root")
		root.context.trace.setTag("someTraceTag", "someValue")
		for i := 0; i < 10; i++ { // create 10 child spans to ensure some aren't sampled
			child := tracer.StartSpan(fmt.Sprintf("child%d", i), ChildOf(root.Context()))
			child.Finish()
		}
	})

}

func TestSpanTracePushNoFinish(t *testing.T) {
	defer setupteardown(2, 5)()

	assert := assert.New(t)

	tp := new(log.RecordLogger)
	tp.Ignore("appsec: ", "telemetry")
	_, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithLambdaMode(true), WithEnv("testEnv"))
	assert.NoError(err)
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

	trc, transport, flush, stop, err := startTestTracer(t)
	assert.Nil(err)
	defer stop()
	buffer := newTrace()
	assert.NotNil(buffer)
	assert.Len(buffer.spans, 0)

	traceID := randUint64()
	root := trc.StartSpan("name1", WithSpanID(traceID))
	span2 := trc.StartSpan("name2", ChildOf(root.Context()))
	span3 := trc.StartSpan("name3", ChildOf(root.Context()))
	span3a := trc.StartSpan("name3", ChildOf(span3.Context()))

	trace := []*Span{root, span2, span3, span3a}

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
	tracer, transport, flush, stop, err := startTestTracer(t)
	assert.Nil(err)
	defer stop()

	root := tracer.StartSpan(
		"root",
	)
	root.setSamplingPriority(ext.PriorityAutoKeep, samplernames.Manual)
	child := tracer.StartSpan(
		"child",
		ChildOf(root.Context()),
		Tag(ext.ManualKeep, true),
	)
	child.Finish()
	root.Finish()

	flush(1)

	traces := transport.Traces()
	assert.Len(traces, 1)
	trace := traces[0]
	assert.Len(trace, 2)
	for _, span := range trace {
		if span.name == "root" {
			// root should have inherited child's sampling priority
			assert.Equal(span.metrics[keySamplingPriority], 2.)
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
		assertSpan := func(t *testing.T, s *Span) {
			if tc.wantPeerService == "" {
				assert.NotContains(t, s.meta, "peer.service")
			} else {
				assert.Equal(t, tc.wantPeerService, s.meta["peer.service"])
			}
			if tc.wantPeerServiceSource == "" {
				assert.NotContains(t, s.meta, "_dd.peer.service.source")
			} else {
				assert.Equal(t, tc.wantPeerServiceSource, s.meta["_dd.peer.service.source"])
			}
			if tc.wantPeerServiceRemappedFrom == "" {
				assert.NotContains(t, s.meta, "_dd.peer.service.remapped_from")
			} else {
				assert.Equal(t, tc.wantPeerServiceRemappedFrom, s.meta["_dd.peer.service.remapped_from"])
			}
		}
		t.Run(tc.name, func(t *testing.T) {
			tracer, transport, flush, stop, err := startTestTracer(t)
			assert.Nil(t, err)
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
	run := func(t *testing.T, tracerOpts []StartOption, spanOpts []StartSpanOption) []*Span {
		prevSvc := globalconfig.ServiceName()
		t.Cleanup(func() { globalconfig.SetServiceName(prevSvc) })

		tracer, transport, flush, stop, err := startTestTracer(t, tracerOpts...)
		assert.Nil(t, err)
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
			assert.Equal(t, "span-service", s.service)
			assert.Equal(t, "global-service", s.meta["_dd.base_service"])
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
			assert.Equal(t, "global-service", s.service)
			assert.NotContains(t, s.meta, "_dd.base_service")
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
			assert.Equal(t, "GLOBAL-service", s.service)
			assert.NotContains(t, s.meta, "_dd.base_service")
		}
	})
	t.Run("global-service-not-set", func(t *testing.T) {
		spanOpts := []StartSpanOption{
			ServiceName("span-service"),
		}
		spans := run(t, nil, spanOpts)
		for _, s := range spans {
			assert.Equal(t, "span-service", s.service)
			// in this case we don't assert to a concrete value because the default tracer service name is calculated
			// based on the process name and might change depending on how tests are run.
			assert.NotEmpty(t, s.meta["_dd.base_service"])
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
			assert.Equal(t, "span-service", s.service)
			assert.Equal(t, "global-service", s.meta["_dd.base_service"])
		}
	})
}

func TestNewSpanContext(t *testing.T) {
	t.Run("basic", func(t *testing.T) {
		span := &Span{
			traceID:  1,
			spanID:   2,
			parentID: 3,
		}
		ctx := newSpanContext(span, nil)
		assert := assert.New(t)
		assert.Equal(ctx.traceID.Lower(), span.traceID)
		assert.Equal(ctx.spanID, span.spanID)
		assert.NotNil(ctx.trace)
		assert.Nil(ctx.trace.priority)
		assert.Equal(ctx.trace.root, span)
		assert.Contains(ctx.trace.spans, span)
	})

	t.Run("priority", func(t *testing.T) {
		span := &Span{
			traceID:  1,
			spanID:   2,
			parentID: 3,
			metrics:  map[string]float64{keySamplingPriority: 1},
		}
		ctx := newSpanContext(span, nil)
		assert := assert.New(t)
		assert.Equal(ctx.traceID.Lower(), span.traceID)
		assert.Equal(ctx.spanID, span.spanID)
		assert.Equal(ctx.SpanID(), span.spanID)
		assert.Equal(*ctx.trace.priority, 1.)
		assert.Equal(ctx.trace.root, span)
		assert.Contains(ctx.trace.spans, span)
	})

	t.Run("root", func(t *testing.T) {
		t.Setenv(headerPropagationStyleExtract, "datadog")
		_, _, _, stop, err := startTestTracer(t)
		assert.Nil(t, err)
		defer stop()
		assert := assert.New(t)
		ctx, err := NewPropagator(nil).Extract(TextMapCarrier(map[string]string{
			DefaultTraceIDHeader:  "1",
			DefaultParentIDHeader: "2",
			DefaultPriorityHeader: "3",
		}))
		assert.Nil(err)
		span := StartSpan("some-span", ChildOf(ctx))
		assert.EqualValues(uint64(1), ctx.traceID.Lower())
		assert.EqualValues(2, ctx.spanID)
		assert.EqualValues(3, *ctx.trace.priority)
		assert.Equal(ctx.trace.root, span)
	})
}

func TestSpanContextParent(t *testing.T) {
	s := &Span{
		traceID:  1,
		spanID:   2,
		parentID: 3,
	}
	for name, parentCtx := range map[string]*SpanContext{
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
				spans:    []*Span{newBasicSpan("abc")},
				priority: func() *float64 { v := new(float64); *v = 2; return v }(),
			},
		},
		"sampling_decision": {
			baggage:    map[string]string{"A": "A", "B": "B"},
			hasBaggage: 1,
			trace: &trace{
				spans:            []*Span{newBasicSpan("abc")},
				samplingDecision: decisionKeep,
			},
		},
		"origin": {
			trace:  &trace{spans: []*Span{newBasicSpan("abc")}},
			origin: "synthetics",
		},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := newSpanContext(s, parentCtx)
			assert := assert.New(t)
			assert.Equal(ctx.traceID.Lower(), s.traceID)
			assert.Equal(ctx.spanID, s.spanID)
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
	tp.Ignore("appsec: ", "telemetry")
	_, _, _, stop, err := startTestTracer(t, WithLogger(tp), WithLambdaMode(true), WithEnv("testEnv"))
	assert.Nil(t, err)
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
	assert.Contains(tp.Logs()[0], "ERROR: trace buffer full (2 spans)")
}

func TestSpanContextBaggage(t *testing.T) {
	assert := assert.New(t)

	var ctx SpanContext
	ctx.setBaggageItem("key", "value")
	assert.Equal("value", ctx.baggage["key"])
}

func TestSpanContextIterator(t *testing.T) {
	assert := assert.New(t)

	got := make(map[string]string)
	ctx := SpanContext{baggage: map[string]string{"key": "value"}, hasBaggage: 1}
	ctx.ForeachBaggageItem(func(k, v string) bool {
		got[k] = v
		return true
	})

	assert.Len(got, 1)
	assert.Equal("value", got["key"])
}

func TestNilSpanContextIterator(t *testing.T) {
	got := make(map[string]string)
	var ctx *SpanContext
	ctx.ForeachBaggageItem(func(k, v string) bool {
		got[k] = v
		return true
	})

	assert.Len(t, got, 0)
}

func TestSpanContextIteratorBreak(t *testing.T) {
	got := make(map[string]string)
	ctx := SpanContext{baggage: map[string]string{"key": "value"}}
	ctx.ForeachBaggageItem(func(_, _ string) bool {
		return false
	})

	assert.Len(t, got, 0)
}

func BenchmarkBaggageItemPresent(b *testing.B) {
	ctx := SpanContext{baggage: map[string]string{"key": "value"}, hasBaggage: 1}
	for n := 0; n < b.N; n++ {
		ctx.ForeachBaggageItem(func(_, _ string) bool {
			return true
		})
	}
}

func BenchmarkBaggageItemEmpty(b *testing.B) {
	ctx := SpanContext{}
	for n := 0; n < b.N; n++ {
		ctx.ForeachBaggageItem(func(_, _ string) bool {
			return true
		})
	}
}

func TestSetSamplingPriorityLocked(t *testing.T) {
	t.Run("NoPriorAndP0IsIgnored", func(t *testing.T) {
		tr := trace{
			propagatingTags: map[string]string{},
		}
		tr.setSamplingPriorityLocked(ext.PriorityAutoReject, samplernames.RemoteRate)
		assert.Empty(t, tr.propagatingTags[keyDecisionMaker])
	})
	t.Run("UnknownSamplerIsIgnored", func(t *testing.T) {
		tr := trace{
			propagatingTags: map[string]string{},
		}
		tr.setSamplingPriorityLocked(ext.PriorityAutoReject, samplernames.Unknown)
		assert.Empty(t, tr.propagatingTags[keyDecisionMaker])
	})
	t.Run("NoPriorAndP1IsAccepted", func(t *testing.T) {
		tr := trace{
			propagatingTags: map[string]string{},
		}
		tr.setSamplingPriorityLocked(ext.PriorityAutoKeep, samplernames.RemoteRate)
		assert.Equal(t, "-2", tr.propagatingTags[keyDecisionMaker])
	})
	t.Run("PriorAndP1AndSameDMIsIgnored", func(t *testing.T) {
		tr := trace{
			propagatingTags: map[string]string{keyDecisionMaker: "-1"},
		}
		tr.setSamplingPriorityLocked(ext.PriorityAutoKeep, samplernames.AgentRate)
		assert.Equal(t, "-1", tr.propagatingTags[keyDecisionMaker])
	})
	t.Run("PriorAndP1DifferentDMAccepted", func(t *testing.T) {
		tr := trace{
			propagatingTags: map[string]string{keyDecisionMaker: "-1"},
		}
		tr.setSamplingPriorityLocked(ext.PriorityAutoKeep, samplernames.RemoteRate)
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

func TestSpanProcessTags(t *testing.T) {
	testCases := []struct {
		name    string
		enabled bool
	}{
		{
			name:    "disabled",
			enabled: false,
		},
		{
			name:    "enabled",
			enabled: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("DD_EXPERIMENTAL_PROPAGATE_PROCESS_TAGS_ENABLED", strconv.FormatBool(tc.enabled))
			processtags.Reload()
			tracer, transport, flush, stop, err := startTestTracer(t)
			assert.NoError(t, err)
			t.Cleanup(stop)

			p := tracer.StartSpan("p")
			c1 := p.StartChild("c1")
			c2 := p.StartChild("c2")
			c11 := c1.StartChild("c1-1")

			c11.Finish()
			c2.Finish()
			c1.Finish()
			p.Finish()

			flush(1)
			traces := transport.Traces()
			require.Len(t, traces, 1)
			require.Len(t, traces[0], 4)

			root := traces[0][0]
			assert.Equal(t, "p", root.name)
			if tc.enabled {
				assert.NotEmpty(t, root.meta["_dd.tags.process"])
			} else {
				assert.NotContains(t, root.meta, "_dd.tags.process")
			}

			for _, s := range traces[0][1:] {
				assert.NotContains(t, s.meta, "_dd.tags.process")
			}
		})
	}
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
