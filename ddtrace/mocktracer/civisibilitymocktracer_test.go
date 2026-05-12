// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package mocktracer

import (
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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ciVisibilityMockTracerTestServer struct {
	mu      sync.Mutex
	paths   []string
	handler *httptest.Server
}

func newCIVisibilityMockTracerTestServer(t *testing.T) *ciVisibilityMockTracerTestServer {
	t.Helper()

	server := new(ciVisibilityMockTracerTestServer)
	server.handler = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		_ = r.Body.Close()

		server.mu.Lock()
		server.paths = append(server.paths, r.URL.Path)
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

func setupCIVisibilityMockTracerIntegrationTest(t *testing.T, useNoop bool) *ciVisibilityMockTracerTestServer {
	t.Helper()

	resetCIVisibilityMockTracerTestState(t)
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
	stopCount atomic.Int32
}

func (*countingTracer) StartSpan(_ string, _ ...tracer.StartSpanOption) *tracer.Span {
	return nil
}

func (*countingTracer) SetServiceInfo(_, _, _ string) {}

func (*countingTracer) Extract(_ any) (*tracer.SpanContext, error) {
	return nil, nil
}

func (*countingTracer) Inject(_ *tracer.SpanContext, _ any) error { return nil }

func (t *countingTracer) Stop() {
	t.stopCount.Add(1)
}

func (*countingTracer) TracerConf() tracer.TracerConf {
	return tracer.TracerConf{}
}

func (*countingTracer) Flush() {}

type nilSpanTracer struct{}

func (nilSpanTracer) StartSpan(string, ...tracer.StartSpanOption) *tracer.Span { return nil }
func (nilSpanTracer) SetServiceInfo(_, _, _ string)                            {}
func (nilSpanTracer) Extract(any) (*tracer.SpanContext, error)                 { return nil, nil }
func (nilSpanTracer) Inject(*tracer.SpanContext, any) error                    { return nil }
func (nilSpanTracer) TracerConf() tracer.TracerConf                            { return tracer.TracerConf{} }
func (nilSpanTracer) Flush()                                                   {}
func (nilSpanTracer) Stop()                                                    {}

// TestCIVisibilityMockTracer_StartSpan_Routing verifies that spans are routed
// correctly based on their SpanType tag. CI Visibility spans should go to the
// real tracer, others to the mock tracer.
func TestCIVisibilityMockTracer_StartSpan_Routing(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	// Note: The 'real' tracer here will be the default global tracer.
	// If mocktracer.Start() was called *before* this, 'real' would also be a mock.
	// We rely on the fact that CI spans won't appear in the *internal* mock tracer (`cmt.mock`).
	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)

	// 1. Regular span (should go to internal mock)
	regSpan := cmt.StartSpan("regular.op")
	require.NotNil(t, regSpan)
	regSpan.Finish()

	// 2. CI Visibility span (should go to real tracer)
	ciSpan := cmt.StartSpan("ci.test.op", tracer.SpanType(constants.SpanTypeTest))
	// We might not have a real tracer configured to actually *do* anything,
	// but the key is it *shouldn't* be handled by cmt.mock.
	// If ciSpan is nil, it means the real tracer is likely a NoopTracer, which is fine for this test.
	if ciSpan != nil {
		ciSpan.Finish() // Finish it if we got one
	}

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
}

// TestCIVisibilityMockTracer_Delegation verifies basic delegation methods.
func TestCIVisibilityMockTracer_Delegation(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)

	// Test Reset
	span1 := cmt.StartSpan("op1")
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

func TestCIVisibilityMockTracer_PreservesGlobalWhenCIStartsAfterMockTracer(t *testing.T) {
	for _, useNoop := range []bool{true, false} {
		t.Run(boolString(useNoop), func(t *testing.T) {
			server := setupCIVisibilityMockTracerIntegrationTest(t, useNoop)

			mt := Start()
			cmt, ok := mt.(*civisibilitymocktracer)
			require.True(t, ok)

			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
			require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
			t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")

			if got := getGlobalTracer(); got != cmt {
				t.Fatalf("global tracer = %T, want preserved CI Visibility mock tracer", got)
			}
			var mockCompatible Tracer
			require.NotPanics(t, func() {
				mockCompatible = getGlobalTracer().(Tracer)
			})
			if mockCompatible != mt {
				t.Fatalf("mock-compatible global tracer = %T, want started mock tracer", mockCompatible)
			}

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
	server := setupCIVisibilityMockTracerIntegrationTest(t, true)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	realBeforeMock := getGlobalTracer()

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")
	civisibility.SetState(civisibility.StateInitialized)
	mt := Start()
	cmt, ok := mt.(*civisibilitymocktracer)
	require.True(t, ok)

	if got := getGlobalTracer(); got != cmt {
		t.Fatalf("global tracer = %T, want CI Visibility mock tracer", got)
	}
	if got := cmt.realTracer(); got != realBeforeMock {
		t.Fatalf("real tracer delegate = %T, want already-started tracer", got)
	}

	normalSpan := tracer.StartSpan("app.operation")
	require.NotNil(t, normalSpan)
	normalSpan.Finish()
	require.Len(t, mt.FinishedSpans(), 1)

	ciSpan := tracer.StartSpan("ci.test", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpan)
	ciSpan.Finish()
	assert.Len(t, mt.FinishedSpans(), 1)

	tracer.Flush()
	requireTestCycleRequest(t, server)
}

func TestCIVisibilityMockTracer_StoppedMockTracerIsNotPreserved(t *testing.T) {
	setupCIVisibilityMockTracerIntegrationTest(t, true)

	mt := Start()
	cmt, ok := mt.(*civisibilitymocktracer)
	require.True(t, ok)
	previousReal := &countingTracer{}
	require.True(t, cmt.SetCIVisibilityTracer(previousReal))

	mt.Stop()
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))

	if got := getGlobalTracer(); got == cmt {
		t.Fatal("stopped CI Visibility mock tracer was preserved as the global tracer")
	}
	assert.Equal(t, int32(1), previousReal.stopCount.Load())
}

func TestCIVisibilityMockTracer_ReplacingRealTracerStopsPreviousDelegate(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)

	cmt := newCIVisibilityMockTracer()
	t.Cleanup(cmt.Stop)
	firstReal := &countingTracer{}
	secondReal := &countingTracer{}

	require.True(t, cmt.SetCIVisibilityTracer(firstReal))
	require.True(t, cmt.SetCIVisibilityTracer(secondReal))

	assert.Equal(t, int32(1), firstReal.stopCount.Load())
	assert.Equal(t, int32(0), secondReal.stopCount.Load())
	if got := cmt.realTracer(); got != secondReal {
		t.Fatalf("real tracer delegate = %T, want second real tracer", got)
	}
}

func TestCIVisibilityMockTracer_RepeatedTracerStartsUpdateRealTracer(t *testing.T) {
	setupCIVisibilityMockTracerIntegrationTest(t, true)

	mt := Start()
	cmt, ok := mt.(*civisibilitymocktracer)
	require.True(t, ok)

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	firstReal := cmt.realTracer()

	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "1")
	require.NoError(t, tracer.Start(tracer.WithTestDefaults(nil)))
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "false")
	secondReal := cmt.realTracer()

	if got := getGlobalTracer(); got != cmt {
		t.Fatalf("global tracer = %T, want preserved CI Visibility mock tracer", got)
	}
	if firstReal == secondReal {
		t.Fatal("real tracer delegate was not replaced after the second tracer start")
	}

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

