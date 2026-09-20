// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type callbackTestTracer struct {
	onStart     func()
	startCount  atomic.Int32
	injectCount atomic.Int32
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
func (t *callbackTestTracer) Inject(*SpanContext, any) error {
	t.injectCount.Add(1)
	return nil
}
func (*callbackTestTracer) TracerConf() TracerConf { return TracerConf{} }
func (*callbackTestTracer) Flush()                 {}
func (t *callbackTestTracer) Stop()                { t.stopCount.Add(1) }

type markerPropagator string

func (p markerPropagator) Inject(_ *SpanContext, carrier any) error {
	writer, ok := carrier.(TextMapWriter)
	if !ok {
		return ErrInvalidCarrier
	}
	writer.Set("x-test-tracer", string(p))
	return nil
}

func (markerPropagator) Extract(any) (*SpanContext, error) { return nil, ErrSpanContextNotFound }

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

	require.True(t, router.ClearMockTracer(mockTracer))
	router.StartSpan("application.restored")
	require.EqualValues(t, 1, applicationTracer.startCount.Load())
}

func TestSpanTypeForRoutingConcurrentWithSetTag(t *testing.T) {
	span := &Span{}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		<-start
		for range 10_000 {
			span.SetTag(ext.SpanType, ext.SpanTypeWeb)
			span.SetTag(ext.SpanType, constants.SpanTypeTest)
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		for range 10_000 {
			_ = span.spanTypeForRouting()
		}
	}()

	close(start)
	wg.Wait()
	assert.Equal(t, constants.SpanTypeTest, span.spanTypeForRouting())
}

func TestConcreteTracerForSpanDoesNotReadMetadataWithoutCIRouter(t *testing.T) {
	globalTracer := &callbackTestTracer{}
	localTrace := newTrace()
	span := &Span{context: &SpanContext{trace: localTrace}}
	span.mu.Lock()
	localTrace.mu.Lock()

	done := make(chan Tracer, 1)
	go func() {
		done <- concreteTracerForSpan(globalTracer, span)
	}()

	select {
	case got := <-done:
		localTrace.mu.Unlock()
		span.mu.Unlock()
		assert.Same(t, globalTracer, got)
	case <-time.After(time.Second):
		localTrace.mu.Unlock()
		span.mu.Unlock()
		t.Fatal("normal tracer routing read span or trace metadata")
	}
}

