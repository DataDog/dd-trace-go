// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"

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

type observedAgentSpan struct {
	Name       string
	Service    string
	Resource   string
	Type       string
	Start      int64
	Duration   int64
	SpanID     uint64
	TraceID    uint64
	ParentID   uint64
	Error      int32
	Meta       map[string]string
	Metrics    map[string]float64
	SpanLinks  []SpanLink
	SpanEvents []spanEvent
}

func observeAgentSpans(spans []*Span) []observedAgentSpan {
	out := make([]observedAgentSpan, 0, len(spans))
	for _, s := range spans {
		meta := maps.Clone(s.meta.Map(true))
		if len(meta) == 0 {
			meta = nil
		}
		metrics := maps.Clone(s.metrics)
		if len(metrics) == 0 {
			metrics = nil
		}
		links := make([]SpanLink, len(s.spanLinks))
		for i, link := range s.spanLinks {
			links[i] = link
			links[i].Attributes = maps.Clone(link.Attributes)
		}
		events := append([]spanEvent(nil), s.spanEvents...)
		out = append(out, observedAgentSpan{
			Name:       s.name,
			Service:    s.service,
			Resource:   s.resource,
			Type:       s.spanType,
			Start:      s.start,
			Duration:   s.duration,
			SpanID:     s.spanID,
			TraceID:    s.traceID,
			ParentID:   s.parentID,
			Error:      s.error,
			Meta:       meta,
			Metrics:    metrics,
			SpanLinks:  links,
			SpanEvents: events,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].SpanID < out[j].SpanID
	})
	return out
}

func spanByID(t *testing.T, spans []observedAgentSpan, id uint64) observedAgentSpan {
	t.Helper()
	for _, span := range spans {
		if span.SpanID == id {
			return span
		}
	}
	require.Failf(t, "missing span", "span_id=%d not found in %#v", id, spans)
	return observedAgentSpan{}
}

func requireMeta(t *testing.T, span observedAgentSpan, key, want string) {
	t.Helper()
	got, ok := span.Meta[key]
	require.True(t, ok, "span %d (%s) missing meta key %q", span.SpanID, span.Name, key)
	require.Equal(t, want, got, "span %d (%s) meta key %q", span.SpanID, span.Name, key)
}

func requireNoMeta(t *testing.T, span observedAgentSpan, keys ...string) {
	t.Helper()
	for _, key := range keys {
		require.NotContains(t, span.Meta, key, "span %d (%s) has stale meta key %q", span.SpanID, span.Name, key)
	}
}

func requireMetric(t *testing.T, span observedAgentSpan, key string, want float64) {
	t.Helper()
	got, ok := span.Metrics[key]
	require.True(t, ok, "span %d (%s) missing metric key %q", span.SpanID, span.Name, key)
	require.Equal(t, want, got, "span %d (%s) metric key %q", span.SpanID, span.Name, key)
}

func requireNoMetric(t *testing.T, span observedAgentSpan, keys ...string) {
	t.Helper()
	for _, key := range keys {
		require.NotContains(t, span.Metrics, key, "span %d (%s) has stale metric key %q", span.SpanID, span.Name, key)
	}
}

func requireProtocolRequests(t *testing.T, requests []string, protocol testTraceProtocol) {
	t.Helper()
	require.NotEmpty(t, requests)
	for _, path := range requests {
		require.Equal(t, protocol.path, path)
	}
}