func TestCIVisibilityMockTracer_ReplacingOldMockDelegateKeepsGlobal(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)
	t.Setenv(constants.CIVisibilityEnabledEnvironmentVariable, "parent")

	oldMock := newMockTracer()
	t.Cleanup(func() {
		oldMock.dsmProcessor.Stop()
	})
	internal.SetGlobalTracer(tracer.Tracer(oldMock))

	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)

	fakeReal := &countingTracer{}
	require.True(t, cmt.SetCIVisibilityTracer(fakeReal))
	if got := getGlobalTracer(); got != cmt {
		t.Fatalf("global tracer = %T, want preserved CI Visibility mock tracer", got)
	}
	if got := cmt.realTracer(); got != fakeReal {
		t.Fatalf("real tracer delegate = %T, want fake real tracer", got)
	}
}

func TestCIVisibilityMockTracer_DoesNotTrackNilRealSpan(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)

	cmt := newCIVisibilityMockTracer()
	t.Cleanup(cmt.Stop)
	require.True(t, cmt.SetCIVisibilityTracer(nilSpanTracer{}))

	ciSpan := cmt.StartSpan("ci.test", tracer.SpanType(constants.SpanTypeTest))
	require.Nil(t, ciSpan)

	cmt.realSpansMu.Lock()
	assert.Empty(t, cmt.realSpans)
	cmt.realSpansMu.Unlock()
	require.NotPanics(t, func() {
		cmt.FinishSpan(nil)
	})
}

func TestCIVisibilityMockTracer_ResetDoesNotReclassifyInFlightCIVisibilitySpans(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)

	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
	realMock := newMockTracer()
	t.Cleanup(func() {
		realMock.dsmProcessor.Stop()
	})
	require.True(t, cmt.SetCIVisibilityTracer(realMock))

	ciSpan := cmt.StartSpan("ci.test", tracer.SpanType(constants.SpanTypeTest))
	require.NotNil(t, ciSpan)

	cmt.Reset()
	ciSpan.Finish()

	assert.Empty(t, cmt.FinishedSpans())
}

func TestCIVisibilityMockTracer_SecondStopDuringCIVisibilityExitStopsRealTracer(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)

	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
	real := &countingTracer{}
	require.True(t, cmt.SetCIVisibilityTracer(real))

	cmt.Stop()
	assert.Equal(t, int32(0), real.stopCount.Load())

	civisibility.SetState(civisibility.StateExiting)
	cmt.Stop()
	assert.Equal(t, int32(1), real.stopCount.Load())
}

func TestCIVisibilityMockTracer_GlobalStopStopsRealTracerDelegate(t *testing.T) {
	resetCIVisibilityMockTracerTestState(t)

	cmt := newCIVisibilityMockTracer()
	internal.StoreGlobalTracer[Tracer, tracer.Tracer](cmt)
	real := &countingTracer{}
	require.True(t, cmt.SetCIVisibilityTracer(real))

	tracer.Stop()

	assert.Equal(t, int32(1), real.stopCount.Load())
	assert.IsType(t, &tracer.NoopTracer{}, cmt.realTracer())
}
