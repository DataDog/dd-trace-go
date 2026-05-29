// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpanClearZeroesFields(t *testing.T) {
	s := newSpan("test.op", "test.svc", "/test", 1, 2, 3)
	s.spanType = "web"
	s.error = 1
	s.meta.Set("custom.key", "custom.val")
	s.metrics["custom.metric"] = 42.0
	s.spanLinks = []SpanLink{{TraceID: 99, SpanID: 88}}
	s.finished = true

	s.clear()

	// Serialized fields must be zeroed.
	assert.Equal(t, "", s.name)
	assert.Equal(t, "", s.service)
	assert.Equal(t, "", s.resource)
	assert.Equal(t, "", s.spanType)
	assert.Equal(t, int64(0), s.start)
	assert.Equal(t, int64(0), s.duration)
	assert.Equal(t, uint64(0), s.spanID)
	assert.Equal(t, uint64(0), s.traceID)
	assert.Equal(t, uint64(0), s.parentID)
	assert.Equal(t, int32(0), s.error)
	assert.False(t, s.finished)

	// Maps must be empty.
	require.NotNil(t, s.metrics)
	assert.True(t, s.meta.IsZero())
	assert.Empty(t, s.metrics)

	// Slices and pointer fields must be nil.
	assert.Nil(t, s.spanLinks)
	assert.Nil(t, s.spanEvents)

	// s.context is intentionally NOT cleared: Context() is lock-free and
	// external code may still read it after Finish(). spanStart reassigns
	// it on reuse.
}

func TestSpanPoolPayloadCorrectness(t *testing.T) {
	tracer, transport, flush, stop, err := startTestTracer(t, WithSpanPool(true))
	require.NoError(t, err)
	defer stop()

	// Use explicit Start/Finish times so duration is deterministic across
	// platforms with coarse clock resolution (e.g., Windows CI runners,
	// where back-to-back now() calls can return identical values).
	start := time.Now()
	span := tracer.StartSpan("test.op",
		StartTime(start),
		Tag(ext.ManualKeep, true),
		ServiceName("test.svc"),
		ResourceName("/test"),
		SpanType("web"),
		Tag("custom.key", "custom.val"),
		Tag("custom.metric", 1.5),
	)
	span.Finish(FinishTime(start.Add(time.Millisecond)))
	flush(1)

	traces := transport.Traces()
	require.Len(t, traces, 1)
	require.Len(t, traces[0], 1)
	s := traces[0][0]

	assert.Equal(t, "test.op", s.name)
	assert.Equal(t, "test.svc", s.service)
	assert.Equal(t, "/test", s.resource)
	assert.Equal(t, "web", s.spanType)
	v, _ := s.meta.Get("custom.key")
	assert.Equal(t, "custom.val", v)
	assert.Equal(t, 1.5, s.metrics["custom.metric"])
	assert.Equal(t, int32(0), s.error)
	assert.NotZero(t, s.spanID)
	assert.NotZero(t, s.traceID)
	assert.Equal(t, start.UnixNano(), s.start)
	assert.Equal(t, int64(time.Millisecond), s.duration)
}

