// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withAgentInfoPollInterval is a test-only StartOption that overrides the
// default 5-second polling interval, allowing tests to verify polling behaviour
// without long sleeps.
func withAgentInfoPollInterval(d time.Duration) StartOption {
	return func(c *config) {
		c.agentInfoPollInterval = d
	}
}

// TestRefreshAgentFeaturesPreservesStaticFields verifies that a call to
// refreshAgentFeatures preserves fields that are baked into components at
// startup, while still updating dynamic fields.
func TestRefreshAgentFeaturesPreservesStaticFields(t *testing.T) {
	// callCount tracks how many times the /info endpoint is hit.
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			// Startup response: set all static fields (v1, evpProxy, stats, etc.)
			if err := json.NewEncoder(w).Encode(map[string]any{
				"endpoints":           []string{"/v0.6/stats", "/evp_proxy/v2/", "/v1.0/traces"},
				"client_drop_p0s":     true,
				"span_events":         true,
				"span_meta_structs":   true,
				"obfuscation_version": 2,
				"peer_tags":           []string{"peer.hostname"},
				"feature_flags":       []string{"flag_a"},
				"config":              map[string]any{"statsd_port": 8999, "default_env": "prod"},
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		} else {
			// Poll response: all static-field-related values are different —
			// refreshAgentFeatures must not apply them.
			if err := json.NewEncoder(w).Encode(map[string]any{
				"endpoints":           []string{}, // no v1.0/traces, no evp_proxy, no stats
				"client_drop_p0s":     false,
				"span_events":         false,
				"span_meta_structs":   false,
				"obfuscation_version": 0,
				"peer_tags":           []string{},
				"feature_flags":       []string{"other_flag"},
				"config":              map[string]any{"statsd_port": 1111, "default_env": "overwritten"},
			}); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		}
	}))
	defer srv.Close()

	tr, err := newTracer(
		WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")),
		WithAgentTimeout(2),
	)
	require.NoError(t, err)
	defer tr.Stop()

	startup := tr.config.agent.load()

	// Sanity-check startup response was applied correctly.
	assert.True(t, startup.DropP0s)
	assert.True(t, startup.spanEventsAvailable)

	// Trigger one manual refresh (bypassing the ticker).
	tr.refreshAgentFeatures()

	after := tr.config.agent.load()

	// Dynamic fields should reflect the poll response.
	assert.False(t, after.DropP0s, "DropP0s must update dynamically")
	assert.False(t, after.Stats, "Stats must update dynamically")
	assert.False(t, after.spanEventsAvailable, "spanEventsAvailable must update dynamically")
	assert.Zero(t, after.obfuscationVersion, "obfuscationVersion must update dynamically")
	assert.Empty(t, after.peerTags, "peerTags must update dynamically")

	// Static fields must be frozen at their startup values.
	assert.Equal(t, startup.v1ProtocolAvailable, after.v1ProtocolAvailable, "v1ProtocolAvailable must not change after startup")
	assert.Equal(t, startup.StatsdPort, after.StatsdPort, "StatsdPort must not change after startup")
	assert.Equal(t, startup.evpProxyV2, after.evpProxyV2, "evpProxyV2 must not change after startup")
	assert.Equal(t, startup.metaStructAvailable, after.metaStructAvailable, "metaStructAvailable must not change after startup")
	assert.Equal(t, startup.featureFlags, after.featureFlags, "featureFlags must not change after startup")
	assert.Equal(t, startup.defaultEnv, after.defaultEnv, "defaultEnv must not change after startup")
}

