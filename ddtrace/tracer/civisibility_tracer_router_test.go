// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type callbackTestTracer struct {
	onStart     func()
	startCount  atomic.Int32
	finishCount atomic.Int32
	stopCount   atomic.Int32
	lastConfig  atomic.Pointer[StartSpanConfig]
}

func (t *callbackTestTracer) StartSpan(_ string, opts ...StartSpanOption) *Span {
	t.startCount.Add(1)
	t.lastConfig.Store(NewStartSpanConfig(opts...))
	if t.onStart != nil {
		t.onStart()
	}
	return &Span{}
}

func (*callbackTestTracer) Extract(any) (*SpanContext, error) { return nil, nil }
func (*callbackTestTracer) Inject(*SpanContext, any) error    { return nil }
func (*callbackTestTracer) TracerConf() TracerConf            { return TracerConf{} }
func (*callbackTestTracer) Flush()                            {}
func (t *callbackTestTracer) Stop()                           { t.stopCount.Add(1) }
func (t *callbackTestTracer) FinishSpan(*Span)                { t.finishCount.Add(1) }

type resettingTestTracer struct {
	callbackTestTracer
	resetCount atomic.Int32
}

func (t *resettingTestTracer) Reset() { t.resetCount.Add(1) }

func TestCIVisibilityTracerRouterRoutesWithoutPerSpanOwnership(t *testing.T) {
	ciTracer := &callbackTestTracer{}
	mockTracer := &callbackTestTracer{}
	applicationTracer := &callbackTestTracer{}
	router := newCIVisibilityTracerRouter(ciTracer, false)

	require.True(t, router.SetApplicationTracer(applicationTracer))
	require.True(t, router.SetMockTracer(mockTracer))
	router.StartSpan("ci.test", SpanType(constants.SpanTypeTest))
	router.StartSpan("application.mocked")

	require.EqualValues(t, 1, ciTracer.startCount.Load())
	require.EqualValues(t, 1, mockTracer.startCount.Load())
	require.Zero(t, applicationTracer.startCount.Load())

	applicationSpan := &Span{spanType: ext.SpanTypeWeb}
	router.FinishSpan(applicationSpan)
	require.EqualValues(t, 1, mockTracer.finishCount.Load())

	require.True(t, router.ClearMockTracer(mockTracer))
	router.StartSpan("application.restored")
	require.EqualValues(t, 1, applicationTracer.startCount.Load())
}

func TestCIVisibilityTracerRouterReplacesCIDelegate(t *testing.T) {
	first := &callbackTestTracer{}
	second := &callbackTestTracer{}
	router := newCIVisibilityTracerRouter(first, false)

	require.True(t, router.SetCIVisibilityTracer(second))
	require.EqualValues(t, 1, first.stopCount.Load())
	require.Zero(t, second.stopCount.Load())
	require.Same(t, second, router.ciVisibilityTracer())
}

func TestCIVisibilityTracerRouterReplacesNoopPolicyWithCIDelegate(t *testing.T) {
	first := &callbackTestTracer{}
	second := &callbackTestTracer{}
	router := newCIVisibilityTracerRouter(first, false)

	replacement := newCIVisibilityTracerRouter(second, true)
	require.True(t, router.SetCIVisibilityTracer(replacement))
	require.Nil(t, router.StartSpan("application.operation"))
	require.Zero(t, second.startCount.Load())

	replacement = newCIVisibilityTracerRouter(first, false)
	require.True(t, router.SetCIVisibilityTracer(replacement))
	require.NotNil(t, router.StartSpan("application.operation"))
	require.EqualValues(t, 1, first.startCount.Load())
}

