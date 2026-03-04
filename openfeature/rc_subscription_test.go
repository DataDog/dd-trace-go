// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package openfeature

import (
	"encoding/json"
	"testing"
	"time"

	rc "github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

// resetRCState resets the global rcState for test isolation.
func resetRCState() {
	rcState.Lock()
	defer rcState.Unlock()
	rcState.subscribed = false
	rcState.provider = nil
	rcState.buffered = nil
}

func TestAttachProviderNotSubscribed(t *testing.T) {
	resetRCState()
	defer resetRCState()

	provider := newDatadogProvider(ProviderConfig{})
	got := attachProvider(provider)
	assert.False(t, got, "attachProvider should return false when not subscribed")
}

func TestAttachProviderSubscribed(t *testing.T) {
	resetRCState()
	defer resetRCState()

	// Simulate that SubscribeRC was called by setting subscribed = true
	// (we can't call SubscribeRC directly without an RC client running).
	rcState.Lock()
	rcState.subscribed = true
	rcState.Unlock()

	provider := newDatadogProvider(ProviderConfig{})
	got := attachProvider(provider)
	assert.True(t, got, "attachProvider should return true when subscribed")

	// Provider should be set.
	rcState.Lock()
	assert.Equal(t, provider, rcState.provider)
	rcState.Unlock()
}

func TestAttachProviderReplaysBufferedConfig(t *testing.T) {
	resetRCState()
	defer resetRCState()

	// Build a valid config payload.
	config := universalFlagsConfiguration{
		Format: "SERVER",
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
	buffered := remoteconfig.ProductUpdate{"path/config": data}
	rcState.Lock()
	rcState.subscribed = true
	rcState.buffered = &buffered
	rcState.Unlock()

	provider := newDatadogProvider(ProviderConfig{})
	got := attachProvider(provider)
	assert.True(t, got)

	// The buffered config should have been replayed into the provider.
	cfg := provider.getConfiguration()
	require.NotNil(t, cfg, "provider should have configuration after replay")
	assert.Contains(t, cfg.Flags, "test-flag")

	// Buffer should be cleared.
	rcState.Lock()
	assert.Nil(t, rcState.buffered, "buffer should be nil after replay")
	rcState.Unlock()
}

func TestAttachProviderNoBuffer(t *testing.T) {
	resetRCState()
	defer resetRCState()

	// Subscribed but no buffered update yet.
	rcState.Lock()
	rcState.subscribed = true
	rcState.Unlock()

	provider := newDatadogProvider(ProviderConfig{})
	got := attachProvider(provider)
	assert.True(t, got)

	// Provider should have no configuration.
	cfg := provider.getConfiguration()
	assert.Nil(t, cfg, "provider should have no configuration when nothing was buffered")
}

func TestForwardingCallbackBuffersWhenNoProvider(t *testing.T) {
	resetRCState()
	defer resetRCState()

	config := universalFlagsConfiguration{
		Format: "SERVER",
		Flags: map[string]*flag{
			"flag-1": {
				Key:           "flag-1",
				Enabled:       true,
				VariationType: valueTypeString,
				Variations: map[string]*variant{
					"v1": {Key: "v1", Value: "hello"},
				},
				Allocations: []*allocation{},
			},
		},
	}
	data, err := json.Marshal(config)
	require.NoError(t, err)

	update := remoteconfig.ProductUpdate{"path/1": data}
	statuses := forwardingCallback(update)

	// All paths should be acknowledged.
	assert.Equal(t, rc.ApplyStateAcknowledged, statuses["path/1"].State)

	// Should be buffered.
	rcState.Lock()
	require.NotNil(t, rcState.buffered)
	assert.Contains(t, *rcState.buffered, "path/1")
	rcState.Unlock()
}

func TestForwardingCallbackBuffersOnlyLatest(t *testing.T) {
	resetRCState()
	defer resetRCState()

	// Send two updates — only the latest should be buffered.
	config1 := universalFlagsConfiguration{
		Format: "SERVER",
		Flags: map[string]*flag{
			"flag-old": {
				Key: "flag-old", Enabled: true, VariationType: valueTypeBoolean,
				Variations: map[string]*variant{"on": {Key: "on", Value: true}},
				Allocations: []*allocation{},
			},
		},
	}
	config2 := universalFlagsConfiguration{
		Format: "SERVER",
		Flags: map[string]*flag{
			"flag-new": {
				Key: "flag-new", Enabled: true, VariationType: valueTypeBoolean,
				Variations: map[string]*variant{"on": {Key: "on", Value: true}},
				Allocations: []*allocation{},
			},
		},
	}
	data1, _ := json.Marshal(config1)
	data2, _ := json.Marshal(config2)

	forwardingCallback(remoteconfig.ProductUpdate{"path/old": data1})
	forwardingCallback(remoteconfig.ProductUpdate{"path/new": data2})

	rcState.Lock()
	require.NotNil(t, rcState.buffered)
	assert.NotContains(t, *rcState.buffered, "path/old", "old update should be overwritten")
	assert.Contains(t, *rcState.buffered, "path/new", "latest update should be buffered")
	rcState.Unlock()
}

func TestForwardingCallbackForwardsWhenProviderAttached(t *testing.T) {
	resetRCState()
	defer resetRCState()

	provider := newDatadogProvider(ProviderConfig{})

	// Simulate subscribed + provider attached.
	rcState.Lock()
	rcState.subscribed = true
	rcState.provider = provider
	rcState.Unlock()

	config := universalFlagsConfiguration{
		Format: "SERVER",
		Flags: map[string]*flag{
			"live-flag": {
				Key: "live-flag", Enabled: true, VariationType: valueTypeBoolean,
				Variations: map[string]*variant{"on": {Key: "on", Value: true}},
				Allocations: []*allocation{},
			},
		},
	}
	data, err := json.Marshal(config)
	require.NoError(t, err)

	statuses := forwardingCallback(remoteconfig.ProductUpdate{"path/live": data})
	assert.Equal(t, rc.ApplyStateAcknowledged, statuses["path/live"].State)

	// Provider should have the config directly (not buffered).
	cfg := provider.getConfiguration()
	require.NotNil(t, cfg)
	assert.Contains(t, cfg.Flags, "live-flag")

	// Nothing should be buffered.
	rcState.Lock()
	assert.Nil(t, rcState.buffered)
	rcState.Unlock()
}

func TestStartWithRemoteConfigFastPath(t *testing.T) {
	resetRCState()
	defer resetRCState()

	// Simulate tracer having subscribed and buffered a config.
	config := universalFlagsConfiguration{
		Format: "SERVER",
		Flags: map[string]*flag{
			"fast-flag": {
				Key: "fast-flag", Enabled: true, VariationType: valueTypeBoolean,
				Variations: map[string]*variant{"on": {Key: "on", Value: true}},
				Allocations: []*allocation{},
			},
		},
	}
	data, err := json.Marshal(config)
	require.NoError(t, err)

	buffered := remoteconfig.ProductUpdate{"path/fast": data}
	rcState.Lock()
	rcState.subscribed = true
	rcState.buffered = &buffered
	rcState.Unlock()

	// startWithRemoteConfig should use the fast path.
	provider, err := startWithRemoteConfig(ProviderConfig{})
	require.NoError(t, err)
	require.NotNil(t, provider)

	cfg := provider.getConfiguration()
	require.NotNil(t, cfg, "provider should have config from fast path replay")
	assert.Contains(t, cfg.Flags, "fast-flag")
}
