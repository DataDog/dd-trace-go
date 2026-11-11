// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

func TestTryRegisterAgentless_AfterShutdownRegistersNothing(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("valid")

	settings := internalffe.Settings{
		AgentlessBaseURL: backend.server.URL,
		PollInterval:     time.Hour,
		RequestTimeout:   2 * time.Second,
	}
	p := newDatadogProviderWithSourceAndEVP(
		ProviderConfig{},
		internalffe.SourceAgentless,
		newAgentlessEVPClient(settings),
	)
	p.mu.Lock()
	p.shutdownCalled = true
	p.mu.Unlock()

	src, err := newAgentlessSource(settings, p.updateConfiguration)
	require.NoError(t, err)

	assert.False(t, p.tryRegisterAgentless(src))

	p.mu.RLock()
	defer p.mu.RUnlock()
	assert.Nil(t, p.agentless)

	requests, _, _, _ := backend.status()
	assert.Equal(t, 0, requests, "a poller must never be registered, let alone started, after shutdown")
}

func TestStartWithAgentless_ShutdownMidPoll(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("delayed_valid") // 150ms handler sleep

	settings := internalffe.Settings{
		AgentlessBaseURL: backend.server.URL,
		PollInterval:     time.Hour,
		RequestTimeout:   5 * time.Second,
	}

	p, err := startWithAgentless(ProviderConfig{}, settings)
	require.NoError(t, err)

	// start launches the poll in the background and returns immediately, so
	// wait for the backend to actually receive the request before shutting
	// down — otherwise this could pass trivially without exercising a
	// shutdown-mid-poll race at all.
	require.Eventually(t, func() bool {
		requests, _, _, _ := backend.status()
		return requests >= 1
	}, 2*time.Second, time.Millisecond)

	// Shut down while the first poll is still in flight.
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = p.ShutdownWithContext(ctx)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Nil(t, p.getConfiguration())
	// Bounded well under the 5s request timeout rather than near the ~150ms it
	// actually takes, so a loaded runner's scheduling latency can't fail this.
	assert.Less(t, elapsed, 3*time.Second, "Shutdown must not wait out the full request timeout")
}

func TestStartWithAgentless_ConfiguresAgentlessEVP(t *testing.T) {
	t.Setenv(flagEvalCountsEnabledEnvVar, "true")

	backend := newFakeUFCBackend(t)
	backend.setResponses("valid")

	settings := internalffe.Settings{
		AgentlessBaseURL: backend.server.URL,
		APIKey:           "api-key",
		Site:             "datadoghq.eu",
		PollInterval:     time.Hour,
		RequestTimeout:   2 * time.Second,
	}
	p, err := startWithAgentless(ProviderConfig{}, settings)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		require.NoError(t, p.ShutdownWithContext(ctx))
	})

	require.NotNil(t, p.exposureWriter)
	require.NotNil(t, p.flagEvalLoggingWriter)
	require.Same(t, p.exposureWriter.evp, p.flagEvalLoggingWriter.evp)

	evp := p.exposureWriter.evp
	assert.Equal(t, settings.APIKey, evp.apiKey)
	require.NotNil(t, evp.directURL)
	assert.Equal(t, "https://event-platform-intake.datadoghq.eu", evp.directURL.String())
	assert.False(t, evp.fixedLocalRoute)
}

func TestInitWithContext_DeliveryErrFailsFast(t *testing.T) {
	settings := internalffe.Settings{}
	p := newDatadogProviderWithSourceAndEVP(
		ProviderConfig{},
		internalffe.SourceAgentless,
		newAgentlessEVPClient(settings),
	)
	p.mu.Lock()
	p.deliveryErr = errors.New("no API key for managed agentless endpoint")
	p.mu.Unlock()

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err := p.InitWithContext(ctx, openfeature.EvaluationContext{})

	var initErr *openfeature.ProviderInitError
	require.ErrorAs(t, err, &initErr)
	assert.Equal(t, openfeature.ProviderNotReadyCode, initErr.ErrorCode)
	assert.Contains(t, initErr.Message, "no API key for managed agentless endpoint",
		"the cause must reach the SDK caller, not just the log")
	assert.Less(t, time.Since(start), time.Second,
		"a permanent delivery failure must fail Init immediately, not wait out the timeout")
}

