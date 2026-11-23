// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mocktracer

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	_ "unsafe" // Needed for the private bootstrap entry point used by lifecycle tests.

	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	internaltelemetry "github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

//go:linkname startCIVisibilityForTest github.com/DataDog/dd-trace-go/v2/ddtrace/tracer.startCIVisibility
func startCIVisibilityForTest(opts ...tracer.StartOption) error

func TestCIVisibilityMockTracer_RouterlessStartStopsDisplacedResources(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "parent")
	ignored := goleak.IgnoreCurrent()
	old := newMockTracer()
	t.Cleanup(old.dsmProcessor.Stop)
	internal.SetGlobalTracer(tracer.Tracer(old))
	mt := Start().(*civisibilitymocktracer)
	require.Nil(t, mt.currentRouter())
	mt.Stop()
	require.NoError(t, goleak.Find(ignored))
}

func TestCIVisibilityMockTracer_RepeatedStartClosesOldProcessors(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "parent")
	ignored := goleak.IgnoreCurrent()
	first := Start()
	t.Cleanup(first.Stop)
	second := Start()
	t.Cleanup(second.Stop)
	first.Stop()
	span := tracer.StartSpan("second.mock")
	require.NotNil(t, span)
	span.Finish()
	require.Len(t, second.FinishedSpans(), 1)
	second.Stop()
	require.NoError(t, goleak.Find(ignored))
}

func TestCIVisibilityMockTracer_ApplicationStartsDuringBootstrap(t *testing.T) {
	for _, withMock := range []bool{false, true} {
		t.Run(boolString(withMock), func(t *testing.T) {
			server := setupCIVisibilityMockTracerIntegrationTest(t, true)
			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
			civisibility.SetState(civisibility.StateInitializing)
			var mt Tracer
			if withMock {
				mt = Start()
				t.Cleanup(mt.Stop)
			}
			started := make(chan error, 1)
			go func() { started <- tracer.Start(tracer.WithAgentAddr(server.handler.Listener.Addr().String())) }()
			require.NoError(t, <-started)
			require.NoError(t, startCIVisibilityForTest(tracer.WithTestDefaults(nil)))
			civisibility.SetState(civisibility.StateInitialized)
			if mt != nil {
				require.Same(t, mt, getGlobalTracer())
				mocked := tracer.StartSpan("during-bootstrap.mocked", tracer.ServiceName("explicit-service"))
				require.NotNil(t, mocked)
				mocked.Finish()
				require.Len(t, mt.FinishedSpans(), 1)
				_, exists := mt.FinishedSpans()[0].Tags()["_dd.base_service"]
				require.False(t, exists)
				mt.Stop()
			}
			ci := tracer.StartSpan("during-bootstrap.ci", tracer.SpanType(constants.SpanTypeTest))
			require.NotNil(t, ci)
			ci.Finish()
			app := tracer.StartSpan("during-bootstrap.application")
			require.NotNil(t, app)
			app.Finish()
			tracer.Flush()
			require.Eventually(t, func() bool {
				return server.pathBodyContains("/api/v2/citestcycle", "during-bootstrap.ci") &&
					(server.pathBodyContains("/v0.4/traces", "during-bootstrap.application") || server.pathBodyContains("/v1.0/traces", "during-bootstrap.application"))
			}, 5*time.Second, 10*time.Millisecond)
		})
	}
}

func TestCIVisibilityMockTracer_ConcurrentBootstrapAndMockStart(t *testing.T) {
	server := setupCIVisibilityMockTracerIntegrationTest(t, true)
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	civisibility.SetState(civisibility.StateInitializing)
	first := Start()
	t.Cleanup(first.Stop)
	begin := make(chan struct{})
	bootstrapped := make(chan error, 1)
	started := make(chan Tracer, 1)
	go func() { <-begin; bootstrapped <- startCIVisibilityForTest(tracer.WithTestDefaults(nil)) }()
	go func() { <-begin; started <- Start() }()
	close(begin)
	next := <-started
	t.Cleanup(next.Stop)
	require.NoError(t, <-bootstrapped)
	civisibility.SetState(civisibility.StateInitialized)
	require.Same(t, next, getGlobalTracer())
	ci := tracer.StartSpan("concurrent-bootstrap.ci", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, ci)
	ci.Finish()
	application := tracer.StartSpan("concurrent-bootstrap.mock")
	require.NotNil(t, application)
	application.Finish()
	require.Len(t, next.FinishedSpans(), 1)
	tracer.Flush()
	require.Eventually(t, func() bool {
		return server.pathBodyContains("/api/v2/citestcycle", "concurrent-bootstrap.ci")
	}, 5*time.Second, 10*time.Millisecond)
}

func TestCIVisibilityMockTracer_RouterlessStartStopsDisplacedApplication(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "parent")
	old := &countingTracer{}
	internal.SetGlobalTracer(tracer.Tracer(old))
	mt := Start().(*civisibilitymocktracer)
	t.Cleanup(mt.Stop)
	require.Nil(t, mt.currentRouter())
	require.EqualValues(t, 1, old.stopCount.Load())
}

func TestCIVisibilityMockTracer_StalePlainStopDuringApplicationStart(t *testing.T) {
	server := setupCIVisibilityMockTracerIntegrationTest(t, true)
	old := newMockTracer()
	t.Cleanup(old.dsmProcessor.Stop)
	internal.SetGlobalTracer(tracer.Tracer(old))
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	civisibility.SetState(civisibility.StateInitializing)
	require.NoError(t, startCIVisibilityForTest(tracer.WithTestDefaults(nil)))
	civisibility.SetState(civisibility.StateInitialized)

	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	application := &countingTracer{onStop: func() { close(entered); <-release }}
	router := getGlobalTracer().(interface{ SetApplicationTracer(tracer.Tracer) bool })
	require.True(t, router.SetApplicationTracer(application))
	stopped := make(chan struct{})
	go func() { old.Stop(); close(stopped) }()

	staleStopDetachedApplication := false
	select {
	case <-entered:
		staleStopDetachedApplication = true
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stale mock Stop did not complete")
	}
	started := make(chan error, 1)
	go func() {
		started <- tracer.Start(tracer.WithAgentAddr(server.handler.Listener.Addr().String()))
	}()
	if staleStopDetachedApplication {
		require.NoError(t, <-started)
		unblock()
	} else {
		<-entered
		unblock()
		require.NoError(t, <-started)
	}
	<-stopped
	ci := tracer.StartSpan("ci.after-stale-stop", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, ci)
	ci.Finish()
	app := tracer.StartSpan("app.after-stale-stop")
	require.NotNil(t, app)
	app.Finish()
	tracer.Flush()
	require.Eventually(t, func() bool {
		return server.pathBodyContains("/api/v2/citestcycle", "ci.after-stale-stop") &&
			(server.pathBodyContains("/v0.4/traces", "app.after-stale-stop") || server.pathBodyContains("/v1.0/traces", "app.after-stale-stop"))
	}, 5*time.Second, 10*time.Millisecond)
}