func TestSpanPoolRecycledSpanNoStaleData(t *testing.T) {
	tracer, transport, flush, stop, err := startTestTracer(t, WithSpanPool(true))
	require.NoError(t, err)
	defer stop()

	// Span A: error, custom tags
	spanA := tracer.StartSpan("opA",
		Tag(ext.ManualKeep, true),
		ServiceName("svcA"),
		ResourceName("/a"),
		SpanType("web"),
		Tag("keyA", "valA"),
		Tag("metricA", 1.0),
	)
	spanA.SetTag(ext.Error, fmt.Errorf("span A error"))
	spanA.Finish()
	flush(1)
	transport.Traces() // drain

	// Span B: different tags, no error
	spanB := tracer.StartSpan("opB",
		Tag(ext.ManualKeep, true),
		ServiceName("svcB"),
		ResourceName("/b"),
		SpanType("db"),
		Tag("keyB", "valB"),
		Tag("metricB", 2.0),
	)
	spanB.Finish()
	flush(1)

	traces := transport.Traces()
	require.Len(t, traces, 1)
	require.Len(t, traces[0], 1)
	s := traces[0][0]

	// Positive: B's own data is correct.
	assert.Equal(t, "opB", s.name)
	assert.Equal(t, "svcB", s.service)
	assert.Equal(t, "/b", s.resource)
	assert.Equal(t, "db", s.spanType)
	v, _ := s.meta.Get("keyB")
	assert.Equal(t, "valB", v)
	assert.Equal(t, 2.0, s.metrics["metricB"])

	// Negative: no trace of A's data.
	assert.False(t, s.meta.Has("keyA"))
	assert.NotContains(t, s.metrics, "metricA")
	assert.Equal(t, int32(0), s.error)
	assert.NotEqual(t, "web", s.spanType)
	assert.False(t, s.meta.Has(ext.ErrorMsg))
	assert.False(t, s.meta.Has(ext.ErrorType))
	assert.False(t, s.meta.Has(ext.ErrorStack))
}

func TestSpanPoolMultipleRecycleRounds(t *testing.T) {
	tracer, transport, flush, stop, err := startTestTracer(t, WithSpanPool(true))
	require.NoError(t, err)
	defer stop()

	type roundData struct {
		name    string
		service string
		meta    map[string]string
		metrics map[string]float64
		hasErr  bool
	}

	rounds := make([]roundData, 5)
	for i := range rounds {
		rounds[i] = roundData{
			name:    fmt.Sprintf("op-%d", i),
			service: fmt.Sprintf("svc-%d", i),
			meta:    map[string]string{fmt.Sprintf("key-%d", i): fmt.Sprintf("val-%d", i)},
			metrics: map[string]float64{fmt.Sprintf("metric-%d", i): float64(i)},
			hasErr:  i%2 == 0, // error on even rounds
		}
	}

	for i, rd := range rounds {
		span := tracer.StartSpan(rd.name,
			Tag(ext.ManualKeep, true),
			ServiceName(rd.service),
		)
		for k, v := range rd.meta {
			span.SetTag(k, v)
		}
		for k, v := range rd.metrics {
			span.SetTag(k, v)
		}
		if rd.hasErr {
			span.SetTag(ext.Error, fmt.Errorf("error round %d", i))
		}
		span.Finish()
		flush(1)

		traces := transport.Traces()
		require.Len(t, traces, 1, "round %d", i)
		require.Len(t, traces[0], 1, "round %d", i)
		s := traces[0][0]

		// Positive: current round data matches.
		assert.Equal(t, rd.name, s.name, "round %d", i)
		assert.Equal(t, rd.service, s.service, "round %d", i)
		for k, v := range rd.meta {
			got, _ := s.meta.Get(k)
			assert.Equal(t, v, got, "round %d meta key %s", i, k)
		}
		for k, v := range rd.metrics {
			assert.Equal(t, v, s.metrics[k], "round %d metric key %s", i, k)
		}
		if rd.hasErr {
			assert.Equal(t, int32(1), s.error, "round %d", i)
		} else {
			assert.Equal(t, int32(0), s.error, "round %d", i)
		}

		// Negative: no prior round's data.
		for j := range i {
			for k := range rounds[j].meta {
				assert.False(t, s.meta.Has(k), "round %d has stale meta from round %d", i, j)
			}
			for k := range rounds[j].metrics {
				assert.NotContains(t, s.metrics, k, "round %d has stale metric from round %d", i, j)
			}
		}
	}
}

