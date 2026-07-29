// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package goka

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lovoo/goka"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/datastreams"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
)

// mockContext is a minimal goka.Context implementation for unit testing. It
// exposes settable inbound message metadata and captures Emit/Loopback calls.
type mockContext struct {
	topic     goka.Stream
	key       string
	partition int32
	offset    int64
	group     goka.Group
	headers   goka.Headers
	baseCtx   context.Context
	ts        time.Time
	value     any
	joinVal   any
	lookupVal any

	emits           []emitCall
	loopbacks       []emitCall
	setValue        any
	setValueCalled  bool
	deleteCalled    bool
	deferCommitted  bool
	committedCalled bool
	committedErr    error
}

type emitCall struct {
	topic string
	key   string
	value any
	opts  []goka.ContextOption
}

var _ goka.Context = (*mockContext)(nil)

func (m *mockContext) Topic() goka.Stream { return m.topic }
func (m *mockContext) Key() string        { return m.key }
func (m *mockContext) Partition() int32   { return m.partition }
func (m *mockContext) Offset() int64      { return m.offset }
func (m *mockContext) Group() goka.Group  { return m.group }
func (m *mockContext) Value() any         { return m.value }
func (m *mockContext) Headers() goka.Headers {
	if m.headers == nil {
		return goka.Headers{}
	}
	return m.headers
}
func (m *mockContext) SetValue(v any, _ ...goka.ContextOption) {
	m.setValue = v
	m.setValueCalled = true
}
func (m *mockContext) Delete(...goka.ContextOption)  { m.deleteCalled = true }
func (m *mockContext) Timestamp() time.Time          { return m.ts }
func (m *mockContext) Join(goka.Table) any           { return m.joinVal }
func (m *mockContext) Lookup(goka.Table, string) any { return m.lookupVal }
func (m *mockContext) DeferCommit() func(error) {
	m.deferCommitted = true
	return func(err error) {
		m.committedCalled = true
		m.committedErr = err
	}
}
func (m *mockContext) Context() context.Context {
	if m.baseCtx != nil {
		return m.baseCtx
	}
	return context.Background()
}
func (m *mockContext) Emit(topic goka.Stream, key string, value any, opts ...goka.ContextOption) {
	m.emits = append(m.emits, emitCall{string(topic), key, value, opts})
}
func (m *mockContext) Loopback(key string, value any, opts ...goka.ContextOption) {
	m.loopbacks = append(m.loopbacks, emitCall{"", key, value, opts})
}

// Fail mimics goka: it panics to unwind the callback.
func (m *mockContext) Fail(err error) { panic(err) }

func TestCarrierRoundTrip(t *testing.T) {
	headers := goka.Headers{}
	carrier := gokaHeadersCarrier(headers)
	carrier.Set("k1", "v1")
	carrier.Set("k2", "v2")

	assert.Equal(t, []byte("v1"), headers["k1"])
	assert.Equal(t, []byte("v2"), headers["k2"])

	got := map[string]string{}
	require.NoError(t, carrier.ForeachKey(func(k, v string) error {
		got[k] = v
		return nil
	}))
	assert.Equal(t, map[string]string{"k1": "v1", "k2": "v2"}, got)
}

func TestWrapContext_NoCheckpointWhenDSMDisabled(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewTracer() // DSM off by default
	ctx := tr.WrapContext(&mockContext{topic: "in", group: "g"})
	tc := ctx.(*tracedContext)
	assert.Nil(t, tc.dsmCtx, "no inbound DSM pathway should be set when DSM is disabled")

	// Outbound headers should carry no DSM pathway either.
	headers := tc.tr.outboundHeaders(nil, tc.gctx.Context(), "out", 0)
	_, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), gokaHeadersCarrier(headers)))
	assert.False(t, ok, "no DSM pathway expected when DSM is disabled")
}

func TestConsumeSpan_ParentChildLinkage(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	// Simulate an upstream producer by injecting a parent span into the headers.
	parent := tracer.StartSpan("upstream")
	inHeaders := goka.Headers{}
	require.NoError(t, tracer.Inject(parent.Context(), gokaHeadersCarrier(inHeaders)))

	tr := NewTracer(WithService("svc"))
	gctx := &mockContext{topic: "orders", partition: 3, offset: 42, group: "g", headers: inHeaders}

	called := false
	cb := tr.WrapCallback(func(goka.Context, any) { called = true })
	cb(tr.WrapContext(gctx), "msg")

	assert.True(t, called)
	parent.Finish()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 2)
	var consume *mocktracer.Span
	for _, s := range spans {
		if s.OperationName() == "kafka.consume" {
			consume = s
		}
	}
	require.NotNil(t, consume, "expected a kafka.consume span")

	assert.Equal(t, parent.Context().TraceID(), consume.Context().TraceID())
	assert.Equal(t, parent.Context().SpanID(), consume.ParentID())
	assert.Equal(t, "svc", consume.Tag(ext.ServiceName))
	assert.Equal(t, "Consume Topic orders", consume.Tag(ext.ResourceName))
	assert.Equal(t, componentName, consume.Tag(ext.Component))
	assert.Equal(t, "consumer", consume.Tag(ext.SpanKind))
	assert.Equal(t, "kafka", consume.Tag(ext.MessagingSystem))
	assert.Equal(t, "orders", consume.Tag(ext.MessagingDestinationName))
	assert.EqualValues(t, 3, consume.Tag(ext.MessagingKafkaPartition))
	assert.EqualValues(t, 42, consume.Tag("offset"))
}