type ciVisibilityMockTracerTestServer struct {
	mu      sync.Mutex
	paths   []string
	bodies  map[string][][]byte
	handler *httptest.Server
}

func newCIVisibilityMockTracerTestServer(t *testing.T) *ciVisibilityMockTracerTestServer {
	t.Helper()

	server := &ciVisibilityMockTracerTestServer{bodies: make(map[string][][]byte)}
	server.handler = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gzipReader, err := gzip.NewReader(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer gzipReader.Close()
			body = gzipReader
		}
		payload, err := io.ReadAll(body)
		_ = r.Body.Close()
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		server.mu.Lock()
		server.paths = append(server.paths, r.URL.Path)
		server.bodies[r.URL.Path] = append(server.bodies[r.URL.Path], payload)
		server.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.handler.Close)
	return server
}

func (s *ciVisibilityMockTracerTestServer) URL() string {
	return s.handler.URL
}

func (s *ciVisibilityMockTracerTestServer) hasPath(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Contains(s.paths, path)
}

func (s *ciVisibilityMockTracerTestServer) pathBodyContains(path, value string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, body := range s.bodies[path] {
		if bytes.Contains(body, []byte(value)) {
			return true
		}
	}
	return false
}

func setupCIVisibilityMockTracerIntegrationTest(t *testing.T, useNoop bool) *ciVisibilityMockTracerTestServer {
	t.Helper()

	resetCIVisibilityMockTracerTestState(t)
	t.Cleanup(internaltelemetry.MockClient(new(telemetrytest.RecordClient)))
	server := newCIVisibilityMockTracerTestServer(t)
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "parent")
	t.Setenv(constants.CIVisibilityUseNoopTracer, boolString(useNoop))
	t.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "true")
	t.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, server.URL())
	t.Setenv(constants.APIKeyEnvironmentVariable, "dummy")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	t.Cleanup(stopCIVisibilityMockTracerIntegrationTest)
	return server
}

// stopCIVisibilityMockTracerIntegrationTest shuts down CI Visibility through the
// same global tracer path used by these tests and waits for pending uploads.
func stopCIVisibilityMockTracerIntegrationTest() {
	civisibility.SetState(civisibility.StateExiting)
	tracer.Stop()
	civisibility.ResetForTesting()
	setGlobalNoopTracer()
}

func boolString(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func requireTestCycleRequest(t *testing.T, server *ciVisibilityMockTracerTestServer) {
	t.Helper()

	stopCIVisibilityMockTracerIntegrationTest()
	require.Eventually(t, func() bool {
		return server.hasPath("/api/v2/citestcycle")
	}, 5*time.Second, 10*time.Millisecond)
}

type countingTracer struct {
	onStop       func()
	startCount   atomic.Int32
	stopCount    atomic.Int32
	extractCount atomic.Int32
	injectCount  atomic.Int32
	flushCount   atomic.Int32
}

func (t *countingTracer) StartSpan(_ string, opts ...tracer.StartSpanOption) *tracer.Span {
	t.startCount.Add(1)
	tracer.NewStartSpanConfig(opts...)
	return nil
}

func (t *countingTracer) Extract(_ any) (*tracer.SpanContext, error) {
	t.extractCount.Add(1)
	return nil, nil
}

func (t *countingTracer) Inject(_ *tracer.SpanContext, _ any) error {
	t.injectCount.Add(1)
	return nil
}

func (t *countingTracer) Stop() {
	t.stopCount.Add(1)
	if t.onStop != nil {
		t.onStop()
	}
}

func (*countingTracer) TracerConf() tracer.TracerConf {
	return tracer.TracerConf{}
}

func (t *countingTracer) Flush() { t.flushCount.Add(1) }

// ciVisibilityRouterAdapter exposes a concrete tracer through the lifecycle
// interface consumed by civisibilitymocktracer. Routing behavior itself is
// covered by the tracer package's tests.
type ciVisibilityRouterAdapter struct {
	tracer.Tracer
	rejectMock          bool
	setMockCount        atomic.Int32
	clearMockCount      atomic.Int32
	setApplicationCount atomic.Int32
}

func (*ciVisibilityRouterAdapter) Reset() {}

func (t *ciVisibilityRouterAdapter) SetMockTracer(tracer.Tracer) bool {
	t.setMockCount.Add(1)
	return !t.rejectMock
}
func (t *ciVisibilityRouterAdapter) ClearMockTracer(tracer.Tracer) bool {
	t.clearMockCount.Add(1)
	return true
}
func (t *ciVisibilityRouterAdapter) SetApplicationTracer(tracer.Tracer) bool {
	t.setApplicationCount.Add(1)
	return true
}

func (t *ciVisibilityRouterAdapter) SwapCIVisibilityTracer(candidate tracer.Tracer) (tracer.Tracer, bool) {
	if router, ok := candidate.(*ciVisibilityRouterAdapter); ok {
		candidate = router.Tracer
	}
	old := t.Tracer
	t.Tracer = candidate
	if old == candidate {
		old = nil
	}
	return old, true
}
func (t *ciVisibilityRouterAdapter) TracerForTrace(string, string) tracer.Tracer {
	return t.Tracer
}
func (t *ciVisibilityRouterAdapter) Stop() {
	if t.Tracer != nil {
		t.Tracer.Stop()
	}
	t.Tracer = &tracer.NoopTracer{}
}

func adaptCIVisibilityRouter(tr tracer.Tracer) tracer.Tracer {
	return &ciVisibilityRouterAdapter{Tracer: tr}
}

type nilSpanTracer struct{}

func (nilSpanTracer) Reset()                                                     {}
func (nilSpanTracer) StartSpan(string, ...tracer.StartSpanOption) *tracer.Span   { return nil }
func (nilSpanTracer) Extract(any) (*tracer.SpanContext, error)                   { return nil, nil }
func (nilSpanTracer) Inject(*tracer.SpanContext, any) error                      { return nil }
func (nilSpanTracer) TracerConf() tracer.TracerConf                              { return tracer.TracerConf{} }
func (nilSpanTracer) Flush()                                                     {}
func (nilSpanTracer) Stop()                                                      {}
func (nilSpanTracer) SetMockTracer(tracer.Tracer) bool                           { return true }
func (nilSpanTracer) ClearMockTracer(tracer.Tracer) bool                         { return true }
func (nilSpanTracer) SetApplicationTracer(tracer.Tracer) bool                    { return true }
func (nilSpanTracer) TracerForTrace(string, string) tracer.Tracer                { return nil }
func (nilSpanTracer) SwapCIVisibilityTracer(tracer.Tracer) (tracer.Tracer, bool) { return nil, true }

// TestCIVisibilityMockTracer_StartSpan_Routing verifies that spans are routed
// correctly based on their SpanType tag. CI Visibility spans should go to the
// real tracer, others to the mock tracer.
func TestCIVisibilityMockTracer_StartSpan_Routing(t *testing.T) {
	server := setupCIVisibilityMockTracerIntegrationTest(t, true)
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	civisibility.SetState(civisibility.StateInitialized)
	mt := Start()
	t.Cleanup(mt.Stop)
	cmt, ok := mt.(*civisibilitymocktracer)
	require.True(t, ok)

	// 1. Regular span (should go to internal mock)
	regSpan := cmt.StartSpan("regular.op")
	require.NotNil(t, regSpan)
	regSpan.Finish()

	// 2. CI Visibility span (should go to the CI tracer)
	ciSpan := cmt.StartSpan("ci.test.op", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpan)
	ciSpan.Finish()

	// Verification
	mockedSpans := cmt.mock.FinishedSpans() // Access internal mock directly for verification
	assert.Len(t, mockedSpans, 1, "Only the regular span should be in the internal mock tracer")
	if len(mockedSpans) == 1 {
		assert.Equal(t, "regular.op", mockedSpans[0].OperationName())
		assert.NotEqual(t, "ci.test.op", mockedSpans[0].OperationName())
	}

	// Check the public FinishedSpans() method also reflects the internal mock
	publicFinished := cmt.FinishedSpans()
	assert.Len(t, publicFinished, 1, "Public FinishedSpans should match internal mock")
	if len(publicFinished) == 1 {
		assert.Equal(t, "regular.op", publicFinished[0].OperationName())
	}

	// Check OpenSpans - should be empty now
	assert.Empty(t, cmt.OpenSpans(), "OpenSpans should be empty after finishing")
	requireTestCycleRequest(t, server)
}

func TestNewCIVisibilityMockTracerReusesRouterFromCurrentHandle(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)

	router := &ciVisibilityRouterAdapter{Tracer: &countingTracer{}}
	current := &civisibilitymocktracer{
		mock:   newMockTracer(),
		router: router,
	}
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](current)

	next := newCIVisibilityMockTracer()
	t.Cleanup(next.Stop)

	assert.Same(t, router, next.currentRouter())
	assert.EqualValues(t, 1, router.setMockCount.Load())
}

