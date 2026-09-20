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

	"github.com/DataDog/dd-trace-go/v2/ddtrace/internal"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility"
	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	internaltelemetry "github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
	detached            tracer.Tracer
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
func (t *ciVisibilityRouterAdapter) DetachMockTracer(tracer.Tracer) (tracer.Tracer, bool) {
	if t.detached != nil {
		return t.detached, true
	}
	return t, true
}
func (t *ciVisibilityRouterAdapter) SetApplicationTracer(tracer.Tracer) bool {
	t.setApplicationCount.Add(1)
	return true
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

func (nilSpanTracer) Reset()                                                   {}
func (nilSpanTracer) StartSpan(string, ...tracer.StartSpanOption) *tracer.Span { return nil }
func (nilSpanTracer) Extract(any) (*tracer.SpanContext, error)                 { return nil, nil }
func (nilSpanTracer) Inject(*tracer.SpanContext, any) error                    { return nil }
func (nilSpanTracer) TracerConf() tracer.TracerConf                            { return tracer.TracerConf{} }
func (nilSpanTracer) Flush()                                                   {}
func (nilSpanTracer) Stop()                                                    {}
func (nilSpanTracer) SetMockTracer(tracer.Tracer) bool                         { return true }
func (nilSpanTracer) ClearMockTracer(tracer.Tracer) bool                       { return true }
func (t nilSpanTracer) DetachMockTracer(tracer.Tracer) (tracer.Tracer, bool)   { return t, true }
func (nilSpanTracer) SetApplicationTracer(tracer.Tracer) bool                  { return true }
func (nilSpanTracer) TracerForTrace(string, string) tracer.Tracer              { return nil }

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

func TestCIVisibilityMockTracer_InstallsRouterGloballyWhenCIStartsAfterMockTracer(t *testing.T) {
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
			require.Same(t, tracer.Tracer(router), getGlobalTracer())
			require.NotSame(t, mt, getGlobalTracer())

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
			require.Same(t, tracer.Tracer(router), getGlobalTracer())

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

func TestCIVisibilityMockTracer_RepeatedTracerStartsKeepMockRouting(t *testing.T) {
	setupCIVisibilityMockTracerIntegrationTest(t, true)

	mt := Start()
	t.Cleanup(mt.Stop)
	cmt, ok := mt.(*civisibilitymocktracer)
	require.True(t, ok)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")

	require.Same(t, tracer.Tracer(cmt.currentRouter()), getGlobalTracer())
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

func TestCIVisibilityMockTracer_ReplacingOldMockDelegateInstallsRouterGlobally(t *testing.T) {
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
	require.Same(t, tracer.Tracer(cmt.currentRouter()), getGlobalTracer())
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

func TestCIVisibilityMockTracer_StopStopsTransferredCITracerAfterCIExit(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	civisibility.SetState(civisibility.StateExiting)

	ciTracer := &countingTracer{}
	router := &ciVisibilityRouterAdapter{
		Tracer:   &countingTracer{},
		detached: ciTracer,
	}
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
	router := &ciVisibilityRouterAdapter{
		Tracer:   &countingTracer{},
		detached: ciTracer,
	}
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