func TestCIVisibilityTracerRouterDetachMockTracer(t *testing.T) {
	t.Run("transfer concrete CI tracer", func(t *testing.T) {
		ciTracer := &callbackTestTracer{}
		mockTracer := &callbackTestTracer{}
		router := newCIVisibilityTracerRouter(ciTracer, false)
		require.True(t, router.SetMockTracer(mockTracer))

		replacement, ok := router.DetachMockTracer(mockTracer)

		require.True(t, ok)
		require.Same(t, ciTracer, replacement)
		require.Nil(t, router.currentMockTracer())
		require.IsType(t, &NoopTracer{}, router.ciVisibilityTracer())
	})

	t.Run("keep router while noop policy is active", func(t *testing.T) {
		mockTracer := &callbackTestTracer{}
		router := newCIVisibilityTracerRouter(&callbackTestTracer{}, true)
		require.True(t, router.SetMockTracer(mockTracer))

		replacement, ok := router.DetachMockTracer(mockTracer)

		require.True(t, ok)
		require.Same(t, router, replacement)
	})

	t.Run("reject stale mock", func(t *testing.T) {
		activeMock := &callbackTestTracer{}
		router := newCIVisibilityTracerRouter(&callbackTestTracer{}, false)
		require.True(t, router.SetMockTracer(activeMock))

		replacement, ok := router.DetachMockTracer(&callbackTestTracer{})

		require.False(t, ok)
		require.Nil(t, replacement)
		require.Same(t, activeMock, router.currentMockTracer())
	})
}

func TestCIVisibilityTracerRouterResetOnlyResetsActiveMock(t *testing.T) {
	router := newCIVisibilityTracerRouter(&callbackTestTracer{}, false)
	mockTracer := &resettingTestTracer{}
	require.True(t, router.SetMockTracer(mockTracer))

	router.Reset()

	require.EqualValues(t, 1, mockTracer.resetCount.Load())
}

func TestAttachMockTracerToActiveConcreteCITracer(t *testing.T) {
	previousState := civisibility.GetState()
	ciTracer, _ := newUninstalledTestTracer(t)
	mockTracer := &callbackTestTracer{}
	civisibility.SetState(civisibility.StateInitialized)
	setGlobalTracer(ciTracer)
	t.Cleanup(func() {
		civisibility.SetState(civisibility.StateExiting)
		Stop()
		civisibility.SetState(previousState)
	})

	attached := attachMockTracerToCIVisibility(mockTracer)

	router, ok := attached.(*ciVisibilityTracerRouter)
	require.True(t, ok)
	require.Same(t, router, getGlobalTracer())
	require.Same(t, ciTracer, router.ciVisibilityTracer())
	require.Same(t, mockTracer, router.currentMockTracer())
}

func TestCIVisibilityTracerRouter_StartSpan_RoutesBySpanType(t *testing.T) {
	ciTracer := &callbackTestTracer{}
	router := wrapWithCIVisibilityTracerRouter(ciTracer)

	tests := []struct {
		name string
		opts []StartSpanOption
		want bool
	}{
		{name: "test", opts: []StartSpanOption{SpanType(constants.SpanTypeTest)}, want: true},
		{name: "test suite", opts: []StartSpanOption{SpanType(constants.SpanTypeTestSuite)}, want: true},
		{name: "test module", opts: []StartSpanOption{SpanType(constants.SpanTypeTestModule)}, want: true},
		{name: "test session", opts: []StartSpanOption{SpanType(constants.SpanTypeTestSession)}, want: true},
		{name: "all nil options", opts: []StartSpanOption{nil, nil, nil}},
		{name: "nil options at end", opts: []StartSpanOption{SpanType(constants.SpanTypeTest), nil, nil}, want: true},
		{name: "nil options at beginning and end", opts: []StartSpanOption{nil, SpanType(constants.SpanTypeTest), nil}, want: true},
		{name: "nil options at beginning", opts: []StartSpanOption{nil, nil, SpanType(constants.SpanTypeTest)}, want: true},
		{name: "application", opts: []StartSpanOption{SpanType(ext.SpanTypeWeb)}},
		{name: "generic span", opts: []StartSpanOption{SpanType(constants.SpanTypeSpan)}},
		{name: "no span type", opts: []StartSpanOption{ResourceName("resource")}},
		{name: "no options"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := ciTracer.startCount.Load()
			span := router.StartSpan("operation", tt.opts...)
			if tt.want {
				require.NotNil(t, span)
				require.EqualValues(t, before+1, ciTracer.startCount.Load())
				return
			}
			assert.Nil(t, span)
			assert.EqualValues(t, before, ciTracer.startCount.Load())
		})
	}
}