// TestCIVisibilityMockTracer_Delegation verifies basic delegation methods.
func TestCIVisibilityMockTracer_Delegation(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	cmt := newCIVisibilityMockTracer()
	t.Cleanup(cmt.Stop)
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)

	// Test Reset
	span1 := cmt.StartSpan("op1")
	spanTracer := cmt.TracerForTrace("", "")
	require.Same(t, cmt.mock, spanTracer)
	span1.Finish()
	assert.Len(t, cmt.FinishedSpans(), 1)
	cmt.Reset()
	assert.Empty(t, cmt.FinishedSpans(), "FinishedSpans should be empty after Reset")
	assert.Empty(t, cmt.OpenSpans(), "OpenSpans should be empty after Reset")

	// Test Open/Finished Spans sequence
	span2 := cmt.StartSpan("op2")
	assert.Len(t, cmt.OpenSpans(), 1, "Should have 1 open span")
	assert.Equal(t, "op2", cmt.OpenSpans()[0].OperationName())
	assert.Empty(t, cmt.FinishedSpans(), "FinishedSpans should be empty while span is open")

	span2.Finish()
	assert.Empty(t, cmt.OpenSpans(), "OpenSpans should be empty after finish")
	assert.Len(t, cmt.FinishedSpans(), 1, "Should have 1 finished span")
	assert.Equal(t, "op2", cmt.FinishedSpans()[0].OperationName())

	carrier := tracer.TextMapCarrier{}
	require.NoError(t, cmt.Inject(span2.Context(), carrier))
	extracted, err := cmt.Extract(carrier)
	require.NoError(t, err)
	require.Equal(t, span2.Context().TraceIDLower(), extracted.TraceIDLower())
	require.Equal(t, span2.Context().SpanID(), extracted.SpanID())
	require.NotNil(t, cmt.GetDataStreamsProcessor())

	assert.False(t, cmt.SetApplicationTracer(&countingTracer{}))

	delegate := &countingTracer{}
	router := &ciVisibilityRouterAdapter{Tracer: delegate}
	require.True(t, cmt.SetCIVisibilityTracer(router))
	require.True(t, cmt.SetApplicationTracer(&countingTracer{}))
	_, err = cmt.Extract(carrier)
	require.NoError(t, err)
	require.NoError(t, cmt.Inject(nil, carrier))
	cmt.Flush()
	assert.EqualValues(t, 1, delegate.extractCount.Load())
	assert.EqualValues(t, 1, delegate.injectCount.Load())
	assert.EqualValues(t, 1, delegate.flushCount.Load())
	assert.EqualValues(t, 1, router.setApplicationCount.Load())
}

func TestCIVisibilityMockTracer_RejectsInvalidOrStoppedCIRouter(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	cmt := newCIVisibilityMockTracer()

	assert.False(t, cmt.SetCIVisibilityTracer(&countingTracer{}))
	rejecting := &ciVisibilityRouterAdapter{Tracer: &countingTracer{}, rejectMock: true}
	assert.False(t, cmt.SetCIVisibilityTracer(rejecting))
	assert.EqualValues(t, 1, rejecting.setMockCount.Load())

	cmt.Stop()
	candidate := &ciVisibilityRouterAdapter{Tracer: &countingTracer{}}
	assert.False(t, cmt.SetCIVisibilityTracer(candidate))
	assert.EqualValues(t, 1, candidate.setMockCount.Load())
	assert.EqualValues(t, 1, candidate.clearMockCount.Load())
}

