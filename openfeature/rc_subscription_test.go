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
	internalffe.SetBufferedForTest(&remoteconfig.ProductUpdate{"path/config": data})

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
	internalffe.SetBufferedForTest(&remoteconfig.ProductUpdate{"path/fast": data})

	// startWithRemoteConfig should use the fast path.
	provider, err := startWithRemoteConfig(ProviderConfig{})
	require.NoError(t, err)
	require.NotNil(t, provider)

	cfg := provider.getConfiguration()
	require.NotNil(t, cfg, "provider should have config from fast path replay")
	assert.Contains(t, cfg.Flags, "fast-flag")
}