func TestConcreteTracerForLockedTraceDoesNotReadMetadataWithoutCIRouter(t *testing.T) {
	globalTracer := &callbackTestTracer{}

	got := concreteTracerForLockedTrace(globalTracer, nil, constants.SpanTypeTest)

	assert.Same(t, globalTracer, got)
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
	t.Run("keep router without other delegates", func(t *testing.T) {
		ciTracer := &callbackTestTracer{}
		mockTracer := &callbackTestTracer{}
		router := newCIVisibilityTracerRouter(ciTracer, false)
		require.True(t, router.SetMockTracer(mockTracer))

		replacement, ok := router.DetachMockTracer(mockTracer)

		require.True(t, ok)
		require.Same(t, router, replacement)
		require.Nil(t, router.currentMockTracer())
		require.Same(t, ciTracer, router.ciVisibilityTracer())
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

	// The trace predates routing markers, so its CI span type selects the CI
	// tracer and preserves the existing propagation behavior.
	injectErr := wrapped.Inject(span.Context(), carrier)
	assert.Nil(t, injectErr)
	assert.Equal(t, fmt.Sprint(span.traceID), carrier["x-datadog-trace-id"])
	assert.Equal(t, fmt.Sprint(span.spanID), carrier["x-datadog-parent-id"])

	span.Finish()
}

func TestCIVisibilityTracerRouter_InjectUsesTraceRoutingType(t *testing.T) {
	ciTracer, _ := newUninstalledTestTracer(t, WithPropagator(markerPropagator("ci")), WithSpanPool(false))
	applicationTracer, _ := newUninstalledTestTracer(t, WithPropagator(markerPropagator("application")), WithSpanPool(false))
	mockTracer := &callbackTestTracer{}
	router := newCIVisibilityTracerRouter(ciTracer, false)
	t.Cleanup(func() {
		ciTracer.Stop()
		applicationTracer.Stop()
	})

	ciSpan := router.StartSpan("ci.test", SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpan)
	require.True(t, router.SetApplicationTracer(applicationTracer))
	applicationSpan := router.StartSpan("application.operation")
	require.NotNil(t, applicationSpan)
	require.True(t, router.SetMockTracer(mockTracer))

	applicationCarrier := TextMapCarrier{}
	require.NoError(t, router.Inject(applicationSpan.Context(), applicationCarrier))
	require.Equal(t, "application", applicationCarrier["x-test-tracer"])
	require.Zero(t, mockTracer.injectCount.Load())

	ciCarrier := TextMapCarrier{}
	require.NoError(t, router.Inject(ciSpan.Context(), ciCarrier))
	require.Equal(t, "ci", ciCarrier["x-test-tracer"])
	require.Zero(t, mockTracer.injectCount.Load())

	applicationSpan.Finish()
	ciSpan.Finish()
}

func TestCIVisibilityTracerRouter_InternalMetricsUseActiveOrdinaryTracer(t *testing.T) {
	var ciStatsd, applicationStatsd statsdtest.TestStatsdClient
	ciTracer, _ := newUninstalledTestTracer(t, withStatsdClient(&ciStatsd), WithSpanPool(false))
	applicationTracer, _ := newUninstalledTestTracer(t, withStatsdClient(&applicationStatsd), WithSpanPool(false))
	router := newCIVisibilityTracerRouter(ciTracer, false)
	require.True(t, router.SetApplicationTracer(applicationTracer))
	setGlobalTracer(router)
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
		ciTracer.Stop()
		applicationTracer.Stop()
	})

	reportAPIErrorsMetric(nil, errors.New("application failure"), tracesAPIPath)
	require.Len(t, applicationStatsd.GetCallsByName("datadog.tracer.api.errors"), 1)
	require.Empty(t, ciStatsd.GetCallsByName("datadog.tracer.api.errors"))

	require.Same(t, applicationTracer, router.detachApplicationTracer())
	reportAPIErrorsMetric(nil, errors.New("ci failure"), tracesAPIPath)
	require.Len(t, ciStatsd.GetCallsByName("datadog.tracer.api.errors"), 1)
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

func TestCIVisibilityTracerRouter_RestoresRouterAfterApplicationStop(t *testing.T) {
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
	router, ok := getGlobalTracer().(*ciVisibilityTracerRouter)
	require.True(t, ok)

	Stop()
	require.Same(t, router, getGlobalTracer())
	require.Same(t, ciTracer, router.ciVisibilityTracer())
	require.EqualValues(t, 1, applicationTracer.stopCnt.Load())
}

func TestCIVisibilityTracerRouter_RoutesApplicationAndCISpansAfterApplicationStart(t *testing.T) {
	ciTracer, ciTransport := newUninstalledTestTracer(t)
	firstApplicationTransport := newDummyTransport()
	secondApplicationTransport := newDummyTransport()
	wrapped := newCIVisibilityTracerRouter(ciTracer, false)
	setGlobalTracer(wrapped)
	civisibility.SetState(civisibility.StateInitialized)
	t.Cleanup(func() {
		civisibility.SetState(civisibility.StateExiting)
		Stop()
		civisibility.SetState(civisibility.StateUninitialized)
	})

	ciSpanBeforeApplicationStart := StartSpan("ci.test.before", SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpanBeforeApplicationStart)
	applicationSpanBeforeApplicationStart := StartSpan("application.before")
	require.NotNil(t, applicationSpanBeforeApplicationStart)

	// CI Visibility remains enabled process-wide while application tracing starts.
	// The later Start must disable it only for the new application tracer.
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	t.Setenv("DD_TRACE_ENABLED", "true")
	require.NoError(t, Start(
		withTransport(firstApplicationTransport),
		withNoopStats(),
		WithService("first-application-service"),
		WithHTTPClient(internal.DefaultHTTPClient(defaultHTTPTimeout, true)),
	))
	require.Same(t, wrapped, getGlobalTracer())
	applicationTracer, ok := wrapped.currentApplicationTracer().(*tracer)
	require.True(t, ok)
	require.False(t, applicationTracer.config.internalConfig.CIVisibilityEnabled())
	require.Equal(t, applicationTracer.TracerConf(), wrapped.TracerConf())
	require.Equal(t, "first-application-service", wrapped.TracerConf().ServiceTag)

	ciSpanAfterApplicationStart := StartSpan("ci.test.after", SpanType(constants.SpanTypeTest))
	applicationSpan := StartSpan("http.request", SpanType(ext.SpanTypeWeb))
	require.NotNil(t, ciSpanAfterApplicationStart)
	require.NotNil(t, applicationSpan)

	applicationSpan.Finish()
	applicationSpanBeforeApplicationStart.Finish()
	Stop()
	require.Same(t, wrapped, getGlobalTracer())
	require.Nil(t, wrapped.currentApplicationTracer())

	applicationSpanBetweenStarts := StartSpan("application.between")
	require.NotNil(t, applicationSpanBetweenStarts)
	require.Equal(t, ciVisibilityTracerTypeCIApp, ciVisibilityTracerType(applicationSpanBetweenStarts.context.trace))

	require.NoError(t, Start(
		withTransport(secondApplicationTransport),
		withNoopStats(),
		WithService("second-application-service"),
		WithHTTPClient(internal.DefaultHTTPClient(defaultHTTPTimeout, true)),
	))
	require.Same(t, wrapped, getGlobalTracer())
	secondApplicationSpan := StartSpan("application.second")
	require.NotNil(t, secondApplicationSpan)
	applicationSpanBetweenStarts.Finish()
	secondApplicationSpan.Finish()
	Stop()
	require.Same(t, wrapped, getGlobalTracer())

	ciSpanBeforeApplicationStart.Finish()
	ciSpanAfterApplicationStart.Finish()
	require.Eventually(t, func() bool {
		Flush()
		return ciTransport.Len() == 4 && firstApplicationTransport.Len() == 1 && secondApplicationTransport.Len() == 1
	}, time.Second, 5*time.Millisecond)
}

func TestCIVisibilityTracerRouter_ApplicationSpanUsesApplicationConfig(t *testing.T) {
	previousState := civisibility.GetState()
	ciTracer, _ := newUninstalledTestTracer(
		t,
		WithEnv("ci-env"),
		WithServiceVersion("ci-version"),
		WithSpanPool(false),
	)
	applicationTracer, _ := newUninstalledTestTracer(
		t,
		WithEnv("application-env"),
		WithServiceVersion("application-version"),
		WithSpanPool(false),
	)
	applicationAgentFeatures := applicationTracer.config.agent.load()
	applicationAgentFeatures.metaStructAvailable = true
	applicationTracer.config.agent.store(applicationAgentFeatures)

	router := newCIVisibilityTracerRouter(ciTracer, false)
	require.True(t, router.SetApplicationTracer(applicationTracer))
	setGlobalTracer(router)
	t.Cleanup(func() {
		civisibility.SetState(civisibility.StateExiting)
		setGlobalTracer(&NoopTracer{})
		civisibility.SetState(previousState)
	})

	applicationSpan := StartSpan("application.operation")
	require.NotNil(t, applicationSpan)
	metaStruct := &testMsgpStruct{A: "application"}
	require.True(t, applicationSpan.SetMetaStruct("application-meta", metaStruct))
	require.Same(t, metaStruct, applicationSpan.metaStruct["application-meta"])

	applicationSpan.mu.Lock()
	formattedCh := make(chan string, 1)
	go func() {
		formattedCh <- fmt.Sprintf("%v", applicationSpan)
	}()
	var formatted string
	select {
	case formatted = <-formattedCh:
		applicationSpan.mu.Unlock()
	case <-time.After(time.Second):
		applicationSpan.mu.Unlock()
		t.Fatal("formatting an application span blocked while its span lock was held")
	}
	require.Contains(t, formatted, "dd.env=application-env")
	require.Contains(t, formatted, "dd.version=application-version")
	require.NotContains(t, formatted, "ci-env")
	require.NotContains(t, formatted, "ci-version")
	applicationSpan.Finish()
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

func newUninstalledTestTracer(t testing.TB, opts ...StartOption) (*tracer, *dummyTransport) {
	t.Helper()
	transport := newDummyTransport()
	opts = append([]StartOption{
		withTransport(transport),
		WithHTTPClient(internal.DefaultHTTPClient(defaultHTTPTimeout, true)),
	}, opts...)
	tr, err := newTracer(opts...)
	require.NoError(t, err)
	return tr, transport
}

func TestCIVisibilityTracerRouter_SeparatesCrossTracerParentAndUsesApplicationFinalization(t *testing.T) {
	ciTracer, ciTransport := newUninstalledTestTracer(t, WithService("ci-service"), WithSpanPool(false))
	applicationTracer, applicationTransport := newUninstalledTestTracer(
		t,
		WithService("application-service"),
		WithStatsComputation(true),
		WithSpanPool(false),
	)
	applicationAgentFeatures := applicationTracer.config.agent.load()
	applicationAgentFeatures.Stats = true
	applicationAgentFeatures.DropP0s = true
	applicationTracer.config.agent.store(applicationAgentFeatures)
	router := newCIVisibilityTracerRouter(ciTracer, false)
	require.True(t, router.SetApplicationTracer(applicationTracer))
	setGlobalTracer(router)
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	testSpan := StartSpan("test", SpanType(constants.SpanTypeTest))
	require.NotNil(t, testSpan)
	testSpan.SetTag(ext.ManualKeep, true)
	testSpan.SetBaggageItem("tenant", "acme")
	testSpan.context.errors.Store(1)
	testSpan.context.trace.setTag("trace-tag", "preserved")
	applicationSpan := StartSpan(
		"http.request",
		ChildOf(testSpan.Context()),
		SpanType(ext.SpanTypeWeb),
		Tag(keyMeasured, 1),
		Tag(ext.SpanKind, ext.SpanKindClient),
	)
	require.NotNil(t, applicationSpan)

	assert.Equal(t, testSpan.traceID, applicationSpan.traceID)
	assert.Equal(t, testSpan.spanID, applicationSpan.parentID)
	assert.NotSame(t, testSpan.context.trace, applicationSpan.context.trace)
	assert.Same(t, applicationSpan, applicationSpan.context.trace.root)
	assert.Equal(t, "ci-service", applicationSpan.service)
	assert.Equal(t, "acme", applicationSpan.BaggageItem("tenant"))
	assert.EqualValues(t, 1, applicationSpan.context.errors.Load())
	priority, ok := applicationSpan.context.SamplingPriority()
	require.True(t, ok)
	assert.Equal(t, ext.PriorityUserKeep, priority)

	applicationSpan.Finish()
	baseService, ok := applicationSpan.meta.Get(keyBaseService)
	require.True(t, ok)
	assert.Equal(t, "application-service", baseService)
	assert.NotNil(t, applicationSpan.statSpan)
	testSpan.Finish()

	require.Eventually(t, func() bool {
		router.Flush()
		return ciTransport.Len() == 1 && applicationTransport.Len() == 1
	}, time.Second, 5*time.Millisecond)
	ciTraces := ciTransport.Traces()
	require.Len(t, ciTraces, 1)
	require.Len(t, ciTraces[0], 1)
	assert.Equal(t, "test", ciTraces[0][0].name)
	applicationTraces := applicationTransport.Traces()
	require.Len(t, applicationTraces, 1)
	require.Len(t, applicationTraces[0], 1)
	assert.Equal(t, "http.request", applicationTraces[0][0].name)
	traceTag, ok := applicationTraces[0][0].meta.Get("trace-tag")
	require.True(t, ok)
	assert.Equal(t, "preserved", traceTag)
}

func TestCIVisibilityTracerRouter_ApplicationTraceKeepsTracerTypeWhenMockStarts(t *testing.T) {
	ciTracer, ciTransport := newUninstalledTestTracer(t, WithSpanPool(false))
	applicationTracer, applicationTransport := newUninstalledTestTracer(t, WithSpanPool(false))
	router := newCIVisibilityTracerRouter(ciTracer, false)
	require.True(t, router.SetApplicationTracer(applicationTracer))
	setGlobalTracer(router)
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	applicationSpan := StartSpan("http.request", SpanType(ext.SpanTypeWeb))
	require.NotNil(t, applicationSpan)
	assert.Equal(t, ciVisibilityTracerTypeApplication, ciVisibilityTracerType(applicationSpan.context.trace))

	// Changing the public span.type tag must not change the tracer type selected
	// when the trace was created.
	applicationSpan.SetTag(ext.SpanType, constants.SpanTypeTest)
	require.True(t, router.SetMockTracer(&callbackTestTracer{}))
	applicationSpan.Finish()

	require.Eventually(t, func() bool {
		router.Flush()
		return applicationTransport.Len() == 1
	}, time.Second, 5*time.Millisecond)
	assert.Zero(t, ciTransport.Len())
	traces := applicationTransport.Traces()
	require.Len(t, traces, 1)
	require.Len(t, traces[0], 1)
	tracerType, ok := traces[0][0].meta.Get(ciVisibilityTracerTypeTag)
	require.True(t, ok)
	assert.Equal(t, ciVisibilityTracerTypeApplication, tracerType)
}

func TestCIVisibilityTracerRouter_CIAppTraceStaysOnCITracerWhenApplicationStarts(t *testing.T) {
	ciTracer, ciTransport := newUninstalledTestTracer(t, WithSpanPool(false))
	applicationTracer, applicationTransport := newUninstalledTestTracer(t, WithSpanPool(false))
	router := newCIVisibilityTracerRouter(ciTracer, false)
	setGlobalTracer(router)
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	applicationSpan := StartSpan("http.request", SpanType(ext.SpanTypeWeb))
	require.NotNil(t, applicationSpan)
	assert.Equal(t, ciVisibilityTracerTypeCIApp, ciVisibilityTracerType(applicationSpan.context.trace))

	require.True(t, router.SetApplicationTracer(applicationTracer))
	applicationSpan.Finish()

	require.Eventually(t, func() bool {
		router.Flush()
		return ciTransport.Len() == 1
	}, time.Second, 5*time.Millisecond)
	assert.Zero(t, applicationTransport.Len())
	traces := ciTransport.Traces()
	require.Len(t, traces, 1)
	require.Len(t, traces[0], 1)
	tracerType, ok := traces[0][0].meta.Get(ciVisibilityTracerTypeTag)
	require.True(t, ok)
	assert.Equal(t, ciVisibilityTracerTypeCIApp, tracerType)
}

func TestCIVisibilityTracerRouter_ApplicationTraceUsesCurrentApplicationTracer(t *testing.T) {
	ciTracer, _ := newUninstalledTestTracer(t, WithSpanPool(false))
	firstApplicationTracer, firstApplicationTransport := newUninstalledTestTracer(t, WithSpanPool(false))
	secondApplicationTracer, secondApplicationTransport := newUninstalledTestTracer(t, WithSpanPool(false))
	router := newCIVisibilityTracerRouter(ciTracer, false)
	require.True(t, router.SetApplicationTracer(firstApplicationTracer))
	setGlobalTracer(router)
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	applicationSpan := StartSpan("http.request", SpanType(ext.SpanTypeWeb))
	require.NotNil(t, applicationSpan)
	require.True(t, router.SetApplicationTracer(secondApplicationTracer))
	applicationSpan.Finish()

	require.Eventually(t, func() bool {
		router.Flush()
		return secondApplicationTransport.Len() == 1
	}, time.Second, 5*time.Millisecond)
	assert.Zero(t, firstApplicationTransport.Len())
}

func TestCIVisibilityTracerRouter_TracerTypeSurvivesFinishedPooledParent(t *testing.T) {
	ciTracer, ciTransport := newUninstalledTestTracer(t, WithSpanPool(true))
	applicationTracer, applicationTransport := newUninstalledTestTracer(t, WithSpanPool(false))
	router := newCIVisibilityTracerRouter(ciTracer, false)
	require.True(t, router.SetApplicationTracer(applicationTracer))
	setGlobalTracer(router)
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	testSpan := StartSpan("test", SpanType(constants.SpanTypeTest))
	require.NotNil(t, testSpan)
	parentContext := testSpan.Context()
	parentTrace := parentContext.trace
	traceID := testSpan.traceID
	parentID := testSpan.spanID
	testSpan.Finish()

	require.Eventually(t, func() bool {
		router.Flush()
		return ciTransport.Len() == 1 && testSpan.spanTypeForRouting() == ""
	}, time.Second, 5*time.Millisecond)
	assert.Equal(t, ciVisibilityTracerTypeCIApp, ciVisibilityTracerType(parentTrace))

	applicationSpan := StartSpan(
		"http.request",
		ChildOf(parentContext),
		SpanType(ext.SpanTypeWeb),
	)
	require.NotNil(t, applicationSpan)
	assert.NotSame(t, parentTrace, applicationSpan.context.trace)
	assert.Equal(t, traceID, applicationSpan.traceID)
	assert.Equal(t, parentID, applicationSpan.parentID)
	assert.Equal(t, ciVisibilityTracerTypeApplication, ciVisibilityTracerType(applicationSpan.context.trace))
	applicationSpan.Finish()

	require.Eventually(t, func() bool {
		router.Flush()
		return applicationTransport.Len() == 1
	}, time.Second, 5*time.Millisecond)
}

func TestCIVisibilityTracerRouter_CompletesTraceWhenApplicationDestinationDisappears(t *testing.T) {
	applicationTracer, applicationTransport := newUninstalledTestTracer(t, WithSpanPool(false))
	ciTracer, _ := newUninstalledTestTracer(t, WithSpanPool(false))
	router := wrapWithCIVisibilityTracerRouter(ciTracer)
	require.True(t, router.SetApplicationTracer(applicationTracer))
	setGlobalTracer(router)
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
		applicationTracer.Stop()
	})

	applicationSpan := StartSpan("http.request", SpanType(ext.SpanTypeWeb))
	require.NotNil(t, applicationSpan)
	traceState := applicationSpan.context.trace
	require.Same(t, applicationTracer, router.detachApplicationTracer())

	target := router.TracerForTrace(ciVisibilityTracerTypeApplication, ext.SpanTypeWeb)
	assert.IsType(t, NoopTracer{}, target)

	applicationSpan.Finish()
	traceState.mu.RLock()
	assert.Empty(t, traceState.spans)
	assert.Zero(t, traceState.finished)
	traceState.mu.RUnlock()
	assert.Zero(t, applicationTransport.Len())
}