// TestCIVisibilityMockTracer_Stop verifies that the tracer becomes no-op after Stop.
func TestCIVisibilityMockTracer_Stop(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)

	cmt.Stop() // Stop the tracer

	// Verify isnoop is set (internal check, not strictly necessary but good for understanding)
	assert.True(t, cmt.isnoop.Load(), "isnoop flag should be true after Stop")

	// Verify methods become no-op
	assert.Nil(t, cmt.StartSpan("op.after.stop"), "StartSpan should return nil after Stop")

	ctx, err := cmt.Extract(http.Header{})
	assert.Nil(t, ctx, "Extract should return nil context after Stop")
	assert.NoError(t, err, "Extract should return no error after Stop")

	err = cmt.Inject(nil, http.Header{})
	assert.NoError(t, err, "Inject should return no error after Stop")

	assert.Nil(t, cmt.GetDataStreamsProcessor(), "GetDataStreamsProcessor should return nil after Stop")
	assert.Nil(t, cmt.SentDSMBacklogs(), "SentDSMBacklogs should return nil after Stop")

	// Check span lists are not affected (though Reset would clear them)
	assert.Empty(t, cmt.FinishedSpans(), "FinishedSpans should remain empty")
	assert.Empty(t, cmt.OpenSpans(), "OpenSpans should remain empty")
}

func TestCIVisibilityMockTracer_StartSpanOptionsRunOnce(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)

	var optionCalls atomic.Int32
	span := cmt.StartSpan("application.operation", func(*tracer.StartSpanConfig) {
		optionCalls.Add(1)
	})
	require.NotNil(t, span)
	span.Finish()

	require.EqualValues(t, 1, optionCalls.Load())
	require.Len(t, cmt.FinishedSpans(), 1)
}

func TestCIVisibilityMockTracer_WithoutRouterFiltersCISpans(t *testing.T) {
	for _, exiting := range []bool{false, true} {
		t.Run("exiting="+boolString(exiting), func(t *testing.T) {
			resetCIVisibilityMockTracerTestState(t)
			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, boolString(!exiting))
			if exiting {
				civisibility.SetState(civisibility.StateExiting)
			}
			mt := Start()
			t.Cleanup(mt.Stop)
			require.Nil(t, mt.(*civisibilitymocktracer).currentRouter())
			for _, spanType := range []string{constants.SpanTypeTest, constants.SpanTypeTestSuite, constants.SpanTypeTestModule, constants.SpanTypeTestSession} {
				var calls int
				span := tracer.StartSpan("ci.event", tracer.SpanType(spanType), func(*tracer.StartSpanConfig) { calls++ })
				assert.Nil(t, span, spanType)
				assert.Equal(t, 1, calls)
			}
			span := tracer.StartSpan("application.operation")
			require.NotNil(t, span)
			span.Finish()
			require.Len(t, mt.FinishedSpans(), 1)
			require.Empty(t, mt.OpenSpans())
		})
	}
}

func TestCIVisibilityMockTracer_FinishAfterStop(t *testing.T) {
	for _, direct := range []bool{false, true} {
		t.Run("direct="+boolString(direct), func(t *testing.T) {
			resetCIVisibilityMockTracerTestState(t)
			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
			mt := Start()
			t.Cleanup(mt.Stop)
			span := mt.StartSpan("application.operation")
			require.NotNil(t, span)
			mt.Stop()
			if direct {
				mt.FinishSpan(span)
			} else {
				span.Finish()
			}
			require.Empty(t, mt.FinishedSpans())
			require.Len(t, mt.OpenSpans(), 1)
		})
	}
}

type ciVisibilityReentrantSampler struct {
	entered atomic.Bool
	mock    Tracer
	child   *tracer.Span
}

func (s *ciVisibilityReentrantSampler) Sample(parent *tracer.Span) bool {
	if s.entered.CompareAndSwap(false, true) {
		s.mock = Start()
		s.child = tracer.StartSpan("sampler.child", tracer.ChildOf(parent.Context()))
	}
	return true
}

func TestCIVisibilityMockTracer_SamplerStartsMock(t *testing.T) {
	for _, useNoop := range []bool{false, true} {
		t.Run(boolString(useNoop), func(t *testing.T) {
			setupCIVisibilityMockTracerIntegrationTest(t, useNoop)
			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
			require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
			civisibility.SetState(civisibility.StateInitialized)
			sampler := &ciVisibilityReentrantSampler{}
			require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil), tracer.WithSampler(sampler)))
			parent := tracer.StartSpan("application.parent")
			require.NotNil(t, parent)
			require.NotNil(t, sampler.mock)
			t.Cleanup(sampler.mock.Stop)
			require.NotNil(t, sampler.child)
			sampler.child.Finish()
			parent.Finish()
			finished := sampler.mock.FinishedSpans()
			require.Len(t, finished, 1)
			require.Equal(t, "sampler.child", finished[0].OperationName())
			require.Equal(t, parent.Context().SpanID(), finished[0].ParentID())
			require.Equal(t, parent.Context().TraceIDLower(), finished[0].TraceID())
			require.Empty(t, sampler.mock.OpenSpans())
		})
	}
}

func TestCIVisibilityMockTracer_StoppedMockForwardsToPreservedTracer(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	civisibility.SetState(civisibility.StateInitialized)

	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
	real := &countingTracer{}
	require.True(t, cmt.SetCIVisibilityTracer(adaptCIVisibilityRouter(real)))

	cmt.Stop()
	cmt.StartSpan("application.after.mock.stop")

	require.EqualValues(t, 1, real.startCount.Load())
	require.Zero(t, real.stopCount.Load())
}

// TestCIVisibilityMockTracer_Flush verifies that Flush moves open spans to finished.
func TestCIVisibilityMockTracer_Flush(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)

	// Start a regular span (handled by internal mock) but don't finish it
	s := cmt.StartSpan("span.to.flush")
	require.NotNil(t, s)

	// Verify it's in OpenSpans
	open := cmt.OpenSpans()
	require.Len(t, open, 1)
	assert.Equal(t, s.Context().SpanID(), open[0].Context().SpanID())
	assert.Empty(t, cmt.FinishedSpans())

	// Call Flush
	cmt.Flush() // Should flush both mock and real (though we only check mock here)

	// Verify the span moved from Open to Finished in the mock tracer
	assert.Empty(t, cmt.OpenSpans(), "OpenSpans should be empty after Flush")
	finished := cmt.FinishedSpans()
	require.Len(t, finished, 1)
	assert.Equal(t, s.Context().SpanID(), finished[0].Context().SpanID())
	assert.Equal(t, "span.to.flush", finished[0].OperationName())
}

// TestCIVisibilityMockTracer_TracerConf verifies TracerConf delegates correctly.
func TestCIVisibilityMockTracer_TracerConf(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	cmt := newCIVisibilityMockTracer()
	defer cmt.Stop()

	conf := cmt.TracerConf()
	// The default mock tracer has an empty config, so we check that
	assert.Equal(t, tracer.TracerConf{}, conf)
}