func TestCIVisibilityTracerRouter_StartSpan_PreservesConfig(t *testing.T) {
	ciTracer := &callbackTestTracer{}
	router := wrapWithCIVisibilityTracerRouter(ciTracer)
	parent := &SpanContext{spanID: 123}
	startTime := time.Now().Add(-time.Hour)
	ctx := context.Background()
	links := []SpanLink{{TraceID: 123, SpanID: 456}}

	require.NotNil(t, router.StartSpan("operation",
		SpanType(constants.SpanTypeTest),
		Tag("custom.tag", "custom-value"),
		StartTime(startTime),
		ChildOf(parent),
		WithSpanID(999),
		withContext(ctx),
		WithSpanLinks(links),
	))

	cfg := ciTracer.lastConfig.Load()
	require.NotNil(t, cfg)
	assert.Same(t, parent, cfg.Parent)
	assert.Equal(t, startTime, cfg.StartTime)
	assert.Equal(t, constants.SpanTypeTest, cfg.Tags[ext.SpanType])
	assert.Equal(t, "custom-value", cfg.Tags["custom.tag"])
	assert.EqualValues(t, 999, cfg.SpanID)
	assert.Equal(t, ctx, cfg.Context)
	assert.Equal(t, links, cfg.SpanLinks)
}

func TestCIVisibilityTracerRouter_Extract(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := wrapWithCIVisibilityTracerRouter(tr)

	// Extract should always return nil, nil
	carrier := TextMapCarrier(map[string]string{
		"x-datadog-trace-id":  "123",
		"x-datadog-parent-id": "456",
	})

	ctx, err := wrapped.Extract(carrier)
	assert.Nil(t, ctx)
	assert.Nil(t, err)
}

func TestCIVisibilityTracerRouter_Inject(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := wrapWithCIVisibilityTracerRouter(tr)

	// Create a span context to inject
	span := tr.StartSpan("test.operation", SpanType(constants.SpanTypeTest))
	require.NotNil(t, span)

	carrier := TextMapCarrier(map[string]string{})

	// Inject should return nil (no error) but not actually inject anything
	injectErr := wrapped.Inject(span.Context(), carrier)
	assert.Nil(t, injectErr)
	assert.Empty(t, carrier)

	span.Finish()
}

func TestCIVisibilityTracerRouter_PreservesCIVisibilityAcrossApplicationStop(t *testing.T) {
	ciTracer, ciTransport := newUninstalledTestTracer(t)
	wrapped := wrapWithCIVisibilityTracerRouter(ciTracer)
	setGlobalTracer(wrapped)
	civisibility.SetState(civisibility.StateInitialized)
	t.Cleanup(func() {
		civisibility.SetState(civisibility.StateExiting)
		Stop()
		civisibility.SetState(civisibility.StateUninitialized)
	})

	ciSpan := StartSpan("ci.test", SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpan)

	Stop()
	require.Same(t, wrapped, getGlobalTracer())

	ciSpan.Finish()
	require.Eventually(t, func() bool {
		Flush()
		return ciTransport.Len() == 1
	}, time.Second, 5*time.Millisecond)
}

func TestCIVisibilityTracerRouter_RestoresConcreteCITracerAfterApplicationStop(t *testing.T) {
	ciTracer, _ := newUninstalledTestTracer(t)
	applicationTracer := &preservingTestTracer{}
	setGlobalTracer(ciTracer)
	civisibility.SetState(civisibility.StateInitialized)
	t.Cleanup(func() {
		civisibility.SetState(civisibility.StateExiting)
		Stop()
		civisibility.SetState(civisibility.StateUninitialized)
	})

	setGlobalTracerPreservingCIVisibilityMockTracer(applicationTracer, false)
	require.IsType(t, &ciVisibilityTracerRouter{}, getGlobalTracer())

	Stop()
	require.Same(t, ciTracer, getGlobalTracer())
	require.EqualValues(t, 1, applicationTracer.stopCnt.Load())
}