func TestConsumeSpan_ErrorTagging(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewTracer()
	gctx := &mockContext{topic: "orders", group: "g"}

	cb := tr.WrapCallback(func(ctx goka.Context, _ any) {
		ctx.Fail(assert.AnError) // records the error, then panics like goka
	})

	// goka expects the Fail panic to unwind; recover it here as goka would.
	require.Panics(t, func() { cb(tr.WrapContext(gctx), "msg") })

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, assert.AnError.Error(), spans[0].Tag(ext.ErrorMsg))
}

func TestConsumeSpan_PanicWithoutFail(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewTracer()
	gctx := &mockContext{topic: "orders", group: "g"}

	// A panic that never goes through ctx.Fail (e.g. an internal goka failure or
	// a bug in the handler) must still tag the consume span with the error.
	boom := errors.New("boom")
	cb := tr.WrapCallback(func(goka.Context, any) { panic(boom) })

	require.PanicsWithValue(t, boom, func() { cb(tr.WrapContext(gctx), "msg") })

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, boom.Error(), spans[0].Tag(ext.ErrorMsg))
}

func TestConsumeSpan_PanicNonError(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewTracer()
	gctx := &mockContext{topic: "orders", group: "g"}

	cb := tr.WrapCallback(func(goka.Context, any) { panic("kaboom") })

	require.PanicsWithValue(t, "kaboom", func() { cb(tr.WrapContext(gctx), "msg") })

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Contains(t, spans[0].Tag(ext.ErrorMsg), "kaboom")
}

func TestEmit_NoProduceSpanPropagatesConsume(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewTracer(WithService("svc"))
	gctx := &mockContext{topic: "orders", group: "g"}

	cb := tr.WrapCallback(func(ctx goka.Context, _ any) {
		ctx.Emit("orders-enriched", "k", "v")
	})
	cb(tr.WrapContext(gctx), "msg")

	// goka.Context.Emit is async with no completion handle, so no kafka.produce
	// span is created; only the consume span is emitted.
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "kafka.consume", spans[0].OperationName())

	// The emit still carries an injected headers option propagating the trace.
	require.Len(t, gctx.emits, 1)
	require.NotEmpty(t, gctx.emits[0].opts, "an emit-headers option should be prepended")
}

func TestDeferCommit_DefersOffsetTracking(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewTracer(WithDataStreams())
	gctx := &mockContext{topic: "orders", group: "g"}

	var commit func(error)
	cb := tr.WrapCallback(func(ctx goka.Context, _ any) {
		commit = ctx.DeferCommit()
	})
	cb(tr.WrapContext(gctx), "msg")

	// The callback deferred the commit, so the underlying commit is not called on
	// callback return — it runs only when the returned function is invoked.
	assert.True(t, gctx.deferCommitted)
	assert.False(t, gctx.committedCalled)

	require.NotNil(t, commit)
	commit(nil)
	assert.True(t, gctx.committedCalled)
	assert.NoError(t, gctx.committedErr)
}

func TestLoopTopic_UsesConfiguredSuffix(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	def := NewTracer().WrapContext(&mockContext{group: "g"}).(*tracedContext)
	assert.Equal(t, "g-loop", def.loopTopic())

	custom := NewTracer(WithLoopSuffix("-loop1")).WrapContext(&mockContext{group: "g"}).(*tracedContext)
	assert.Equal(t, "g-loop1", custom.loopTopic())
}

func TestEmit_InjectsTraceAndDSM(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewTracer(WithDataStreams())
	gctx := &mockContext{topic: "orders", group: "g"}

	cb := tr.WrapCallback(func(ctx goka.Context, _ any) {
		ctx.Emit("orders-enriched", "k", "v")
	})
	cb(tr.WrapContext(gctx), "msg")

	require.Len(t, gctx.emits, 1)
	call := gctx.emits[0]
	assert.Equal(t, "orders-enriched", call.topic)
	assert.Equal(t, "k", call.key)
	assert.Equal(t, "v", call.value)
	require.NotEmpty(t, call.opts, "an emit-headers option should be prepended")
}