// TestCIVisibilityMockTracer_SentDSMBacklogs tests DSM backlog retrieval.
func TestCIVisibilityMockTracer_SentDSMBacklogs(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	cmt := newCIVisibilityMockTracer()
	defer cmt.Stop()

	// Initially, no backlogs
	backlogs := cmt.SentDSMBacklogs()
	assert.Empty(t, backlogs)

	// Simulate some DSM activity (indirectly, as direct simulation is complex)
	// For now, we know the mockDSMTransport starts empty, and flushing doesn't add
	// without pathway activity, so this test mainly ensures the method doesn't panic
	// and returns the expected (empty) list from the internal mock transport.
	cmt.Flush() // Flush includes DSM flush

	backlogs = cmt.SentDSMBacklogs() // Flushes again internally
	assert.Empty(t, backlogs)        // Still expect empty unless DSM was used

	// Test after stop
	cmt.Stop()
	assert.Nil(t, cmt.SentDSMBacklogs(), "Should return nil after stop")
}

func TestCIVisibilityMockTracer_KeepsMockHandleGlobalWhenCIStartsAfterMockTracer(t *testing.T) {
	for _, useNoop := range []bool{true, false} {
		t.Run(boolString(useNoop), func(t *testing.T) {
			server := setupCIVisibilityMockTracerIntegrationTest(t, useNoop)

			mt := Start()
			t.Cleanup(mt.Stop)
			cmt, ok := mt.(*civisibilitymocktracer)
			require.True(t, ok)

			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
			require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")

			router := cmt.currentRouter()
			require.NotNil(t, router)
			require.Same(t, mt, getGlobalTracer())

			normalSpan := tracer.StartSpan("app.operation")
			require.NotNil(t, normalSpan)
			normalSpan.Finish()
			require.Len(t, mt.FinishedSpans(), 1)
			assert.Equal(t, "app.operation", mt.FinishedSpans()[0].OperationName())

			ciSpan := tracer.StartSpan("ci.test", tracer.SpanType(constants.SpanTypeTest))
			require.NotNil(t, ciSpan)
			ciSpan.Finish()
			assert.Len(t, mt.FinishedSpans(), 1)

			tracer.Flush()
			requireTestCycleRequest(t, server)
		})
	}
}

func TestCIVisibilityMockTracer_WrapsAlreadyStartedCIVisibilityTracer(t *testing.T) {
	for _, useNoop := range []bool{false, true} {
		t.Run(boolString(useNoop), func(t *testing.T) {
			server := setupCIVisibilityMockTracerIntegrationTest(t, useNoop)

			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
			require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
			_, ok := getGlobalTracer().(ciVisibilityRouter)
			require.True(t, ok)

			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")
			civisibility.SetState(civisibility.StateInitialized)
			mt := Start()
			cmt, ok := mt.(*civisibilitymocktracer)
			require.True(t, ok)
			router := cmt.currentRouter()
			require.NotNil(t, router)
			require.Same(t, mt, getGlobalTracer())

			normalSpan := tracer.StartSpan("app.operation")
			require.NotNil(t, normalSpan)
			normalSpan.Finish()
			require.Len(t, mt.FinishedSpans(), 1)

			ciSpan := tracer.StartSpan("ci.test", tracer.SpanType(constants.SpanTypeTest))
			require.NotNil(t, ciSpan)
			ciSpan.Finish()
			assert.Len(t, mt.FinishedSpans(), 1)

			mt.Stop()
			require.Same(t, tracer.Tracer(router), getGlobalTracer())

			tracer.Flush()
			requireTestCycleRequest(t, server)
		})
	}
}

func TestCIVisibilityMockTracer_PreservesLegacyFinishContract(t *testing.T) {
	for _, useNoop := range []bool{false, true} {
		t.Run(boolString(useNoop), func(t *testing.T) {
			setupCIVisibilityMockTracerIntegrationTest(t, useNoop)

			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
			require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")
			civisibility.SetState(civisibility.StateInitialized)

			mt := Start()
			t.Cleanup(mt.Stop)
			span := tracer.StartSpan("legacy.application")
			require.NotNil(t, span)

			require.NotPanics(t, func() {
				global := getGlobalTracer().(Tracer)
				global.FinishSpan(span)
				span.Finish()
			})
			require.Len(t, mt.FinishedSpans(), 1)
		})
	}
}

func TestCIVisibilityMockTracer_StoppingStaleMockKeepsActiveMock(t *testing.T) {
	for _, useNoop := range []bool{false, true} {
		t.Run(boolString(useNoop), func(t *testing.T) {
			server := setupCIVisibilityMockTracerIntegrationTest(t, useNoop)
			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
			require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
			civisibility.SetState(civisibility.StateInitialized)
			router := getGlobalTracer()

			first := Start()
			t.Cleanup(first.Stop)
			second := Start()
			t.Cleanup(second.Stop)
			first.Stop()
			first.Stop()

			require.Same(t, second, getGlobalTracer())
			require.Same(t, router, tracer.Tracer(second.(*civisibilitymocktracer).currentRouter()))
			span := tracer.StartSpan("application.active-mock")
			require.NotNil(t, span)
			span.Finish()
			require.Empty(t, first.FinishedSpans())
			require.Len(t, second.FinishedSpans(), 1)

			ciSpan := tracer.StartSpan("ci.after.stale-mock.stop", tracer.SpanType(constants.SpanTypeTest))
			require.NotNil(t, ciSpan)
			ciSpan.Finish()
			require.Len(t, second.FinishedSpans(), 1)
			requireTestCycleRequest(t, server)
		})
	}
}

type blockingClearMockTracerRouter struct {
	ciVisibilityRouter
	onClear func()
}

func (r *blockingClearMockTracerRouter) ClearMockTracer(mock tracer.Tracer) bool {
	cleared := r.ciVisibilityRouter.ClearMockTracer(mock)
	r.onClear()
	return cleared
}

func TestCIVisibilityMockTracer_ConcurrentStopKeepsNewMockGlobal(t *testing.T) {
	for _, useNoop := range []bool{false, true} {
		t.Run(boolString(useNoop), func(t *testing.T) {
			server := setupCIVisibilityMockTracerIntegrationTest(t, useNoop)
			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
			require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
			civisibility.SetState(civisibility.StateInitialized)

			first := Start().(*civisibilitymocktracer)
			cleared := make(chan struct{})
			release := make(chan struct{})
			stopped := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			// Pause after clearing the real delegate, before Stop restores the global tracer.
			router := &blockingClearMockTracerRouter{
				ciVisibilityRouter: first.currentRouter(),
				onClear: sync.OnceFunc(func() {
					close(cleared)
					<-release
				}),
			}
			first.routerMu.Lock()
			first.router = router
			first.routerMu.Unlock()
			go func() {
				first.Stop()
				close(stopped)
			}()
			defer func() {
				unblock()
				<-stopped
			}()
			select {
			case <-cleared:
			case <-time.After(5 * time.Second):
				t.Fatal("mock shutdown did not clear its delegate")
			}

			second := Start()
			t.Cleanup(second.Stop)
			require.Same(t, second, getGlobalTracer())
			unblock()
			<-stopped
			require.Same(t, second, getGlobalTracer(), "old mock Stop must not overwrite the new handle")

			span := tracer.StartSpan("application.new-mock")
			require.NotNil(t, span)
			require.NotPanics(t, func() {
				getGlobalTracer().(Tracer).FinishSpan(span)
				span.Finish()
			})
			require.Len(t, second.FinishedSpans(), 1)
			ciSpan := tracer.StartSpan("ci.after.concurrent-mock-stop", tracer.SpanType(constants.SpanTypeTest))
			require.NotNil(t, ciSpan)
			ciSpan.Finish()
			requireTestCycleRequest(t, server)
		})
	}
}