func TestCIVisibilityTracerRouter_RoutesApplicationAndCISpansAfterApplicationStart(t *testing.T) {
	ciTracer, ciTransport := newUninstalledTestTracer(t)
	ciTracerConf := ciTracer.TracerConf()
	applicationTransport := newDummyTransport()
	wrapped := wrapWithCIVisibilityTracerRouter(ciTracer)
	setGlobalTracer(wrapped)
	civisibility.SetState(civisibility.StateInitialized)
	t.Cleanup(func() {
		civisibility.SetState(civisibility.StateExiting)
		Stop()
		civisibility.SetState(civisibility.StateUninitialized)
	})

	ciSpanBeforeApplicationStart := StartSpan("ci.test.before", SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpanBeforeApplicationStart)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")
	t.Setenv("DD_TRACE_ENABLED", "true")
	require.NoError(t, Start(
		withTransport(applicationTransport),
		withNoopStats(),
		WithService("application-service"),
		WithHTTPClient(internal.DefaultHTTPClient(defaultHTTPTimeout, true)),
	))
	require.Same(t, wrapped, getGlobalTracer())
	require.Equal(t, ciTracerConf, wrapped.TracerConf())

	ciSpanAfterApplicationStart := StartSpan("ci.test.after", SpanType(constants.SpanTypeTest))
	applicationSpan := StartSpan("http.request", SpanType(ext.SpanTypeWeb))
	require.NotNil(t, ciSpanAfterApplicationStart)
	require.NotNil(t, applicationSpan)

	applicationSpan.Finish()
	Stop()
	require.Same(t, wrapped, getGlobalTracer())

	ciSpanBeforeApplicationStart.Finish()
	ciSpanAfterApplicationStart.Finish()
	require.Eventually(t, func() bool {
		Flush()
		return ciTransport.Len() == 2 && applicationTransport.Len() == 1
	}, time.Second, 5*time.Millisecond)
}

func TestCIVisibilityTracerRouter_ApplicationOptionsRunOnceWithoutDelegateLock(t *testing.T) {
	ciTracer := &callbackTestTracer{}
	applicationTracer := &callbackTestTracer{}
	replacementTracer := &callbackTestTracer{}
	wrapped := wrapWithCIVisibilityTracerRouter(ciTracer)
	require.True(t, wrapped.SetApplicationTracer(applicationTracer))

	var optionCalls atomic.Int32
	var replaced atomic.Bool
	applicationTracer.onStart = func() {
		// Replacing the delegate from inside a sampler-like callback must not
		// deadlock on the wrapper's read lock.
		replaced.Store(wrapped.SetApplicationTracer(replacementTracer))
	}

	done := make(chan struct{})
	go func() {
		wrapped.StartSpan("application.operation", func(*StartSpanConfig) {
			optionCalls.Add(1)
		})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("application StartSpan callback blocked while replacing its delegate")
	}
	require.True(t, replaced.Load())
	require.EqualValues(t, 1, optionCalls.Load())
	require.EqualValues(t, 1, applicationTracer.stopCount.Load())
}

