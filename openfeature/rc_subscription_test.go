// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
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
	cleanupProvider(t, provider)
	got := attachProvider(provider)
	assert.False(t, got, "attachProvider should return false when not subscribed")
}

func TestAttachProviderSubscribed(t *testing.T) {
	internalffe.ResetForTest()
	defer internalffe.ResetForTest()

	internalffe.SetSubscribedForTest(true)
	t.Cleanup(remoteconfig.Stop)

	provider := newDatadogProvider(ProviderConfig{})
	cleanupProvider(t, provider)
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
	data, err := json.Marshal(&config)
	require.NoError(t, err)

	// Simulate SubscribeRC + a buffered update arriving before provider exists.
	internalffe.SetSubscribedForTest(true)
	t.Cleanup(remoteconfig.Stop)
	internalffe.SetBufferedForTest(remoteconfig.ProductUpdate{"path/config": data})

	provider := newDatadogProvider(ProviderConfig{})
	cleanupProvider(t, provider)
	got := attachProvider(provider)
	assert.True(t, got)

	// The buffered config should have been replayed into the provider.
	cfg := provider.getConfiguration()
	require.NotNil(t, cfg, "provider should have configuration after replay")
	assert.Contains(t, cfg.Flags, "test-flag")

	// The update is kept for providers that attach later.
	assert.Contains(t, internalffe.GetBufferedForTest(), "path/config")
}

func TestAttachProviderNoBuffer(t *testing.T) {
	internalffe.ResetForTest()
	defer internalffe.ResetForTest()

	internalffe.SetSubscribedForTest(true)
	t.Cleanup(remoteconfig.Stop)

	provider := newDatadogProvider(ProviderConfig{})
	cleanupProvider(t, provider)
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
	data, err := json.Marshal(&config)
	require.NoError(t, err)

	internalffe.SetSubscribedForTest(true)
	t.Cleanup(remoteconfig.Stop)
	internalffe.SetBufferedForTest(remoteconfig.ProductUpdate{"path/fast": data})

	// Verify SubscribeProvider returns fast path (tracer subscribed)
	// without touching global remoteconfig state.
	provider := newDatadogProvider(ProviderConfig{})
	cleanupProvider(t, provider)
	require.NoError(t, internalffe.SubscribeProvider())

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
	t.Cleanup(remoteconfig.Stop)

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

// An application can register a second provider, for example one with exposures off
// under its own OpenFeature domain, while Remote Config delivers the flags.
func TestStartWithRemoteConfigSeveralProviders(t *testing.T) {
	internalffe.ResetForTest()
	defer internalffe.ResetForTest()
	internalffe.SetSubscribedForTest(true)
	t.Cleanup(remoteconfig.Stop)

	encode := func(key string) []byte {
		data, err := json.Marshal(&universalFlagsConfiguration{
			Format: "SERVER",
			Flags: map[string]*flag{key: {
				Key: key, Enabled: true, VariationType: valueTypeBoolean,
				Variations:  map[string]*variant{"on": {Key: "on", Value: true}},
				Allocations: []*allocation{},
			}},
		})
		require.NoError(t, err)
		return data
	}
	internalffe.SetBufferedForTest(remoteconfig.ProductUpdate{"path/config": encode("first")})

	live, err := startWithRemoteConfig(ProviderConfig{})
	require.NoError(t, err)
	cleanupProvider(t, live)
	peek, err := startWithRemoteConfig(ProviderConfig{ExposuresEnabled: new(false)})
	require.NoError(t, err, "a second provider attaches to the same subscription")
	cleanupProvider(t, peek)
	for _, p := range []*DatadogProvider{live, peek} {
		require.NotNil(t, p.getConfiguration())
		assert.Contains(t, p.getConfiguration().Flags, "first")
	}

	internalffe.ForwardForTest(remoteconfig.ProductUpdate{"path/config": encode("second")})
	for _, p := range []*DatadogProvider{live, peek} {
		assert.Contains(t, p.getConfiguration().Flags, "second", "every provider gets each update")
	}

	shutdownAndWait(t, peek)
	internalffe.ForwardForTest(remoteconfig.ProductUpdate{"path/config": encode("third")})
	assert.Contains(t, live.getConfiguration().Flags, "third", "shutting one provider down keeps the other attached")
}

// A provider attached after Remote Config delivered an invalid replacement gets
// the configuration the attached provider kept, so both evaluate the same flags.
func TestStartWithRemoteConfigLateProviderSkipsRejectedConfig(t *testing.T) {
	internalffe.ResetForTest()
	defer internalffe.ResetForTest()
	internalffe.SetSubscribedForTest(true)
	t.Cleanup(remoteconfig.Stop)

	data, err := json.Marshal(&universalFlagsConfiguration{
		Format: "SERVER",
		Flags: map[string]*flag{"kept": {
			Key: "kept", Enabled: true, VariationType: valueTypeBoolean,
			Variations:  map[string]*variant{"on": {Key: "on", Value: true}},
			Allocations: []*allocation{},
		}},
	})
	require.NoError(t, err)

	live, err := startWithRemoteConfig(ProviderConfig{})
	require.NoError(t, err)
	cleanupProvider(t, live)
	internalffe.ForwardForTest(remoteconfig.ProductUpdate{"path/config": data})
	internalffe.ForwardForTest(remoteconfig.ProductUpdate{"path/config": []byte(`{"format":"CLIENT"}`)})
	require.Contains(t, live.getConfiguration().Flags, "kept", "the live provider keeps its last known configuration")

	peek, err := startWithRemoteConfig(ProviderConfig{ExposuresEnabled: new(false)})
	require.NoError(t, err)
	cleanupProvider(t, peek)
	require.NotNil(t, peek.getConfiguration())
	assert.Contains(t, peek.getConfiguration().Flags, "kept")
}