func TestSpanPoolSpanTypeAndErrorReset(t *testing.T) {
	tracer, transport, flush, stop, err := startTestTracer(t, WithSpanPool(true))
	require.NoError(t, err)
	defer stop()

	// A: type=web, error=1
	spanA := tracer.StartSpan("opA",
		Tag(ext.ManualKeep, true),
		SpanType("web"),
	)
	spanA.SetTag(ext.Error, fmt.Errorf("A error"))
	spanA.Finish()
	flush(1)
	transport.Traces() // drain

	// B: no type, no error
	spanB := tracer.StartSpan("opB",
		Tag(ext.ManualKeep, true),
	)
	spanB.Finish()
	flush(1)

	tracesB := transport.Traces()
	require.Len(t, tracesB, 1)
	require.Len(t, tracesB[0], 1)
	sB := tracesB[0][0]

	assert.Equal(t, "", sB.spanType)
	assert.Equal(t, int32(0), sB.error)

	// C: type=cache, error=1
	spanC := tracer.StartSpan("opC",
		Tag(ext.ManualKeep, true),
		SpanType("cache"),
	)
	spanC.SetTag(ext.Error, fmt.Errorf("C error"))
	spanC.Finish()
	flush(1)

	tracesC := transport.Traces()
	require.Len(t, tracesC, 1)
	require.Len(t, tracesC[0], 1)
	sC := tracesC[0][0]

	assert.Equal(t, "cache", sC.spanType)
	assert.Equal(t, int32(1), sC.error)
}

func TestSpanPoolPartialFlushedRootRetainedUntilTraceComplete(t *testing.T) {
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "2")

	tracer, transport, flush, stop, err := startTestTracer(t, WithSpanPool(true))
	require.NoError(t, err)
	defer stop()

	root := tracer.StartSpan("root", Tag(ext.ManualKeep, true))
	child0 := tracer.StartSpan("child0", ChildOf(root.Context()))
	child1 := tracer.StartSpan("child1", ChildOf(root.Context()))
	child2 := tracer.StartSpan("child2", ChildOf(root.Context()))

	root.Finish()
	child0.Finish() // triggers a partial flush containing root while child1/child2 remain open
	flush(1)

	traces := transport.Traces()
	require.Len(t, traces, 1)
	require.Len(t, traces[0], 2)

	flushedNames := map[string]bool{}
	for _, s := range traces[0] {
		flushedNames[s.name] = true
	}
	assert.True(t, flushedNames["root"])
	assert.True(t, flushedNames["child0"])

	// The partially flushed root is still trace.root for the unfinished children,
	// so it must not be cleared and returned to the pool until the trace completes.
	assert.Equal(t, "root", root.name)
	assert.True(t, root.finished)
	assert.Equal(t, root, child1.Root())
	assert.Equal(t, root, child2.Root())

	child1.Finish()
	child2.Finish()
	flush(1)

	traces = transport.Traces()
	require.Len(t, traces, 1)
	require.Len(t, traces[0], 2)
	remainingNames := map[string]bool{}
	for _, s := range traces[0] {
		remainingNames[s.name] = true
	}
	assert.False(t, remainingNames["root"], "root must not be encoded again in the final chunk")
	assert.True(t, remainingNames["child1"])
	assert.True(t, remainingNames["child2"])
}

func BenchmarkSpanPoolRelease(b *testing.B) {
	// Cycle one span at a time: release → acquire keeps the pool at 0-1
	// items, avoiding sync.Pool internal ring-buffer growth allocations
	// that cause flaky B/op across runs (GC clears the pool between
	// runN iterations, forcing ring-buffer rebuild with varying b.N).
	s := acquireSpan(true)
	for b.Loop() {
		s.clear()
		spanPool.Put(s)
		s = acquireSpan(true)
	}
}