func TestCIVisibilityMockTracer_RoutesSpansStartedBeforeMockTracerToCITracer(t *testing.T) {
	server := setupCIVisibilityMockTracerIntegrationTest(t, false)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	ciSpan := tracer.StartSpan("ci.test", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpan)
	applicationSpan := tracer.StartSpan("application.before-mock")
	require.NotNil(t, applicationSpan)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")
	civisibility.SetState(civisibility.StateInitialized)
	mt := Start()
	t.Cleanup(mt.Stop)
	_, ok := mt.(*civisibilitymocktracer)
	require.True(t, ok)

	ciSpan.Finish()
	applicationSpan.Finish()
	assert.Empty(t, mt.FinishedSpans())

	tracer.Flush()
	requireTestCycleRequest(t, server)
}

func TestCIVisibilityMockTracer_RoutesFinishedCISpanOutsideMock(t *testing.T) {
	server := setupCIVisibilityMockTracerIntegrationTest(t, true)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")
	civisibility.SetState(civisibility.StateInitialized)
	mt := Start().(*civisibilitymocktracer)
	t.Cleanup(mt.Stop)

	ciSpan := tracer.StartSpan("ci.test", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpan)
	ciSpan.Finish()
	tracer.Flush()

	// The router classifies the finished CI span without retaining it in a
	// wrapper-side registry. The CI event must not leak into mock state.
	require.Empty(t, mt.OpenSpans())
	require.Empty(t, mt.FinishedSpans())
	requireTestCycleRequest(t, server)
}

func TestCIVisibilityMockTracer_StoppedMockTracerIsNotPreserved(t *testing.T) {
	setupCIVisibilityMockTracerIntegrationTest(t, true)

	mt := Start()
	cmt, ok := mt.(*civisibilitymocktracer)
	require.True(t, ok)
	previousReal := &countingTracer{}
	require.True(t, cmt.SetCIVisibilityTracer(adaptCIVisibilityRouter(previousReal)))

	mt.Stop()
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))

	if got := getGlobalTracer(); got == cmt {
		t.Fatal("stopped CI Visibility mock tracer was preserved as the global tracer")
	}
	assert.Equal(t, int32(1), previousReal.stopCount.Load())
}

func TestCIVisibilityMockTracer_StoppedMockTracerPreservesCIThroughApplicationTracerLifecycle(t *testing.T) {
	server := setupCIVisibilityMockTracerIntegrationTest(t, true)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	ciTracer := getGlobalTracer()
	ciSpan := tracer.StartSpan("ci.before.application", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpan)

	civisibility.SetState(civisibility.StateInitialized)
	mt := Start()
	cmt, ok := mt.(*civisibilitymocktracer)
	require.True(t, ok)
	mt.Stop()

	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	applicationSpan := tracer.StartSpan("application.after.mock.stop")
	require.NotNil(t, applicationSpan)
	applicationSpan.Finish()
	tracer.Stop()

	if got := getGlobalTracer(); got != ciTracer {
		t.Fatalf("global tracer = %T, want restored CI Visibility tracer %T", got, ciTracer)
	}

	ciSpan.Finish()
	ciSpanAfterApplication := tracer.StartSpan("ci.after.application", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpanAfterApplication)
	ciSpanAfterApplication.Finish()
	tracer.Flush()

	assert.True(t, cmt.isnoop.Load())
	requireTestCycleRequest(t, server)
}

func TestCIVisibilityMockTracer_ReplacingRealTracerStopsPreviousDelegate(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)

	cmt := newCIVisibilityMockTracer()
	t.Cleanup(cmt.Stop)
	firstReal := &countingTracer{}
	secondReal := &countingTracer{}

	require.True(t, cmt.SetCIVisibilityTracer(adaptCIVisibilityRouter(firstReal)))
	require.True(t, cmt.SetCIVisibilityTracer(adaptCIVisibilityRouter(secondReal)))

	assert.Equal(t, int32(1), firstReal.stopCount.Load())
	assert.Equal(t, int32(0), secondReal.stopCount.Load())
	cmt.StartSpan("ci.test", tracer.SpanType(constants.SpanTypeTest))
	assert.EqualValues(t, 1, secondReal.startCount.Load())
}

func TestCIVisibilityMockTracer_AdoptionAndStopOwnership(t *testing.T) {
	t.Run("adoption before stop", func(t *testing.T) {
		resetCIVisibilityMockTracerTestState(t)
		civisibility.SetState(civisibility.StateInitializing)
		cmt := newCIVisibilityMockTracer()
		t.Cleanup(cmt.Stop)
		internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
		old := &countingTracer{}
		require.True(t, cmt.SetCIVisibilityTracer(adaptCIVisibilityRouter(old)))
		ownedRouter := cmt.currentRouter()
		next := &countingTracer{}
		require.True(t, cmt.SetCIVisibilityTracer(adaptCIVisibilityRouter(next)))
		require.Same(t, ownedRouter, cmt.currentRouter())
		cmt.Stop()
		require.Same(t, tracer.Tracer(ownedRouter), getGlobalTracer())
		require.EqualValues(t, 1, old.stopCount.Load())
		require.Zero(t, next.stopCount.Load())
	})
	t.Run("adoption after stop captures router", func(t *testing.T) {
		resetCIVisibilityMockTracerTestState(t)
		civisibility.SetState(civisibility.StateInitializing)
		cmt := newCIVisibilityMockTracer()
		t.Cleanup(cmt.Stop)
		internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
		old := &countingTracer{}
		require.True(t, cmt.SetCIVisibilityTracer(adaptCIVisibilityRouter(old)))
		// Pause restoration after Stop has captured its router. Adoption must
		// reject this handle, rather than replace the captured router.
		cleared := make(chan struct{})
		unblock := make(chan struct{})
		release := sync.OnceFunc(func() { close(unblock) })
		t.Cleanup(release)
		ownedRouter := &blockingClearMockTracerRouter{
			ciVisibilityRouter: cmt.currentRouter(),
			onClear:            sync.OnceFunc(func() { close(cleared); <-unblock }),
		}
		cmt.routerMu.Lock()
		cmt.router = ownedRouter
		cmt.routerMu.Unlock()
		stopped := make(chan struct{})
		go func() { cmt.Stop(); close(stopped) }()
		select {
		case <-cleared:
		case <-time.After(time.Second):
			t.Fatal("mock Stop did not capture its router")
		}
		next := &countingTracer{}
		candidate := adaptCIVisibilityRouter(next).(*ciVisibilityRouterAdapter)
		require.False(t, cmt.SetCIVisibilityTracer(candidate))
		require.Same(t, ownedRouter, cmt.currentRouter())
		require.EqualValues(t, 1, candidate.clearMockCount.Load())
		release()
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Fatal("mock Stop did not finish")
		}
		require.Same(t, tracer.Tracer(ownedRouter), getGlobalTracer())
		require.Zero(t, old.stopCount.Load())
		require.Zero(t, next.stopCount.Load())
	})
}

