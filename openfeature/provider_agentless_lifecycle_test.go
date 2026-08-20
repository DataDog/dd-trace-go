// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/open-feature/go-sdk/openfeature"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
)

func TestTryRegisterAgentless_AfterShutdownRegistersNothing(t *testing.T) {
	backend := newFakeUFCBackend(t)
	backend.setResponses("valid")

	p := newDatadogProviderWithSource(ProviderConfig{}, internalffe.SourceAgentless)
	p.mu.Lock()
	p.shutdownCalled = true
	p.mu.Unlock()

	src, err := newAgentlessSource(internalffe.Settings{
		AgentlessBaseURL: backend.server.URL,
		PollInterval:     time.Hour,
		RequestTimeout:   2 * time.Second,
	}, p.updateConfiguration)
	require.NoError(t, err)

	assert.False(t, p.tryRegisterAgentless(src))

	p.mu.RLock()
	defer p.mu.RUnlock()
	assert.Nil(t, p.agentless)
	assert.False(t, p.activated)

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

	// Shut down while the first, synchronous poll is still in flight.
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = p.ShutdownWithContext(ctx)
	elapsed := time.Since(start)

	require.NoError(t, err)
	assert.Less(t, elapsed, time.Second, "Shutdown must not wait out the full request timeout")
	assert.Nil(t, p.getConfiguration())
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
