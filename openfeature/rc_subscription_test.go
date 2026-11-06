// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalffe "github.com/DataDog/dd-trace-go/v2/internal/openfeature"
	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

func TestAttachProviderNotSubscribed(t *testing.T) {
	internalffe.ResetForTest()
	defer internalffe.ResetForTest()

	provider := newDatadogProvider(ProviderConfig{})
	got := attachProvider(provider)
	assert.False(t, got, "attachProvider should return false when not subscribed")
}

func TestAttachProviderSubscribed(t *testing.T) {
	internalffe.ResetForTest()
	defer internalffe.ResetForTest()

	internalffe.SetSubscribedForTest(true)

	provider := newDatadogProvider(ProviderConfig{})
	got := attachProvider(provider)
	assert.True(t, got, "attachProvider should return true when subscribed")
}

func TestAttachProviderReplaysBufferedConfig(t *testing.T) {
	internalffe.ResetForTest()
	defer internalffe.ResetForTest()

	// Build a valid config payload.
	config := universalFlagsConfiguration{
		Format:    "SERVER",
		CreatedAt: time.Now(),
		Flags: map[string]*flag{
			"test-flag": {
				Key:           "test-flag",
				Enabled:       true,
				VariationType: valueTypeBoolean,
				Variations: map[string]*variant{
					"on": {Key: "on", Value: true},
				},
				Allocations: []*allocation{},
			},
		},
	}
	data, err := json.Marshal(config)
	require.NoError(t, err)

	// Simulate SubscribeRC + a buffered update arriving before provider exists.
	internalffe.SetSubscribedForTest(true)
	internalffe.SetBufferedForTest(remoteconfig.ProductUpdate{"path/config": data})

	provider := newDatadogProvider(ProviderConfig{})
	got := attachProvider(provider)
	assert.True(t, got)

	// The buffered config should have been replayed into the provider.
	cfg := provider.getConfiguration()
	require.NotNil(t, cfg, "provider should have configuration after replay")
	assert.Contains(t, cfg.Flags, "test-flag")

	// Buffer should be cleared.
	assert.Nil(t, internalffe.GetBufferedForTest(), "buffer should be nil after replay")
}

func TestAttachProviderNoBuffer(t *testing.T) {
	internalffe.ResetForTest()
	defer internalffe.ResetForTest()

	internalffe.SetSubscribedForTest(true)

	provider := newDatadogProvider(ProviderConfig{})
	got := attachProvider(provider)
	assert.True(t, got)

	// Provider should have no configuration.
	cfg := provider.getConfiguration()
	assert.Nil(t, cfg, "provider should have no configuration when nothing was buffered")
}

func TestStartWithRemoteConfigFastPath(t *testing.T) {
	internalffe.ResetForTest()
	defer internalffe.ResetForTest()

	// Simulate tracer having subscribed and buffered a config.
	config := universalFlagsConfiguration{
		Format: "SERVER",
		Flags: map[string]*flag{
			"fast-flag": {
				Key: "fast-flag", Enabled: true, VariationType: valueTypeBoolean,
				Variations:  map[string]*variant{"on": {Key: "on", Value: true}},
				Allocations: []*allocation{},
			},
		},
	}
	data, err := json.Marshal(config)
	require.NoError(t, err)

	internalffe.SetSubscribedForTest(true)
	internalffe.SetBufferedForTest(remoteconfig.ProductUpdate{"path/fast": data})

	// Verify SubscribeProvider returns fast path (tracer subscribed)
	// without touching global remoteconfig state.
	provider := newDatadogProvider(ProviderConfig{})
	tracerOwnsSubscription, token, err := internalffe.SubscribeProvider(provider.rcCallback)
	require.NoError(t, err)
	require.True(t, tracerOwnsSubscription, "SubscribeProvider should report tracer subscribed")
	assert.Zero(t, token)

	// Attach the provider, which replays the buffered config.
	attached := attachProvider(provider)
	require.True(t, attached, "attachProvider should succeed when tracer subscribed")

	cfg := provider.getConfiguration()
	require.NotNil(t, cfg, "provider should have config from fast path replay")
	assert.Contains(t, cfg.Flags, "fast-flag")
}

func TestStartWithRemoteConfig_ConfiguresAgentEVP(t *testing.T) {
	t.Setenv(flagEvalCountsEnabledEnvVar, "true")
	internalffe.ResetForTest()
	defer internalffe.ResetForTest()
	internalffe.SetSubscribedForTest(true)

	provider, err := startWithRemoteConfig(ProviderConfig{})
	require.NoError(t, err)
	require.NotNil(t, provider.exposureWriter)
	require.NotNil(t, provider.flagEvalLoggingWriter)
	require.Same(t, provider.exposureWriter.evp, provider.flagEvalLoggingWriter.evp)

	evp := provider.exposureWriter.evp
	assert.True(t, evp.fixedLocalRoute)
	assert.Equal(t, evpRouteLocal, evp.routeMode)
	assert.Equal(t, evpProxyV2Path, evp.localBase)
	assert.Empty(t, evp.apiKey)
	assert.Nil(t, evp.directURL)
	assert.Nil(t, evp.directClient)
}