func BenchmarkSpanPoolEndToEnd(b *testing.B) {
	poolModes := []struct {
		name    string
		enabled bool
	}{
		{"pool", true},
		{"nopool", false},
	}

	for _, pm := range poolModes {
		b.Run(pm.name+"/bare", func(b *testing.B) {
			agent := startTestAgent(b)
			tr := newTracerTest(b, agent, WithSpanPool(pm.enabled))

			b.ResetTimer()
			for range b.N {
				span := tr.StartSpan("bench.op")
				span.Finish()
			}
			b.StopTimer()

			stopTracerTest(tr)
			received := agent.SpanCount()
			b.ReportMetric(float64(received)/float64(b.N)*100, "delivery%")
		})

		b.Run(pm.name+"/tagged", func(b *testing.B) {
			agent := startTestAgent(b)
			tr := newTracerTest(b, agent, WithSpanPool(pm.enabled))

			b.ResetTimer()
			for range b.N {
				span := tr.StartSpan("bench.op",
					ServiceName("bench.svc"),
					ResourceName("/bench"),
					SpanType("web"),
				)
				span.SetTag("http.method", "GET")
				span.SetTag("http.url", "/bench/endpoint")
				span.SetTag("component", "benchmark")
				span.SetTag("response.size", 1024)
				span.SetTag("request.duration_ms", 1.5)
				span.Finish()
			}
			b.StopTimer()

			stopTracerTest(tr)
			received := agent.SpanCount()
			b.ReportMetric(float64(received)/float64(b.N)*100, "delivery%")
		})

		b.Run(pm.name+"/errored", func(b *testing.B) {
			agent := startTestAgent(b)
			tr := newTracerTest(b, agent, WithSpanPool(pm.enabled))
			benchErr := fmt.Errorf("benchmark error")

			b.ResetTimer()
			for range b.N {
				span := tr.StartSpan("bench.op")
				span.SetTag(ext.Error, benchErr)
				span.Finish()
			}
			b.StopTimer()

			stopTracerTest(tr)
			received := agent.SpanCount()
			b.ReportMetric(float64(received)/float64(b.N)*100, "delivery%")
		})

		// concurrent measures pool throughput under goroutine contention.
		// delivery% is expected to be well below 100% because pushChunk
		// (tracer.go) drops trace chunks when the tracer.out channel
		// (capacity payloadQueueSize=1000) is full. Under RunParallel,
		// goroutines produce spans far faster than the single worker can
		// drain the channel, so most chunks are silently dropped. This is
		// intentional production back-pressure behaviour; the metric
		// captures the drop rate under saturation.
		b.Run(pm.name+"/concurrent", func(b *testing.B) {
			agent := startTestAgent(b)
			tr := newTracerTest(b, agent, WithSpanPool(pm.enabled))

			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					span := tr.StartSpan("bench.op")
					span.Finish()
				}
			})
			b.StopTimer()

			stopTracerTest(tr)
			received := agent.SpanCount()
			b.ReportMetric(float64(received)/float64(b.N)*100, "delivery%")
		})
	}
}

// TestSpanPoolEndToEndConcurrentCorrectness verifies that spans created from
// multiple goroutines are delivered without corruption or duplication. The total
// span count (500) is kept well below payloadQueueSize (1000) so that pushChunk
// never drops chunks and we can assert exact delivery.
func TestSpanPoolEndToEndConcurrentCorrectness(t *testing.T) {
	const numGoroutines = 10
	const spansPerGoroutine = 50 // 500 total, well within payloadQueueSize

	agent := startTestAgent(t)
	tr := newTracerTest(t, agent, WithSpanPool(true))

	var wg sync.WaitGroup
	for g := range numGoroutines {
		wg.Go(func() {
			for i := range spansPerGoroutine {
				span := tr.StartSpan("pool.concurrent",
					ServiceName("e2e.svc"),
					ResourceName("/e2e"),
				)
				span.SetTag("uid", g*spansPerGoroutine+i)
				span.Finish()
			}
		})
	}
	wg.Wait()

	stopTracerTest(tr)

	const totalSpans = numGoroutines * spansPerGoroutine
	spans := agent.Spans()
	require.Equal(t, totalSpans, len(spans), "expected exactly %d spans", totalSpans)

	seen := make(map[float64]struct{}, totalSpans)
	for i, s := range spans {
		require.Equal(t, "pool.concurrent", s.name, "span %d: wrong name", i)
		require.NotEmpty(t, s.service, "span %d: empty service", i)

		uid, ok := s.metrics["uid"]
		require.True(t, ok, "span %d: missing 'uid' metric", i)
		_, dup := seen[uid]
		require.False(t, dup, "span %d: duplicate uid value %v", i, uid)
		seen[uid] = struct{}{}
	}

	for i := range totalSpans {
		_, ok := seen[float64(i)]
		require.True(t, ok, "missing uid value %d", i)
	}
}