func TestNewDatadogProvider_DisabledSourceIsNoop(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false) })
	t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "offline")

	p, err := NewDatadogProvider(ProviderConfig{})

	require.NoError(t, err)
	assert.IsType(t, &openfeature.NoopProvider{}, p,
		"a disabled source must start no delivery source at all")
}

func TestDatadogProvider_ConcurrentLifecycleRace(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("valid")

	settings := internalffe.Settings{
		AgentlessBaseURL: backend.server.URL,
		PollInterval:     5 * time.Millisecond,
		RequestTimeout:   2 * time.Second,
	}
	p, err := startWithAgentless(ProviderConfig{}, settings)
	require.NoError(t, err)

	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			_ = p.InitWithContext(ctx, openfeature.EvaluationContext{})
		})
	}
	for range 8 {
		wg.Go(func() {
			_ = p.BooleanEvaluation(context.Background(), "some-flag", false, nil)
		})
	}

	wg.Wait()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, p.ShutdownWithContext(ctx))
}

// TestInitWithContext_UndeadlinedContextHonorsInitTimeout pins the regression
// that hung the parametric weblog: the OpenFeature SDK's SetProviderAndWait
// calls InitWithContext directly with context.Background(), never Init, so a
// timeout applied only in Init left the caller waiting forever.
func TestInitWithContext_UndeadlinedContextHonorsInitTimeout(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false) })
	t.Setenv("DD_EXPERIMENTAL_FLAGGING_PROVIDER_INITIALIZATION_TIMEOUT_MS", "200")

	p := newDatadogProvider(ProviderConfig{})

	start := time.Now()
	err := runWithDeadline(t, 10*time.Second, func() error {
		return p.InitWithContext(context.Background(), openfeature.EvaluationContext{})
	})
	elapsed := time.Since(start)

	var initErr *openfeature.ProviderInitError
	if assert.ErrorAs(t, err, &initErr) {
		assert.Equal(t, openfeature.ProviderNotReadyCode, initErr.ErrorCode)
	}
	// Guard both sides of the configured timeout: returning immediately and
	// silently substituting the much longer default must both fail this test.
	assert.GreaterOrEqual(t, elapsed, 100*time.Millisecond,
		"initialization must wait for the configured timeout before reporting not ready")
	// A small multiple of the configured 200ms, so a hard-coded longer timeout
	// (the 10s default included) still fails this.
	assert.Less(t, elapsed, 2*time.Second,
		"an undeadlined context must be bounded by the configured init timeout, not a fixed one")
}

// TestSetProviderAndWait_TimeoutThenLateConfigurationTransitionsToReady
// exercises the complete customer-visible lifecycle. SetProviderAndWait must
// not report READY when its wait ends without configuration, while delivery
// must remain registered so a later configuration can recover the provider.
func TestSetProviderAndWait_TimeoutThenLateConfigurationTransitionsToReady(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false) })
	t.Setenv("DD_EXPERIMENTAL_FLAGGING_PROVIDER_INITIALIZATION_TIMEOUT_MS", "200")

	openfeature.Shutdown() // drop providers and handlers left by other tests
	t.Cleanup(openfeature.Shutdown)

	readyEvents := make(chan openfeature.EventDetails, 2)
	readyCallback := func(details openfeature.EventDetails) {
		readyEvents <- details
	}
	openfeature.AddHandler(openfeature.ProviderReady, &readyCallback)

	p := newDatadogProvider(ProviderConfig{})

	err := runWithDeadline(t, 10*time.Second, func() error {
		return openfeature.SetProviderAndWait(p)
	})

	var initErr *openfeature.ProviderInitError
	if assert.ErrorAs(t, err, &initErr) {
		assert.Equal(t, openfeature.ProviderNotReadyCode, initErr.ErrorCode)
	}
	assert.Nil(t, p.getConfiguration())
	assert.NotEqual(t, openfeature.ReadyState, openfeature.NewDefaultClient().State(),
		"SetProviderAndWait must not report READY before the first configuration")

	select {
	case <-readyEvents:
		t.Error("ProviderReady was emitted before the first configuration")
	case <-time.After(50 * time.Millisecond):
	}

	// Delivery continues after the bounded initialization wait. The first late
	// configuration must be observable as the recovery transition to READY.
	p.updateConfiguration(createTestConfig())

	select {
	case <-readyEvents:
	case <-time.After(time.Second):
		t.Fatal("late configuration did not emit ProviderReady")
	}
	assert.Eventually(t, func() bool {
		return openfeature.NewDefaultClient().State() == openfeature.ReadyState
	}, time.Second, time.Millisecond, "late configuration must promote the SDK state to READY")

	result := p.BooleanEvaluation(context.Background(), "bool-flag", false, openfeature.FlattenedContext{
		"targetingKey": "user-123",
		"country":      "US",
	})
	assert.True(t, result.Value, "evaluation must succeed after late readiness recovery")
}