func TestCIVisibilityMockTracer_DelegateStopCanStartAnotherMock(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	civisibility.SetState(civisibility.StateInitializing)
	first := Start().(*civisibilitymocktracer)
	t.Cleanup(first.Stop)
	previous := &countingTracer{}
	require.True(t, first.SetCIVisibilityTracer(adaptCIVisibilityRouter(previous)))
	ownedRouter := first.currentRouter()
	var second Tracer
	previous.onStop = func() { second = Start() }
	next := &countingTracer{}
	require.True(t, first.SetCIVisibilityTracer(adaptCIVisibilityRouter(next)))
	require.NotNil(t, second)
	t.Cleanup(second.Stop)
	require.Same(t, second, getGlobalTracer())
	require.Same(t, ownedRouter, second.(*civisibilitymocktracer).currentRouter())
	require.EqualValues(t, 1, previous.stopCount.Load())
	require.Zero(t, next.stopCount.Load())
}

func TestCIVisibilityMockTracer_RepeatedTracerStartsKeepMockRouting(t *testing.T) {
	setupCIVisibilityMockTracerIntegrationTest(t, true)

	mt := Start()
	t.Cleanup(mt.Stop)
	_, ok := mt.(*civisibilitymocktracer)
	require.True(t, ok)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")

	require.Same(t, mt, getGlobalTracer())
	normalSpan := tracer.StartSpan("app.operation")
	require.NotNil(t, normalSpan)
	normalSpan.Finish()
	require.Len(t, mt.FinishedSpans(), 1)

	ciSpan := tracer.StartSpan("ci.test", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpan)
	ciSpan.Finish()
	assert.Len(t, mt.FinishedSpans(), 1)
}

func TestCIVisibilityMockTracer_EarlyRealSpanFinishKeepsSubmitRouting(t *testing.T) {
	server := setupCIVisibilityMockTracerIntegrationTest(t, true)

	mt := Start()
	t.Cleanup(mt.Stop)
	_, ok := mt.(*civisibilitymocktracer)
	require.True(t, ok)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))

	parent := tracer.StartSpan("ci.parent", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, parent)
	child := tracer.StartSpan("ci.child", tracer.SpanType(constants.SpanTypeTest), tracer.ChildOf(parent.Context()))
	require.NotNil(t, child)

	child.Finish()
	assert.Empty(t, mt.FinishedSpans())

	parent.Finish()
	tracer.Flush()
	requireTestCycleRequest(t, server)
}

func TestCIVisibilityMockTracer_ReplacingOldMockDelegateKeepsHandleGlobal(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "parent")

	oldMock := newMockTracer()
	t.Cleanup(func() {
		oldMock.dsmProcessor.Stop()
	})
	internal.SetGlobalTracer(tracer.Tracer(oldMock))

	cmt := newCIVisibilityMockTracer()
	t.Cleanup(cmt.Stop)
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)

	fakeReal := &countingTracer{}
	require.True(t, cmt.SetCIVisibilityTracer(adaptCIVisibilityRouter(fakeReal)))
	require.Same(t, cmt, getGlobalTracer())
	cmt.StartSpan("ci.test", tracer.SpanType(constants.SpanTypeTest))
	assert.EqualValues(t, 1, fakeReal.startCount.Load())
}

func TestCIVisibilityMockTracer_HandlesNilCIDelegateSpan(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)

	cmt := newCIVisibilityMockTracer()
	t.Cleanup(cmt.Stop)
	require.True(t, cmt.SetCIVisibilityTracer(nilSpanTracer{}))

	ciSpan := cmt.StartSpan("ci.test", tracer.SpanType(constants.SpanTypeTest))
	require.Nil(t, ciSpan)

	assert.Empty(t, cmt.OpenSpans())
	assert.Empty(t, cmt.FinishedSpans())
	require.NotPanics(t, func() {
		cmt.FinishSpan(nil)
	})
}

func TestCIVisibilityMockTracer_ResetDoesNotCaptureInFlightCISpan(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)

	cmt := newCIVisibilityMockTracer()
	t.Cleanup(cmt.Stop)
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
	realMock := newMockTracer()
	t.Cleanup(func() {
		realMock.dsmProcessor.Stop()
	})
	require.True(t, cmt.SetCIVisibilityTracer(adaptCIVisibilityRouter(realMock)))

	ciSpan := cmt.StartSpan("ci.test", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpan)
	cmt.Reset()
	ciSpan.Finish()

	assert.Empty(t, cmt.FinishedSpans())
}

func TestCIVisibilityMockTracer_SecondStopDuringCIVisibilityExitStopsRealTracer(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	civisibility.SetState(civisibility.StateInitialized)

	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
	real := &countingTracer{}
	require.True(t, cmt.SetCIVisibilityTracer(adaptCIVisibilityRouter(real)))

	cmt.Stop()
	assert.Equal(t, int32(0), real.stopCount.Load())

	civisibility.SetState(civisibility.StateExiting)
	tracer.Stop()
	assert.Equal(t, int32(1), real.stopCount.Load())
}

func TestCIVisibilityMockTracer_GlobalStopStopsRealTracerDelegate(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)

	cmt := newCIVisibilityMockTracer()
	t.Cleanup(cmt.Stop)
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
	real := &countingTracer{}
	require.True(t, cmt.SetCIVisibilityTracer(adaptCIVisibilityRouter(real)))

	tracer.Stop()

	assert.Equal(t, int32(1), real.stopCount.Load())
	assert.Nil(t, cmt.StartSpan("ci.after.stop", tracer.SpanType(constants.SpanTypeTest)))
}