func TestCIVisibilityTracerRouter_PartialFlushUsesApplicationDestination(t *testing.T) {
	ciTracer, ciTransport := newUninstalledTestTracer(t, WithSpanPool(false))
	applicationTracer, applicationTransport := newUninstalledTestTracer(
		t,
		WithPartialFlushing(1),
		WithSpanPool(false),
	)
	router := newCIVisibilityTracerRouter(ciTracer, false)
	require.True(t, router.SetApplicationTracer(applicationTracer))
	setGlobalTracer(router)
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	testSpan := StartSpan("test", SpanType(constants.SpanTypeTest))
	require.NotNil(t, testSpan)
	testSpan.SetTag(ext.ManualKeep, true)
	applicationRoot := StartSpan(
		"http.request",
		ChildOf(testSpan.Context()),
		SpanType(ext.SpanTypeWeb),
	)
	require.NotNil(t, applicationRoot)
	applicationChild := StartSpan(
		"db.query",
		ChildOf(applicationRoot.Context()),
		SpanType(ext.SpanTypeSQL),
	)
	require.NotNil(t, applicationChild)

	applicationChild.Finish()
	require.Eventually(t, func() bool {
		router.Flush()
		return applicationTransport.Len() == 1
	}, time.Second, 5*time.Millisecond)

	applicationRoot.Finish()
	testSpan.Finish()
	require.Eventually(t, func() bool {
		router.Flush()
		return applicationTransport.Len() == 2 && ciTransport.Len() == 1
	}, time.Second, 5*time.Millisecond)

	applicationTraces := applicationTransport.Traces()
	require.Len(t, applicationTraces, 2)
	applicationNames := make([]string, 0, len(applicationTraces))
	for _, trace := range applicationTraces {
		require.Len(t, trace, 1)
		applicationNames = append(applicationNames, trace[0].name)
	}
	assert.ElementsMatch(t, []string{"db.query", "http.request"}, applicationNames)
	ciTraces := ciTransport.Traces()
	require.Len(t, ciTraces, 1)
	require.Len(t, ciTraces[0], 1)
	assert.Equal(t, "test", ciTraces[0][0].name)
}