// TestNewDatadogProvider_SendsTracerServiceStartedLater creates the provider
// before tracer.Start, as an application may, and checks that the next poll
// carries the service the tracer resolved, including its inferred default,
// cleaned up into the format the CDN attributes usage to. The tracer's own
// service stays as configured.
func TestNewDatadogProvider_SendsTracerServiceStartedLater(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    []tracer.StartOption
		service string // the tracer's service
		header  string // the DD-Service value sent; "" when none is
	}{
		{name: "configured", opts: []tracer.StartOption{tracer.WithService("checkout")}, service: "checkout", header: "checkout"},
		{name: "inferred", service: filepath.Base(os.Args[0]), header: filepath.Base(os.Args[0])},
		{name: "cleaned up", opts: []tracer.StartOption{tracer.WithService("My Service")}, service: "My Service", header: "my_service"},
		{name: "nothing usable left", opts: []tracer.StartOption{tracer.WithService("支付")}, service: "支付"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Cleanup(func() { internalconfig.CreateNew() })
			t.Setenv("DD_SERVICE", "")
			t.Setenv("DD_API_KEY", "")
			t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
			t.Setenv("DD_REMOTE_CONFIGURATION_ENABLED", "false")
			t.Setenv("DD_RUNTIME_METRICS_ENABLED", "false")
			t.Setenv("DD_METRICS_OTEL_ENABLED", "false")
			t.Setenv("DD_APPSEC_ENABLED", "false")
			agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"endpoints":[]}`))
			}))
			defer agent.Close()
			t.Setenv("DD_TRACE_AGENT_URL", agent.URL)
			backend := newFakeUFCBackend(t)
			backend.setResponses("valid", "not_modified")
			t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE", "agentless")
			t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE_AGENTLESS_BASE_URL", backend.server.URL)
			t.Setenv("DD_FEATURE_FLAGS_CONFIGURATION_SOURCE_AGENTLESS_POLL_INTERVAL_SECONDS", "3600")
			internalconfig.CreateNew()

			provider, err := NewDatadogProvider(ProviderConfig{})
			require.NoError(t, err)
			p := provider.(*DatadogProvider)
			defer p.Shutdown()
			require.Eventually(t, func() bool { return len(backend.headers()) == 1 }, 2*time.Second, time.Millisecond)

			require.NoError(t, tracer.Start(append(tc.opts, tracer.WithLogStartup(false), tracer.WithHTTPClient(agent.Client()))...))
			defer tracer.Stop()

			// Run the next poll now rather than waiting out the poll interval.
			p.mu.RLock()
			src := p.agentless
			p.mu.RUnlock()
			outcome, _ := src.pollOnce(1)
			require.Equal(t, pollOutcomeSuccess, outcome)

			headers := backend.headers()
			require.Len(t, headers, 2)
			assert.Empty(t, headers[0].Values("DD-Service"), "no service is resolved before tracer.Start")
			if tc.header == "" {
				assert.Empty(t, headers[1].Values("DD-Service"))
			} else {
				assert.Equal(t, []string{tc.header}, headers[1].Values("DD-Service"))
			}
			assert.Equal(t, tc.service, internalconfig.Get().ServiceName(), "the tracer's service stays as configured")
		})
	}
}