func TestCIVisibilityTracerRouter_StopsDelegatesAtTheirLifecycleBoundaries(t *testing.T) {
	ciTracer := &preservingTestTracer{}
	firstApplicationTracer := &preservingTestTracer{}
	secondApplicationTracer := &preservingTestTracer{}
	wrapped := wrapWithCIVisibilityTracerRouter(ciTracer)
	setGlobalTracer(wrapped)
	civisibility.SetState(civisibility.StateInitialized)
	t.Cleanup(func() {
		civisibility.SetState(civisibility.StateExiting)
		Stop()
		civisibility.SetState(civisibility.StateUninitialized)
	})

	require.True(t, wrapped.SetApplicationTracer(firstApplicationTracer))
	require.True(t, wrapped.SetApplicationTracer(secondApplicationTracer))
	require.EqualValues(t, 1, firstApplicationTracer.stopCnt.Load())
	require.Zero(t, secondApplicationTracer.stopCnt.Load())
	require.Zero(t, ciTracer.stopCnt.Load())

	Stop()
	require.Same(t, wrapped, getGlobalTracer())
	require.EqualValues(t, 1, secondApplicationTracer.stopCnt.Load())
	require.Zero(t, ciTracer.stopCnt.Load())

	civisibility.SetState(civisibility.StateExiting)
	Stop()
	require.IsType(t, &NoopTracer{}, getGlobalTracer())
	require.EqualValues(t, 1, secondApplicationTracer.stopCnt.Load())
	require.EqualValues(t, 1, ciTracer.stopCnt.Load())
}

func newUninstalledTestTracer(t testing.TB) (*tracer, *dummyTransport) {
	t.Helper()
	transport := newDummyTransport()
	tr, err := newTracer(
		withTransport(transport),
		WithHTTPClient(internal.DefaultHTTPClient(defaultHTTPTimeout, true)),
	)
	require.NoError(t, err)
	return tr, transport
}

func TestUseConfigNil(t *testing.T) {
	opt := useConfig(nil)
	cfg := &StartSpanConfig{}

	assert.NotPanics(t, func() {
		opt(cfg)
	})
	assert.Nil(t, cfg.Parent)
	assert.True(t, cfg.StartTime.IsZero())
	assert.Nil(t, cfg.Tags)
}

func TestCIVisibilityTracerRouter_MixedSpans(t *testing.T) {
	// Test that CI Visibility spans work while non-CI spans are dropped
	tr, transport, flush, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := wrapWithCIVisibilityTracerRouter(tr)

	// Create CI Visibility spans (should work)
	ciSpan1 := wrapped.StartSpan("ci.span1", SpanType(constants.SpanTypeTest))
	ciSpan2 := wrapped.StartSpan("ci.span2", SpanType(constants.SpanTypeTestSuite))

	// Create non-CI spans (should be nil)
	nonCISpan1 := wrapped.StartSpan("non.ci.span1", SpanType("web"))
	nonCISpan2 := wrapped.StartSpan("non.ci.span2", SpanType("db"))
	nonCISpan3 := wrapped.StartSpan("non.ci.span3") // no span type

	// Verify CI spans are created
	require.NotNil(t, ciSpan1)
	require.NotNil(t, ciSpan2)

	// Verify non-CI spans are nil
	assert.Nil(t, nonCISpan1)
	assert.Nil(t, nonCISpan2)
	assert.Nil(t, nonCISpan3)

	// Finish CI spans
	ciSpan1.Finish()
	ciSpan2.Finish()

	// Only CI spans should be flushed
	flush(2)
	assert.Equal(t, 2, transport.Len())
}

func TestCIVisibilityTracerRouter_ChildSpanFiltering(t *testing.T) {
	// Test that child spans of CI Visibility spans are also filtered (non-CI children return nil)
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	wrapped := wrapWithCIVisibilityTracerRouter(tr)

	// Create a CI Visibility parent span
	parentSpan := wrapped.StartSpan("parent.operation", SpanType(constants.SpanTypeTest))
	require.NotNil(t, parentSpan)

	// Try to create a non-CI Visibility child span - should return nil
	// because the CIVisibilityTracerRouter filters based on span type, not parent relationship
	childSpan := wrapped.StartSpan("child.operation",
		ChildOf(parentSpan.Context()),
		SpanType("web"),
	)
	assert.Nil(t, childSpan, "non-CI child spans should return nil")

	// Create a CI Visibility child span - should work
	ciChildSpan := wrapped.StartSpan("ci.child.operation",
		ChildOf(parentSpan.Context()),
		SpanType(constants.SpanTypeTest),
	)
	require.NotNil(t, ciChildSpan)

	ciChildSpan.Finish()
	parentSpan.Finish()
}