func TestCIVisibilityTracerRouter_KeepsCrossTypeSpansTogetherWhenTheyShareCIDestination(t *testing.T) {
	ciTracer, ciTransport := newUninstalledTestTracer(t, WithSpanPool(false))
	router := newCIVisibilityTracerRouter(ciTracer, false)
	setGlobalTracer(router)
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	testSpan := StartSpan("test", SpanType(constants.SpanTypeTest))
	require.NotNil(t, testSpan)
	applicationSpan := StartSpan(
		"http.request",
		ChildOf(testSpan.Context()),
		SpanType(ext.SpanTypeWeb),
	)
	require.NotNil(t, applicationSpan)
	assert.Same(t, testSpan.context.trace, applicationSpan.context.trace)

	applicationSpan.Finish()
	testSpan.Finish()
	require.Eventually(t, func() bool {
		router.Flush()
		return ciTransport.Len() == 1
	}, time.Second, 5*time.Millisecond)
	traces := ciTransport.Traces()
	require.Len(t, traces, 1)
	require.Len(t, traces[0], 2)
}

func TestCIVisibilityTracerRouter_SeparatesCIChildFromApplicationTrace(t *testing.T) {
	ciTracer, ciTransport := newUninstalledTestTracer(t, WithSpanPool(false))
	applicationTracer, applicationTransport := newUninstalledTestTracer(t, WithSpanPool(false))
	router := newCIVisibilityTracerRouter(ciTracer, false)
	require.True(t, router.SetApplicationTracer(applicationTracer))
	setGlobalTracer(router)
	t.Cleanup(func() {
		setGlobalTracer(&NoopTracer{})
	})

	applicationParent := StartSpan("http.request", SpanType(ext.SpanTypeWeb))
	require.NotNil(t, applicationParent)
	applicationParent.SetBaggageItem("tenant", "test")
	applicationChild := StartSpan("db.query", ChildOf(applicationParent.Context()), SpanType(ext.SpanTypeSQL))
	require.NotNil(t, applicationChild)
	ciChild := StartSpan("test", ChildOf(applicationParent.Context()), SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciChild)

	assert.Same(t, applicationParent.context.trace, applicationChild.context.trace)
	assert.NotSame(t, applicationParent.context.trace, ciChild.context.trace)
	assert.Same(t, ciChild, ciChild.context.trace.root)
	assert.Equal(t, applicationParent.traceID, ciChild.traceID)
	assert.Equal(t, applicationParent.spanID, ciChild.parentID)
	assert.Equal(t, "test", ciChild.BaggageItem("tenant"))

	applicationChild.Finish()
	applicationParent.Finish()
	ciChild.Finish()
	require.Eventually(t, func() bool {
		router.Flush()
		return ciTransport.Len() == 1 && applicationTransport.Len() == 1
	}, time.Second, 5*time.Millisecond)
	ciTraces := ciTransport.Traces()
	require.Len(t, ciTraces, 1)
	require.Len(t, ciTraces[0], 1)
	assert.Equal(t, "test", ciTraces[0][0].name)
	applicationTraces := applicationTransport.Traces()
	require.Len(t, applicationTraces, 1)
	require.Len(t, applicationTraces[0], 2)
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