func TestRemoteConfigSlowPathShutdownRemovesEvaluatorCapabilities(t *testing.T) {
	internalffe.ResetForTest()
	remoteconfig.Reset()
	t.Cleanup(func() {
		internalffe.ResetForTest()
		remoteconfig.Reset()
	})

	provider, err := startWithRemoteConfig(ProviderConfig{})
	require.NoError(t, err)

	for _, capability := range []remoteconfig.Capability{
		remoteconfig.FFEFlagEvaluation,
		remoteconfig.FFEFlagDependentFlagVariantKeyV1,
	} {
		has, err := remoteconfig.HasCapability(capability)
		require.NoError(t, err)
		require.True(t, has, "slow-path subscription should register capability %d", capability)
	}

	require.NoError(t, provider.ShutdownWithContext(context.Background()))
	hasProduct, err := remoteconfig.HasProduct(internalffe.FFEProductName)
	require.NoError(t, err)
	assert.False(t, hasProduct)
	for _, capability := range []remoteconfig.Capability{
		remoteconfig.FFEFlagEvaluation,
		remoteconfig.FFEFlagDependentFlagVariantKeyV1,
	} {
		has, err := remoteconfig.HasCapability(capability)
		require.NoError(t, err)
		assert.False(t, has, "slow-path shutdown should remove capability %d", capability)
	}
}

func TestRemoteConfigFastPathShutdownKeepsTracerCapabilities(t *testing.T) {
	internalffe.ResetForTest()
	remoteconfig.Reset()
	t.Cleanup(func() {
		internalffe.ResetForTest()
		remoteconfig.Reset()
	})

	require.NoError(t, remoteconfig.Start(remoteconfig.DefaultClientConfig()))
	require.NoError(t, internalffe.SubscribeRC())
	provider, err := startWithRemoteConfig(ProviderConfig{})
	require.NoError(t, err)
	internalffe.ForwardForTest(remoteconfig.ProductUpdate{
		"path/first": remoteConfigTestPayload(t, "first-flag"),
	})
	firstConfig := provider.getConfiguration()
	require.NotNil(t, firstConfig)
	require.Contains(t, firstConfig.Flags, "first-flag")

	require.NoError(t, provider.ShutdownWithContext(context.Background()))
	internalffe.ForwardForTest(remoteconfig.ProductUpdate{
		"path/buffered": remoteConfigTestPayload(t, "buffered-flag"),
	})
	assert.Nil(t, provider.getConfiguration(), "stopped provider must not receive buffered updates")
	require.NotNil(t, internalffe.GetBufferedForTest(), "update should buffer while no provider is attached")

	replacement, err := startWithRemoteConfig(ProviderConfig{})
	require.NoError(t, err)
	bufferedConfig := replacement.getConfiguration()
	require.NotNil(t, bufferedConfig)
	require.Contains(t, bufferedConfig.Flags, "buffered-flag")
	assert.Nil(t, internalffe.GetBufferedForTest(), "replacement should drain the buffered update")

	internalffe.ForwardForTest(remoteconfig.ProductUpdate{
		"path/live": remoteConfigTestPayload(t, "live-flag"),
	})
	liveConfig := replacement.getConfiguration()
	require.NotNil(t, liveConfig)
	require.Contains(t, liveConfig.Flags, "live-flag")
	assert.Nil(t, provider.getConfiguration(), "live updates must not reach the stopped provider")
	require.NoError(t, replacement.ShutdownWithContext(context.Background()))

	hasProduct, err := remoteconfig.HasProduct(internalffe.FFEProductName)
	require.NoError(t, err)
	assert.True(t, hasProduct, "provider shutdown must not remove the tracer-owned subscription")
	for _, capability := range []remoteconfig.Capability{
		remoteconfig.FFEFlagEvaluation,
		remoteconfig.FFEFlagDependentFlagVariantKeyV1,
	} {
		has, err := remoteconfig.HasCapability(capability)
		require.NoError(t, err)
		assert.True(t, has, "provider shutdown must keep tracer capability %d", capability)
	}
}

func remoteConfigTestPayload(t *testing.T, flagKey string) []byte {
	t.Helper()
	data, err := json.Marshal(universalFlagsConfiguration{
		Format: "SERVER",
		Flags: map[string]*flag{
			flagKey: {
				Key:           flagKey,
				Enabled:       true,
				VariationType: valueTypeBoolean,
				Variations: map[string]*variant{
					"on": {Key: "on", Value: true},
				},
				Allocations: []*allocation{},
			},
		},
	})
	require.NoError(t, err)
	return data
}
