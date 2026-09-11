// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

	// Timing out with delivery still running stays a non-error: configuration
	// arriving later promotes the provider to ready.
	assert.NoError(t, err)
	assert.Less(t, time.Since(start), 5*time.Second,
		"an undeadlined context must still be bounded by the configured init timeout")
}

// TestSetProviderAndWait_DoesNotHangWithoutConfiguration exercises the same
// bug through the SDK entry point customers actually call.
func TestSetProviderAndWait_DoesNotHangWithoutConfiguration(t *testing.T) {
	internalconfig.SetUseFreshConfig(true)
	t.Cleanup(func() { internalconfig.SetUseFreshConfig(false) })
	t.Setenv("DD_EXPERIMENTAL_FLAGGING_PROVIDER_INITIALIZATION_TIMEOUT_MS", "200")

	p := newDatadogProvider(ProviderConfig{})

	err := runWithDeadline(t, 10*time.Second, func() error {
		return openfeature.SetProviderAndWait(p)
	})

	assert.NoError(t, err)
}