func runStrictSpanPoolAgentScenario(t *testing.T, protocol testTraceProtocol, poolEnabled bool) ([]observedAgentSpan, []string) {
	t.Helper()
	agent := startTestAgent(t)
	tr := newAgentTracerTest(t, agent, protocol, WithSpanPool(poolEnabled))
	defer stopTracerTest(tr)

	base := time.Unix(1_700_000_000, 0)
	root := tr.StartSpan("strict.root",
		WithSpanID(1001),
		StartTime(base),
		Tag(ext.ManualKeep, true),
		ServiceName("strict.root.service"),
		ResourceName("/strict/root"),
		SpanType(ext.SpanTypeWeb),
		Tag(ext.Environment, "root-env"),
		Tag(ext.Version, "root-version"),
		Tag("phase", "related"),
		Tag("root.only", "root-value"),
		Tag("root.metric", 1.25),
		WithSpanLinks([]SpanLink{{
			TraceID:     9001,
			TraceIDHigh: 42,
			SpanID:      9002,
			Attributes:  map[string]string{"link.name": "root-link"},
			Tracestate:  "dd=s:1",
			Flags:       1,
		}}),
	)
	root.AddEvent("root.event",
		WithSpanEventTimestamp(base.Add(time.Microsecond)),
		WithSpanEventAttributes(map[string]any{"event.name": "root", "event.count": int64(1)}),
	)

	ctx := ContextWithSpan(context.Background(), root)
	child, ctx := StartSpanFromContext(ctx, "strict.child",
		WithSpanID(1002),
		StartTime(base.Add(time.Millisecond)),
		ServiceName("strict.child.service"),
		ResourceName("SELECT * FROM strict WHERE id=?"),
		SpanType(ext.SpanTypeSQL),
		Tag("phase", "related"),
		Tag("child.only", "child-value"),
		Tag("child.metric", 2.5),
	)
	grandchild, _ := StartSpanFromContext(ctx, "strict.grandchild",
		WithSpanID(1003),
		StartTime(base.Add(2*time.Millisecond)),
		ServiceName("strict.grandchild.service"),
		ResourceName("/strict/grandchild"),
		SpanType(ext.AppTypeRPC),
		Tag("phase", "related"),
		Tag("grandchild.only", "grandchild-value"),
		Tag("grandchild.metric", 3.5),
	)

	root.Finish(FinishTime(base.Add(10 * time.Millisecond)))
	grandchild.Finish(FinishTime(base.Add(11 * time.Millisecond)))
	child.Finish(WithError(errors.New("strict child error")), NoDebugStack(), FinishTime(base.Add(12*time.Millisecond)))
	flushAgentTracerTest(t, tr, agent, 3)

	for i := range 4 {
		span := tr.StartSpan(fmt.Sprintf("strict.independent.%d", i),
			WithSpanID(2001+uint64(i)),
			StartTime(base.Add(time.Duration(20+i)*time.Millisecond)),
			Tag(ext.ManualKeep, true),
			ServiceName("strict.independent.service"),
			ResourceName(fmt.Sprintf("/strict/independent/%d", i)),
			SpanType(ext.AppTypeCache),
			Tag("phase", "independent"),
			Tag("independent.index", i),
		)
		if i == 0 {
			span.SetTag("independent.only", "independent-value")
			span.SetTag("independent.metric", 4.5)
		}
		span.Finish(FinishTime(base.Add(time.Duration(30+i) * time.Millisecond)))
	}
	flushAgentTracerTest(t, tr, agent, 7)

	reuseParent := tr.StartSpan("strict.reuse.parent",
		WithSpanID(3001),
		StartTime(base.Add(40*time.Millisecond)),
		Tag(ext.ManualKeep, true),
		ServiceName("strict.reuse.parent.service"),
		ResourceName("/strict/reuse/parent"),
		SpanType(ext.SpanTypeWeb),
		Tag("phase", "reuse"),
		Tag("reuse.parent.only", "parent-value"),
		Tag("reuse.parent.metric", 5.5),
	)
	reuseCtx := ContextWithSpan(context.Background(), reuseParent)
	reuseChild, _ := StartSpanFromContext(reuseCtx, "strict.reuse.child",
		WithSpanID(3002),
		StartTime(base.Add(41*time.Millisecond)),
		ServiceName("strict.reuse.child.service"),
		ResourceName("/strict/reuse/child"),
		SpanType(ext.AppTypeRPC),
		Tag("phase", "reuse"),
		Tag("reuse.child.only", "child-value"),
		Tag("reuse.child.metric", 6.5),
	)
	reuseParent.Finish(FinishTime(base.Add(50 * time.Millisecond)))
	reuseChild.Finish(FinishTime(base.Add(51 * time.Millisecond)))
	flushAgentTracerTest(t, tr, agent, 9)

	minimal := tr.StartSpan("strict.minimal",
		WithSpanID(4001),
		StartTime(base.Add(60*time.Millisecond)),
		Tag(ext.ManualKeep, true),
		ServiceName("strict.minimal.service"),
		ResourceName("/strict/minimal"),
	)
	minimal.Finish(FinishTime(base.Add(61 * time.Millisecond)))
	flushAgentTracerTest(t, tr, agent, 10)

	return observeAgentSpans(agent.Spans()), agent.Requests()
}