func TestOutboundHeaders_TraceAndDSMContent(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewTracer(WithDataStreams())
	gctx := &mockContext{topic: "orders", group: "g"}
	tc := tr.WrapContext(gctx).(*tracedContext)
	finish := tc.startConsumeSpan()

	headers := tc.tr.outboundHeaders(tc.span, tc.dsmCtx, "orders-enriched", 0)

	// APM: the emitted headers carry a span context that is a child of the consume trace.
	extracted, err := ExtractSpanContext(headers)
	require.NoError(t, err)
	require.NotNil(t, extracted)
	assert.Equal(t, tc.span.Context().TraceID(), extracted.TraceID())

	// DSM: the emitted headers carry an outbound pathway.
	_, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), gokaHeadersCarrier(headers)))
	assert.True(t, ok, "expected a DSM pathway in outbound headers")

	finish()
}

func TestWrapCallback_PassthroughWhenContextNotWrapped(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewTracer()
	called := false
	cb := tr.WrapCallback(func(goka.Context, any) { called = true })

	// A raw (unwrapped) context must not start a span, just run the callback.
	cb(&mockContext{topic: "orders"}, "msg")
	assert.True(t, called)
	assert.Empty(t, mt.FinishedSpans())
}

func TestEmitHeaders_Standalone(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewTracer(WithDataStreams())

	span := tracer.StartSpan("producer")
	headers := tr.EmitHeaders(tracer.ContextWithSpan(context.Background(), span), "orders")
	span.Finish()

	// APM span context propagated.
	extracted, err := ExtractSpanContext(headers)
	require.NoError(t, err)
	require.NotNil(t, extracted)
	assert.Equal(t, span.Context().TraceID(), extracted.TraceID())

	// DSM outbound pathway present.
	_, ok := datastreams.PathwayFromContext(datastreams.ExtractFromBase64Carrier(context.Background(), gokaHeadersCarrier(headers)))
	assert.True(t, ok, "expected a DSM pathway in standalone emit headers")
}

func TestEmitHeaders_EmptyWhenDisabled(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewTracer() // no DSM, no active span
	headers := tr.EmitHeaders(context.Background(), "orders")
	assert.Empty(t, headers)
}

func TestLoopback_InjectsTraceHeaders(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tr := NewTracer(WithService("svc"))
	gctx := &mockContext{topic: "orders", group: "g"}

	cb := tr.WrapCallback(func(ctx goka.Context, _ any) {
		ctx.Loopback("k", "v")
	})
	cb(tr.WrapContext(gctx), "msg")

	require.Len(t, gctx.loopbacks, 1)
	call := gctx.loopbacks[0]
	assert.Equal(t, "k", call.key)
	assert.Equal(t, "v", call.value)
	require.NotEmpty(t, call.opts, "trace-headers option should be prepended on loopback")
}

func TestContext_FallsBackWhenNoSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	base := context.WithValue(context.Background(), struct{ k string }{"k"}, "v")
	tr := NewTracer()
	tc := tr.WrapContext(&mockContext{topic: "orders", baseCtx: base}).(*tracedContext)

	// No span started yet: Context() returns the wrapped context.
	assert.Equal(t, "v", tc.Context().Value(struct{ k string }{"k"}))

	// After a span starts, Context() returns the traced (span) context.
	finish := tc.startConsumeSpan()
	defer finish()
	span, ok := tracer.SpanFromContext(tc.Context())
	require.True(t, ok)
	assert.Equal(t, tc.span.Context().TraceID(), span.Context().TraceID())
}

func TestTracedContext_DelegatesToWrapped(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	ts := time.Unix(1700000000, 0)
	gctx := &mockContext{
		topic:     "orders",
		key:       "the-key",
		partition: 7,
		offset:    99,
		group:     "the-group",
		value:     "the-value",
		joinVal:   "joined",
		lookupVal: "looked-up",
		ts:        ts,
	}
	tc := NewTracer().WrapContext(gctx)

	assert.Equal(t, goka.Stream("orders"), tc.Topic())
	assert.Equal(t, "the-key", tc.Key())
	assert.EqualValues(t, 7, tc.Partition())
	assert.EqualValues(t, 99, tc.Offset())
	assert.Equal(t, goka.Group("the-group"), tc.Group())
	assert.Equal(t, "the-value", tc.Value())
	assert.Equal(t, ts, tc.Timestamp())
	assert.Equal(t, "joined", tc.Join("t"))
	assert.Equal(t, "looked-up", tc.Lookup("t", "k"))
	assert.NotNil(t, tc.Headers())

	tc.SetValue("new-value")
	assert.True(t, gctx.setValueCalled)
	assert.Equal(t, "new-value", gctx.setValue)

	tc.Delete()
	assert.True(t, gctx.deleteCalled)

	require.NotNil(t, tc.DeferCommit())
	assert.True(t, gctx.deferCommitted)
}