// TestPollAgentInfoUpdatesFeaturesDynamically verifies that periodic polling
// picks up changes in the agent's dynamic capability flags.
func TestPollAgentInfoUpdatesFeaturesDynamically(t *testing.T) {
	const pollInterval = 20 * time.Millisecond

	var statsEnabled atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		enabled := statsEnabled.Load()
		endpoints := []string{}
		if enabled {
			endpoints = []string{"/v0.6/stats"}
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"endpoints":       endpoints,
			"client_drop_p0s": enabled,
			"span_events":     enabled,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	tr, err := newTracer(
		WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")),
		WithAgentTimeout(2),
		withAgentInfoPollInterval(pollInterval),
	)
	require.NoError(t, err)
	defer tr.Stop()

	assert.False(t, tr.config.agent.load().DropP0s, "DropP0s should be false initially")
	assert.False(t, tr.config.agent.load().Stats, "Stats should be false initially")

	// Enable features on the agent side.
	statsEnabled.Store(true)

	// Wait long enough for at least two poll ticks.
	assert.Eventually(t, func() bool {
		return tr.config.agent.load().DropP0s && tr.config.agent.load().Stats
	}, 10*pollInterval, pollInterval, "features should update after polling")
}

// TestPollAgentInfoRetainsLastKnownGoodOnError verifies that when the agent
// becomes unreachable, the last successfully fetched features are retained.
func TestPollAgentInfoRetainsLastKnownGoodOnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"endpoints":       []string{"/v0.6/stats"},
			"client_drop_p0s": true,
			"span_events":     true,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))

	tr, err := newTracer(
		WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")),
		WithAgentTimeout(2),
	)
	require.NoError(t, err)
	defer tr.Stop()

	// Confirm features were fetched at startup.
	require.True(t, tr.config.agent.load().DropP0s)
	require.True(t, tr.config.agent.load().spanEventsAvailable)

	// Take the server down.
	srv.Close()

	// A poll failure must not wipe out the known-good features.
	tr.refreshAgentFeatures()

	assert.True(t, tr.config.agent.load().DropP0s, "DropP0s must be retained on poll failure")
	assert.True(t, tr.config.agent.load().spanEventsAvailable, "spanEventsAvailable must be retained on poll failure")
}

// TestPollAgentInfoGoroutineStopsOnTracerStop verifies that the polling
// goroutine exits when the tracer is stopped, with no goroutine leak.
func TestPollAgentInfoGoroutineStopsOnTracerStop(t *testing.T) {
	const pollInterval = 20 * time.Millisecond

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if err := json.NewEncoder(w).Encode(map[string]any{
			"endpoints":       []string{"/v0.6/stats"},
			"client_drop_p0s": true,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	tr, err := newTracer(
		WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")),
		WithAgentTimeout(2),
		withAgentInfoPollInterval(pollInterval),
	)
	require.NoError(t, err)

	// Stop must complete promptly — the poll goroutine should unblock on t.stop.
	done := make(chan struct{})
	go func() {
		tr.Stop()
		close(done)
	}()

	select {
	case <-done:
		// success: Stop returned before timeout
	case <-time.After(2 * time.Second):
		t.Fatal("tracer.Stop() did not return in time; poll goroutine may be leaking")
	}
}

// TestPollAgentInfoRetainsLastKnownGoodOn404 verifies that when the agent
// returns 404 during a poll (e.g. agent downgrade), the previously fetched
// dynamic features are retained rather than being zeroed out.
func TestPollAgentInfoRetainsLastKnownGoodOn404(t *testing.T) {
	var return404 atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if return404.Load() {
			http.NotFound(w, nil)
			return
		}
		if err := json.NewEncoder(w).Encode(map[string]any{
			"endpoints":       []string{"/v0.6/stats"},
			"client_drop_p0s": true,
			"span_events":     true,
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	tr, err := newTracer(
		WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")),
		WithAgentTimeout(2),
	)
	require.NoError(t, err)
	defer tr.Stop()

	// Confirm features were fetched at startup.
	require.True(t, tr.config.agent.load().DropP0s)
	require.True(t, tr.config.agent.load().spanEventsAvailable)

	// Simulate agent returning 404 (e.g. after a downgrade).
	return404.Store(true)

	// A poll that returns 404 must not wipe out the known-good features.
	tr.refreshAgentFeatures()

	assert.True(t, tr.config.agent.load().DropP0s, "DropP0s must be retained on 404 poll")
	assert.True(t, tr.config.agent.load().spanEventsAvailable, "spanEventsAvailable must be retained on 404 poll")
}