func requireStrictSpanPoolAgentScenario(t *testing.T, spans []observedAgentSpan) {
	t.Helper()
	require.Len(t, spans, 10)

	root := spanByID(t, spans, 1001)
	require.Equal(t, "strict.root", root.Name)
	require.Equal(t, "strict.root.service", root.Service)
	require.Equal(t, "/strict/root", root.Resource)
	require.Equal(t, ext.SpanTypeWeb, root.Type)
	require.Equal(t, uint64(1001), root.TraceID)
	require.Zero(t, root.ParentID)
	require.Zero(t, root.Error)
	requireMeta(t, root, "root.only", "root-value")
	requireMeta(t, root, ext.Environment, "root-env")
	requireMeta(t, root, ext.Version, "root-version")
	requireMetric(t, root, "root.metric", 1.25)
	require.Len(t, root.SpanLinks, 1)
	require.Equal(t, uint64(9001), root.SpanLinks[0].TraceID)
	require.Equal(t, uint64(9002), root.SpanLinks[0].SpanID)
	require.Len(t, root.SpanEvents, 1)

	child := spanByID(t, spans, 1002)
	require.Equal(t, "strict.child", child.Name)
	require.Equal(t, uint64(1001), child.TraceID)
	require.Equal(t, uint64(1001), child.ParentID)
	require.Equal(t, int32(1), child.Error)
	requireMeta(t, child, "child.only", "child-value")
	requireMeta(t, child, ext.ErrorMsg, "strict child error")
	requireMetric(t, child, "child.metric", 2.5)
	requireNoMeta(t, child, "root.only", "grandchild.only", "independent.only", "reuse.parent.only", "reuse.child.only")
	requireNoMetric(t, child, "root.metric", "grandchild.metric", "independent.metric", "reuse.parent.metric", "reuse.child.metric")
	require.Empty(t, child.SpanLinks)
	require.Empty(t, child.SpanEvents)

	grandchild := spanByID(t, spans, 1003)
	require.Equal(t, "strict.grandchild", grandchild.Name)
	require.Equal(t, uint64(1001), grandchild.TraceID)
	require.Equal(t, uint64(1002), grandchild.ParentID)
	require.Zero(t, grandchild.Error)
	requireMeta(t, grandchild, "grandchild.only", "grandchild-value")
	requireMetric(t, grandchild, "grandchild.metric", 3.5)
	requireNoMeta(t, grandchild, "root.only", "child.only", "independent.only", "reuse.parent.only", "reuse.child.only", ext.ErrorMsg, ext.ErrorType, ext.ErrorStack)
	requireNoMetric(t, grandchild, "root.metric", "child.metric", "independent.metric", "reuse.parent.metric", "reuse.child.metric")

	for i := range 4 {
		id := 2001 + uint64(i)
		span := spanByID(t, spans, id)
		require.Equal(t, fmt.Sprintf("strict.independent.%d", i), span.Name)
		require.Equal(t, id, span.TraceID)
		require.Zero(t, span.ParentID)
		require.Zero(t, span.Error)
		requireMetric(t, span, "independent.index", float64(i))
		requireNoMeta(t, span, "root.only", "child.only", "grandchild.only", "reuse.parent.only", "reuse.child.only", ext.ErrorMsg, ext.ErrorType, ext.ErrorStack)
		requireNoMetric(t, span, "root.metric", "child.metric", "grandchild.metric", "reuse.parent.metric", "reuse.child.metric")
		require.Empty(t, span.SpanLinks)
		require.Empty(t, span.SpanEvents)
	}
	independent := spanByID(t, spans, 2001)
	requireMeta(t, independent, "independent.only", "independent-value")
	requireMetric(t, independent, "independent.metric", 4.5)

	reuseParent := spanByID(t, spans, 3001)
	require.Equal(t, "strict.reuse.parent", reuseParent.Name)
	require.Equal(t, uint64(3001), reuseParent.TraceID)
	require.Zero(t, reuseParent.ParentID)
	requireMeta(t, reuseParent, "reuse.parent.only", "parent-value")
	requireMetric(t, reuseParent, "reuse.parent.metric", 5.5)
	requireNoMeta(t, reuseParent, "root.only", "child.only", "grandchild.only", "independent.only", "reuse.child.only", ext.ErrorMsg, ext.ErrorType, ext.ErrorStack)
	requireNoMetric(t, reuseParent, "root.metric", "child.metric", "grandchild.metric", "independent.metric", "reuse.child.metric")

	reuseChild := spanByID(t, spans, 3002)
	require.Equal(t, "strict.reuse.child", reuseChild.Name)
	require.Equal(t, uint64(3001), reuseChild.TraceID)
	require.Equal(t, uint64(3001), reuseChild.ParentID)
	requireMeta(t, reuseChild, "reuse.child.only", "child-value")
	requireMetric(t, reuseChild, "reuse.child.metric", 6.5)
	requireNoMeta(t, reuseChild, "root.only", "child.only", "grandchild.only", "independent.only", "reuse.parent.only", ext.ErrorMsg, ext.ErrorType, ext.ErrorStack)
	requireNoMetric(t, reuseChild, "root.metric", "child.metric", "grandchild.metric", "independent.metric", "reuse.parent.metric")

	minimal := spanByID(t, spans, 4001)
	require.Equal(t, "strict.minimal", minimal.Name)
	require.Equal(t, "strict.minimal.service", minimal.Service)
	require.Equal(t, "/strict/minimal", minimal.Resource)
	require.Empty(t, minimal.Type)
	require.Equal(t, uint64(4001), minimal.TraceID)
	require.Zero(t, minimal.ParentID)
	require.Zero(t, minimal.Error)
	requireNoMeta(t, minimal,
		"root.only", "child.only", "grandchild.only", "independent.only", "reuse.parent.only", "reuse.child.only",
		"_dd.span_links", "events", ext.Environment, ext.Version, ext.ErrorMsg, ext.ErrorType, ext.ErrorStack,
	)
	requireNoMetric(t, minimal,
		"root.metric", "child.metric", "grandchild.metric", "independent.metric", "reuse.parent.metric", "reuse.child.metric",
	)
	require.Empty(t, minimal.SpanLinks)
	require.Empty(t, minimal.SpanEvents)
}