func TestCIVisibilityMockTracer_StopStopsCIRouterAfterCIExit(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	civisibility.SetState(civisibility.StateExiting)

	ciTracer := &countingTracer{}
	router := &ciVisibilityRouterAdapter{Tracer: ciTracer}
	cmt := &civisibilitymocktracer{
		mock:   newMockTracer(),
		router: router,
	}
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)

	cmt.Stop()

	assert.EqualValues(t, 1, ciTracer.stopCount.Load())
	assert.IsType(t, &tracer.NoopTracer{}, getGlobalTracer())
}

func TestCIVisibilityMockTracer_StopRestoresCIRouterWhileCIIsActive(t *testing.T) {
	server := setupCIVisibilityMockTracerIntegrationTest(t, false)

	mt := Start()
	t.Cleanup(mt.Stop)
	cmt, ok := mt.(*civisibilitymocktracer)
	require.True(t, ok)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	civisibility.SetState(civisibility.StateInitialized)

	tracer.Stop()
	require.NotSame(t, cmt, getGlobalTracer())
	require.Same(t, tracer.Tracer(cmt.currentRouter()), getGlobalTracer())
	_, isNoop := getGlobalTracer().(*tracer.NoopTracer)
	require.False(t, isNoop)

	spanBetweenMocks := tracer.StartSpan("application.between-mocks")
	require.NotNil(t, spanBetweenMocks)
	secondMock := Start()
	spanBetweenMocks.Finish()
	assert.Empty(t, secondMock.FinishedSpans())
	require.Eventually(t, func() bool {
		tracer.Flush()
		return server.pathBodyContains("/api/v2/citestcycle", "application.between-mocks")
	}, 5*time.Second, 10*time.Millisecond)

	secondMockSpan := tracer.StartSpan("application.second-mock")
	require.NotNil(t, secondMockSpan)
	secondMockSpan.Finish()
	require.Len(t, secondMock.FinishedSpans(), 1)
	secondMock.Stop()
	require.Same(t, tracer.Tracer(cmt.currentRouter()), getGlobalTracer())

	ciSpan := tracer.StartSpan("ci.after.mock.stop", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpan)
	ciSpan.Finish()
	tracer.Flush()
	requireTestCycleRequest(t, server)
}

func TestCIVisibilityMockTracer_StopDoesNotReplaceUnrelatedGlobalTracer(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	civisibility.SetState(civisibility.StateInitialized)

	ciTracer := &countingTracer{}
	router := &ciVisibilityRouterAdapter{Tracer: ciTracer}
	cmt := &civisibilitymocktracer{
		mock:   newMockTracer(),
		router: router,
	}
	unrelated := &countingTracer{}
	internal.SetGlobalTracer(tracer.Tracer(unrelated))

	cmt.Stop()

	assert.Same(t, tracer.Tracer(unrelated), getGlobalTracer())
	assert.EqualValues(t, 1, ciTracer.stopCount.Load())
}

func TestCIVisibilityMockTracer_PackageStopKeepsCIReachableDuringApplicationShutdown(t *testing.T) {
	for _, mockStart := range []string{"none", "before-ci", "after-ci", "during-stop", "replace-during-stop"} {
		t.Run(mockStart, func(t *testing.T) {
			server := setupCIVisibilityMockTracerIntegrationTest(t, true)
			if mockStart == "before-ci" {
				mt := Start()
				t.Cleanup(mt.Stop)
			}
			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
			require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
			civisibility.SetState(civisibility.StateInitialized)
			if mockStart == "after-ci" || mockStart == "replace-during-stop" {
				mt := Start()
				t.Cleanup(mt.Stop)
			}

			stopping := make(chan struct{})
			release := make(chan struct{})
			stopped := make(chan struct{})
			unblock := sync.OnceFunc(func() { close(release) })
			application := &countingTracer{onStop: func() {
				close(stopping)
				<-release
			}}
			setter, ok := getGlobalTracer().(interface{ SetApplicationTracer(tracer.Tracer) bool })
			require.True(t, ok)
			require.True(t, setter.SetApplicationTracer(application))
			before := tracer.StartSpan("ci.before.stop", tracer.SpanType(constants.SpanTypeTest))
			require.NotNil(t, before)
			go func() {
				tracer.Stop()
				close(stopped)
			}()
			defer func() {
				unblock()
				<-stopped
			}()
			select {
			case <-stopping:
			case <-time.After(5 * time.Second):
				t.Fatal("application shutdown did not start")
			}

			var newMock Tracer
			if mockStart == "during-stop" || mockStart == "replace-during-stop" {
				newMock = Start()
				t.Cleanup(newMock.Stop)
			}

			before.Finish()
			during := tracer.StartSpan("ci.during.stop", tracer.SpanType(constants.SpanTypeTest))
			require.NotNil(t, during, "CI spans must remain available while application Stop blocks")
			during.Finish()
			require.Eventually(t, func() bool {
				tracer.Flush()
				return server.pathBodyContains("/api/v2/citestcycle", "ci.before.stop") &&
					server.pathBodyContains("/api/v2/citestcycle", "ci.during.stop")
			}, 5*time.Second, 10*time.Millisecond)

			unblock()
			<-stopped
			require.EqualValues(t, 1, application.stopCount.Load())
			after := tracer.StartSpan("ci.after.stop", tracer.SpanType(constants.SpanTypeTest))
			require.NotNil(t, after, "CI spans must remain available after application Stop completes")
			after.Finish()
			require.Eventually(t, func() bool {
				tracer.Flush()
				return server.pathBodyContains("/api/v2/citestcycle", "ci.after.stop")
			}, 5*time.Second, 10*time.Millisecond)
			if newMock != nil {
				span := tracer.StartSpan("application.new-mock")
				require.NotNil(t, span, "application Stop must preserve the mock started during shutdown")
				span.Finish()
				require.Len(t, newMock.FinishedSpans(), 1)
				assert.Equal(t, "application.new-mock", newMock.FinishedSpans()[0].OperationName())
			}
		})
	}
}

func TestCIVisibilityMockTracer_PackageStopDetachesApplicationWhenMockStartedBeforeCI(t *testing.T) {
	setupCIVisibilityMockTracerIntegrationTest(t, true)

	mt := Start()
	t.Cleanup(mt.Stop)
	_, ok := mt.(*civisibilitymocktracer)
	require.True(t, ok)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	civisibility.SetState(civisibility.StateInitialized)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	tracer.Stop()

	if span := tracer.StartSpan("application.after.stop"); span != nil {
		span.Finish()
		t.Fatal("application tracer remained attached after tracer.Stop")
	}
}