// TestSpanPoolEndToEndParentChild verifies that parent-child span relationships
// survive pooling: traceIDs match, parentIDs are set, and pair tags don't leak
// across unrelated trace trees. Like the concurrent test, the total span count
// (400) stays below payloadQueueSize to guarantee full delivery.
func TestSpanPoolEndToEndParentChild(t *testing.T) {
	const numPairs = 200

	agent := startTestAgent(t)
	tr := newTracerTest(t, agent, WithSpanPool(true))

	for i := range numPairs {
		parent := tr.StartSpan("parent.op",
			ServiceName("e2e.svc"),
			Tag("pair", i),
		)
		child := tr.StartSpan("child.op",
			ServiceName("e2e.svc"),
			ChildOf(parent.Context()),
			Tag("pair", i),
		)
		child.Finish()
		parent.Finish()
	}

	stopTracerTest(tr)

	const totalSpans = numPairs * 2
	spans := agent.Spans()
	require.Equal(t, totalSpans, len(spans), "expected exactly %d spans", totalSpans)

	// Group spans by pair tag value.
	type pairGroup struct {
		spans []*Span
	}
	groups := make(map[float64]*pairGroup)
	for i, s := range spans {
		pair, ok := s.metrics["pair"]
		require.True(t, ok, "span %d: missing 'pair' metric", i)
		g, exists := groups[pair]
		if !exists {
			g = &pairGroup{}
			groups[pair] = g
		}
		g.spans = append(g.spans, s)
	}

	require.Equal(t, numPairs, len(groups), "expected %d pair groups", numPairs)

	for pairVal, g := range groups {
		require.Len(t, g.spans, 2, "pair %v: expected 2 spans", pairVal)

		var parent, child *Span
		for _, s := range g.spans {
			switch s.name {
			case "parent.op":
				parent = s
			case "child.op":
				child = s
			default:
				t.Fatalf("pair %v: unexpected span name %q", pairVal, s.name)
			}
		}
		require.NotNil(t, parent, "pair %v: missing parent span", pairVal)
		require.NotNil(t, child, "pair %v: missing child span", pairVal)

		require.NotZero(t, child.parentID, "pair %v: child has zero parentID", pairVal)
		require.Equal(t, parent.traceID, child.traceID,
			"pair %v: traceID mismatch between parent and child", pairVal)
	}
}

func TestSpanPoolEndToEndCorrectness(t *testing.T) {
	const numSpans = 500

	agent := startTestAgent(t)
	tr := newTracerTest(t, agent, WithSpanPool(true))

	for i := range numSpans {
		span := tr.StartSpan("pool.test",
			ServiceName("e2e.svc"),
			ResourceName("/e2e"),
		)
		span.SetTag("iter", i)
		span.Finish()
	}

	stopTracerTest(tr)

	spans := agent.Spans()
	require.Equal(t, numSpans, len(spans), "expected exactly %d spans", numSpans)

	seen := make(map[float64]struct{}, numSpans)
	for i, s := range spans {
		require.Equal(t, "pool.test", s.name, "span %d: wrong name", i)
		require.NotEmpty(t, s.service, "span %d: empty service", i)

		iter, ok := s.metrics["iter"]
		require.True(t, ok, "span %d: missing 'iter' metric", i)
		_, dup := seen[iter]
		require.False(t, dup, "span %d: duplicate iter value %v", i, iter)
		seen[iter] = struct{}{}
	}

	// Verify all iter values 0..499 are present.
	for i := range numSpans {
		_, ok := seen[float64(i)]
		require.True(t, ok, "missing iter value %d", i)
	}
}