func TestSpanPoolAgentPOVMatchesNoPool(t *testing.T) {
	for _, protocol := range testTraceProtocols {
		t.Run(protocol.name, func(t *testing.T) {
			nopool, nopoolRequests := runStrictSpanPoolAgentScenario(t, protocol, false)
			pool, poolRequests := runStrictSpanPoolAgentScenario(t, protocol, true)

			requireStrictSpanPoolAgentScenario(t, nopool)
			requireStrictSpanPoolAgentScenario(t, pool)
			require.Equal(t, nopool, pool)
			requireProtocolRequests(t, nopoolRequests, protocol)
			requireProtocolRequests(t, poolRequests, protocol)
		})
	}
}

func runPartialFlushReuseAgentScenario(t *testing.T, protocol testTraceProtocol, poolEnabled bool) ([]observedAgentSpan, []string) {
	t.Helper()
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_ENABLED", "true")
	t.Setenv("DD_TRACE_PARTIAL_FLUSH_MIN_SPANS", "2")

	agent := startTestAgent(t)
	tr := newAgentTracerTest(t, agent, protocol, WithSpanPool(poolEnabled))
	defer stopTracerTest(tr)

	base := time.Unix(1_700_000_100, 0)
	root := tr.StartSpan("partial.root",
		WithSpanID(5001),
		StartTime(base),
		Tag(ext.ManualKeep, true),
		ServiceName("partial.root.service"),
		ResourceName("/partial/root"),
		SpanType(ext.SpanTypeWeb),
		Tag("partial.root.only", "root-value"),
	)
	child0 := tr.StartSpan("partial.child0",
		ChildOf(root.Context()),
		WithSpanID(5002),
		StartTime(base.Add(time.Millisecond)),
		ServiceName("partial.child.service"),
		Tag("partial.child0.only", "child0-value"),
	)
	child1 := tr.StartSpan("partial.child1",
		ChildOf(root.Context()),
		WithSpanID(5003),
		StartTime(base.Add(2*time.Millisecond)),
		ServiceName("partial.child.service"),
		Tag("partial.child1.only", "child1-value"),
	)
	child2 := tr.StartSpan("partial.child2",
		ChildOf(root.Context()),
		WithSpanID(5004),
		StartTime(base.Add(3*time.Millisecond)),
		ServiceName("partial.child.service"),
		Tag("partial.child2.only", "child2-value"),
	)

	root.Finish(FinishTime(base.Add(10 * time.Millisecond)))
	child0.Finish(FinishTime(base.Add(11 * time.Millisecond)))
	flushAgentTracerTest(t, tr, agent, 2)

	for i := range 8 {
		span := tr.StartSpan(fmt.Sprintf("partial.recycle.%d", i),
			WithSpanID(6001+uint64(i)),
			StartTime(base.Add(time.Duration(20+i)*time.Millisecond)),
			Tag(ext.ManualKeep, true),
			ServiceName("partial.recycle.service"),
			ResourceName(fmt.Sprintf("/partial/recycle/%d", i)),
			Tag("partial.recycle.only", fmt.Sprintf("recycle-%d", i)),
		)
		span.Finish(FinishTime(base.Add(time.Duration(30+i) * time.Millisecond)))
	}
	flushAgentTracerTest(t, tr, agent, 10)

	child1.Finish(FinishTime(base.Add(40 * time.Millisecond)))
	child2.Finish(FinishTime(base.Add(41 * time.Millisecond)))
	flushAgentTracerTest(t, tr, agent, 12)

	minimal := tr.StartSpan("partial.after",
		WithSpanID(7001),
		StartTime(base.Add(50*time.Millisecond)),
		Tag(ext.ManualKeep, true),
		ServiceName("partial.after.service"),
		ResourceName("/partial/after"),
	)
	minimal.Finish(FinishTime(base.Add(51 * time.Millisecond)))
	flushAgentTracerTest(t, tr, agent, 13)

	return observeAgentSpans(agent.Spans()), agent.Requests()
}

func requirePartialFlushReuseAgentScenario(t *testing.T, spans []observedAgentSpan) {
	t.Helper()
	require.Len(t, spans, 13)

	root := spanByID(t, spans, 5001)
	require.Equal(t, "partial.root", root.Name)
	require.Equal(t, uint64(5001), root.TraceID)
	require.Zero(t, root.ParentID)
	requireMeta(t, root, "partial.root.only", "root-value")

	for i := range 3 {
		id := 5002 + uint64(i)
		span := spanByID(t, spans, id)
		require.Equal(t, fmt.Sprintf("partial.child%d", i), span.Name)
		require.Equal(t, uint64(5001), span.TraceID)
		require.Equal(t, uint64(5001), span.ParentID)
		requireNoMeta(t, span, "partial.root.only", "partial.recycle.only")
	}

	for i := range 8 {
		id := 6001 + uint64(i)
		span := spanByID(t, spans, id)
		require.Equal(t, fmt.Sprintf("partial.recycle.%d", i), span.Name)
		require.Equal(t, id, span.TraceID)
		require.Zero(t, span.ParentID)
		requireMeta(t, span, "partial.recycle.only", fmt.Sprintf("recycle-%d", i))
		requireNoMeta(t, span, "partial.root.only", "partial.child0.only", "partial.child1.only", "partial.child2.only")
	}

	after := spanByID(t, spans, 7001)
	require.Equal(t, "partial.after", after.Name)
	require.Equal(t, uint64(7001), after.TraceID)
	require.Zero(t, after.ParentID)
	requireNoMeta(t, after, "partial.root.only", "partial.recycle.only", "partial.child0.only", "partial.child1.only", "partial.child2.only")
}

func TestSpanPoolPartialFlushAgentPOVMatchesNoPoolAfterReuse(t *testing.T) {
	for _, protocol := range testTraceProtocols {
		t.Run(protocol.name, func(t *testing.T) {
			nopool, nopoolRequests := runPartialFlushReuseAgentScenario(t, protocol, false)
			pool, poolRequests := runPartialFlushReuseAgentScenario(t, protocol, true)

			requirePartialFlushReuseAgentScenario(t, nopool)
			requirePartialFlushReuseAgentScenario(t, pool)
			require.Equal(t, nopool, pool)
			requireProtocolRequests(t, nopoolRequests, protocol)
			requireProtocolRequests(t, poolRequests, protocol)
		})
	}
}

func runSingleSpanSamplingAgentScenario(t *testing.T, protocol testTraceProtocol, poolEnabled bool) ([]observedAgentSpan, []string) {
	t.Helper()
	agent := startTestAgent(t)
	tr := newAgentTracerTest(t, agent,
		protocol,
		WithSpanPool(poolEnabled),
		WithSamplerRate(0),
		WithSamplingRules(SpanSamplingRules(Rule{NameGlob: "sampling.keep", Rate: 1.0})),
	)
	defer stopTracerTest(tr)

	base := time.Unix(1_700_000_200, 0)
	root := tr.StartSpan("sampling.drop.root",
		WithSpanID(9001),
		StartTime(base),
		ServiceName("sampling.service"),
		ResourceName("/sampling/root"),
		Tag("sampling.root.only", "drop-root"),
	)
	keep := tr.StartSpan("sampling.keep",
		ChildOf(root.Context()),
		WithSpanID(9002),
		StartTime(base.Add(time.Millisecond)),
		ServiceName("sampling.service"),
		ResourceName("/sampling/keep"),
		Tag("sampling.keep.only", "keep-child"),
		Tag("sampling.keep.metric", 9.5),
	)
	drop := tr.StartSpan("sampling.drop.child",
		ChildOf(root.Context()),
		WithSpanID(9003),
		StartTime(base.Add(2*time.Millisecond)),
		ServiceName("sampling.service"),
		ResourceName("/sampling/drop"),
		Tag("sampling.drop.only", "drop-child"),
	)

	drop.Finish(FinishTime(base.Add(10 * time.Millisecond)))
	keep.Finish(FinishTime(base.Add(11 * time.Millisecond)))
	root.Finish(FinishTime(base.Add(12 * time.Millisecond)))
	flushAgentTracerTest(t, tr, agent, 1)

	after := tr.StartSpan("sampling.after",
		WithSpanID(9010),
		StartTime(base.Add(20*time.Millisecond)),
		Tag(ext.ManualKeep, true),
		ServiceName("sampling.after.service"),
		ResourceName("/sampling/after"),
	)
	after.Finish(FinishTime(base.Add(21 * time.Millisecond)))
	flushAgentTracerTest(t, tr, agent, 2)

	return observeAgentSpans(agent.Spans()), agent.Requests()
}

func requireSingleSpanSamplingAgentScenario(t *testing.T, spans []observedAgentSpan) {
	t.Helper()
	require.Len(t, spans, 2)

	kept := spanByID(t, spans, 9002)
	require.Equal(t, "sampling.keep", kept.Name)
	require.Equal(t, uint64(9001), kept.TraceID)
	require.Equal(t, uint64(9001), kept.ParentID)
	requireMeta(t, kept, "sampling.keep.only", "keep-child")
	requireMetric(t, kept, "sampling.keep.metric", 9.5)
	requireMetric(t, kept, keySpanSamplingMechanism, float64(samplernames.SingleSpan))
	requireMetric(t, kept, keySingleSpanSamplingRuleRate, 1.0)
	requireNoMeta(t, kept, "sampling.root.only", "sampling.drop.only")

	after := spanByID(t, spans, 9010)
	require.Equal(t, "sampling.after", after.Name)
	require.Equal(t, uint64(9010), after.TraceID)
	require.Zero(t, after.ParentID)
	requireNoMeta(t, after, "sampling.root.only", "sampling.keep.only", "sampling.drop.only")
	requireNoMetric(t, after, "sampling.keep.metric", keySpanSamplingMechanism, keySingleSpanSamplingRuleRate)
}

func TestSpanPoolSingleSpanSamplingAgentPOVMatchesNoPool(t *testing.T) {
	for _, protocol := range testTraceProtocols {
		t.Run(protocol.name, func(t *testing.T) {
			nopool, nopoolRequests := runSingleSpanSamplingAgentScenario(t, protocol, false)
			pool, poolRequests := runSingleSpanSamplingAgentScenario(t, protocol, true)

			requireSingleSpanSamplingAgentScenario(t, nopool)
			requireSingleSpanSamplingAgentScenario(t, pool)
			require.Equal(t, nopool, pool)
			requireProtocolRequests(t, nopoolRequests, protocol)
			requireProtocolRequests(t, poolRequests, protocol)
		})
	}
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
