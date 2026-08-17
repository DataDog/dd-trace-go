// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/log"
)

// withAgentInfoPollInterval is a test-only StartOption that overrides the
// default 5-second polling interval, allowing tests to verify polling behaviour
// without long sleeps.
func withAgentInfoPollInterval(d time.Duration) StartOption {
	return func(c *config) {
		c.agentInfoPollInterval = d
	}
}

func TestRefreshAgentFeaturesUpdatesTraceFilters(t *testing.T) {
	roundTripper := &agentInfoJSONRoundTripper{body: `{
		"endpoints":["/v0.6/stats"],
		"client_drop_p0s":true,
		"filter_tags":{"reject":["first:value"]}
	}`}
	cfg, err := newTestConfig(
		WithAgentAddr("agent:8126"),
		WithHTTPClient(&http.Client{Transport: roundTripper}),
		WithStatsComputation(true),
	)
	require.NoError(t, err)
	require.NotNil(t, cfg.agent.load().traceFilters)
	assert.Equal(t, "first", cfg.agent.load().traceFilters.rejectKV[0].key)

	roundTripper.body = `{
		"endpoints":["/v0.6/stats"],
		"client_drop_p0s":true,
		"filter_tags_regex":{"require":["second.*:value.*"]},
		"ignore_resources":["health.*"]
	}`
	tracer := &tracer{config: cfg, stop: make(chan struct{})}
	tracer.refreshAgentFeatures()

	filters := cfg.agent.load().traceFilters
	require.NotNil(t, filters)
	assert.Empty(t, filters.rejectKV)
	require.Len(t, filters.requireRegex, 1)
	assert.Equal(t, "second.*", filters.requireRegex[0].key)
	require.Len(t, filters.ignoreResources, 1)
}

// TestRefreshAgentFeaturesPreservesStaticFields verifies that a call to
// refreshAgentFeatures preserves fields that are baked into components at
// startup, while still updating dynamic fields.
//
// v1TracesAdvertised is a raw dynamic observation, refreshed on every poll —
// it is not itself the wire-format decision, and it was never "frozen" even
// under its previous name. refreshAgentFeatures feeds it into
// (*config).advanceTraceProtocolState (trace_protocol_state.go), a monotone
// lattice tested separately in trace_protocol_state_test.go and
// trace_protocol_runtime_test.go.
func TestRefreshAgentFeaturesPreservesStaticFields(t *testing.T) {
	// callCount tracks how many times the /info endpoint is hit.
	var callCount atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := callCount.Add(1)
		if n == 1 {
			// Startup response: set all static fields (v1, evpProxy, stats, etc.)
			if err := json.NewEncoder(w).Encode(map[string]any{
				"endpoints":           []string{"/v0.6/stats", "/evp_proxy/v2/", "/v1.0/traces", "/telemetry/proxy/"},
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
	assert.True(t, startup.reachable)
	assert.True(t, startup.hasTelemetryProxy)

	// Trigger one manual refresh (bypassing the ticker).
	tr.refreshAgentFeatures()

	after := tr.config.agent.load()

	// Dynamic fields should reflect the poll response.
	assert.False(t, after.DropP0s, "DropP0s must update dynamically")
	assert.False(t, after.Stats, "Stats must update dynamically")
	assert.False(t, after.spanEventsAvailable, "spanEventsAvailable must update dynamically")
	assert.Zero(t, after.obfuscationVersion, "obfuscationVersion must update dynamically")
	assert.Empty(t, after.peerTags, "peerTags must update dynamically")

	// v1TracesAdvertised is dynamic: the poll response advertises no
	// /v1.0/traces.
	require.True(t, startup.v1TracesAdvertised, "sanity check: startup snapshot must have advertised v1")
	assert.False(t, after.v1TracesAdvertised, "v1TracesAdvertised must update dynamically")

	// Static fields must be frozen at their startup values.
	assert.Equal(t, startup.StatsdPort, after.StatsdPort, "StatsdPort must not change after startup")
	assert.Equal(t, startup.evpProxyV2, after.evpProxyV2, "evpProxyV2 must not change after startup")
	assert.Equal(t, startup.metaStructAvailable, after.metaStructAvailable, "metaStructAvailable must not change after startup")
	assert.Equal(t, startup.featureFlags, after.featureFlags, "featureFlags must not change after startup")
	assert.Equal(t, startup.defaultEnv, after.defaultEnv, "defaultEnv must not change after startup")
	assert.Equal(t, startup.reachable, after.reachable, "reachable must not change after startup")
	assert.Equal(t, startup.hasTelemetryProxy, after.hasTelemetryProxy, "hasTelemetryProxy must not change after startup")
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
		a := tr.config.agent.load()
		return a.DropP0s && a.Stats
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

	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case <-done:
		// success: Stop returned before timeout
	case <-timer.C:
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
			w.WriteHeader(http.StatusNotFound)
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

// TestTraceProtocolDowngradesOn404AfterAgentRollback pins the fix for the
// bug where a v1-capable agent that gets rolled back to a version predating
// /info support (404 on /info) kept v1 available forever, because a 404 was
// treated the same as any other fetch error ("keep last-known-good"). A 404
// on /info is actually positive evidence v1 is gone: /v1.0/traces support
// postdates /info support, so an agent without /info cannot serve v1 either.
func TestTraceProtocolDowngradesOn404AfterAgentRollback(t *testing.T) {
	var return404 atomic.Bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if return404.Load() {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"endpoints":       []string{"/v0.4/traces", "/v1.0/traces", "/v0.6/stats"},
			"client_drop_p0s": true,
		})
	}))
	defer srv.Close()

	tr, err := newTracer(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
	require.NoError(t, err)
	defer tr.Stop()

	require.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol(), "sanity check: startup must resolve to v1")
	require.True(t, tr.config.agent.load().DropP0s, "sanity check: DropP0s must be set at startup")

	// Simulate the agent being rolled back to a version without /info.
	return404.Store(true)
	tr.refreshAgentFeatures()

	// A 404 gives no fresh /info payload, so agentFeatures itself (including
	// v1TracesAdvertised, a raw observation of the last successful poll) is
	// left at its prior value -- it's the protocol *state* that a 404
	// conclusively downgrades, not the cached observation. See
	// (*tracer).refreshAgentFeatures and trace_protocol_state.go.
	assert.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol(), "a 404 on /info must downgrade the effective protocol immediately")
	assert.True(t, tr.config.agent.load().DropP0s, "unrelated last-known-good fields must be retained on a 404 poll")
}

// TestTraceProtocolUpgradesAfterAgentBecomesReachable is the fix for the
// permanent-downgrade bug: previously, a startup /info failure froze the
// trace protocol at v0.4 for the tracer's entire lifetime, even once the
// agent became reachable. A transient startup failure resolves to
// protoUnknown, not protoV04 (see loadAgentFeatures) — protoUnknown carries
// no conclusive evidence either way, so a later successful poll can still
// resolve it to v1.
func TestTraceProtocolUpgradesAfterAgentBecomesReachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close()) // agent unreachable at startup

	tr, err := newTracer(WithAgentAddr(addr), WithAgentTimeout(1))
	require.NoError(t, err)
	defer tr.Stop()

	require.False(t, tr.config.agent.load().v1TracesAdvertised, "sanity check: agent must be unreachable at startup")
	require.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol())
	require.Equal(t, traceProtocolV1, tr.config.internalConfig.RequestedTraceProtocol(),
		"the requested value must never be touched by a capability downgrade")

	// Bring an agent up on the exact same address, advertising v1.
	ln2, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			w.Write([]byte(`{"endpoints":["/v0.4/traces","/v1.0/traces","/v0.6/stats"],"client_drop_p0s":true}`))
		}
	}))
	srv.Listener.Close()
	srv.Listener = ln2
	srv.Start()
	defer srv.Close()

	// A single successful poll is enough: unlike a downgrade already in
	// effect (which is terminal, see trace_protocol_state.go), protoUnknown
	// has no conclusive evidence yet, so the first positive poll resolves it.
	tr.refreshAgentFeatures()

	assert.True(t, tr.config.agent.load().v1TracesAdvertised, "v1 must become available once the agent is reachable")
	assert.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol(), "the tracer must upgrade to v1 once the agent proves reachable")
}

// TestTraceProtocolDowngradesImmediatelyOnInfoPoll pins that a downgrade
// (unlike an upgrade) applies on the very first negative poll, and that the
// requested protocol survives the downgrade unmodified.
func TestTraceProtocolDowngradesImmediatelyOnInfoPoll(t *testing.T) {
	var advertiseV1 atomic.Bool
	advertiseV1.Store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		endpoints := []string{"/v0.4/traces", "/v0.6/stats"}
		if advertiseV1.Load() {
			endpoints = []string{"/v0.4/traces", "/v1.0/traces", "/v0.6/stats"}
		}
		json.NewEncoder(w).Encode(map[string]any{"endpoints": endpoints, "client_drop_p0s": true}) //nolint:errcheck
	}))
	defer srv.Close()

	tr, err := newTracer(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
	require.NoError(t, err)
	defer tr.Stop()

	require.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol(), "sanity check: startup must resolve to v1")

	advertiseV1.Store(false)
	tr.refreshAgentFeatures()

	assert.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol(), "a downgrade must apply on the first negative poll")
	assert.Equal(t, traceProtocolV1, tr.config.internalConfig.RequestedTraceProtocol(), "the requested value must survive the downgrade")
}

// TestTraceProtocolDowngradeIsSticky verifies the core property that replaces
// the old streak-based hysteresis: once a poll produces conclusive evidence
// v1 is unavailable, no number of subsequent positive polls can re-upgrade
// it. Re-upgrading needs a process restart — see trace_protocol_state.go and
// doc.go for why: no number of consecutive /info polls proves anything about
// where the next trace send will land in a load-balanced fleet.
func TestTraceProtocolDowngradeIsSticky(t *testing.T) {
	var advertiseV1 atomic.Bool
	advertiseV1.Store(true)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		endpoints := []string{"/v0.4/traces", "/v0.6/stats"}
		if advertiseV1.Load() {
			endpoints = []string{"/v0.4/traces", "/v1.0/traces", "/v0.6/stats"}
		}
		json.NewEncoder(w).Encode(map[string]any{"endpoints": endpoints, "client_drop_p0s": true}) //nolint:errcheck
	}))
	defer srv.Close()

	tr, err := newTracer(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2))
	require.NoError(t, err)
	defer tr.Stop()

	require.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol(), "sanity check: startup must resolve to v1")

	advertiseV1.Store(false)
	tr.refreshAgentFeatures()
	require.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol(), "sanity check: the downgrade must apply")

	advertiseV1.Store(true)
	for range 20 {
		tr.refreshAgentFeatures()
		assert.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol(), "a downgrade must never re-upgrade from polling alone")
	}
}

// TestTraceProtocolStaysV04AfterRejectedSend covers the mixed-fleet gap that
// polling alone cannot close: a load-balanced fleet where this backend
// advertises v1 via /info but never actually accepts a /v1.0/traces POST. No
// number of subsequent positive /info polls proves where the next send will
// land, so once a live send is rejected outright, the state stays on v0.4
// for the rest of the process — see downgradeAfterRejectedSend and
// trace_protocol_state.go. The rejected payload itself is dropped, not
// redelivered (see doc.go's documented trade-off).
func TestTraceProtocolStaysV04AfterRejectedSend(t *testing.T) {
	agent := startTestAgent(t)
	agent.RejectV1Traces(true)

	tr := newTracerTest(t, agent, WithSendRetries(0))
	defer stopTracerTest(tr)

	require.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol(), "sanity check: /info advertises v1 at startup")

	s := tr.StartSpan("op")
	s.Finish()
	flushAgentTracerTest(t, tr, agent, 0)

	assert.Empty(t, agent.Requests(), "the rejected v1 payload must be dropped, not redelivered")
	assert.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol(), "a rejected send must downgrade immediately")

	// The agent still advertises v1 via /info -- only the trace-send endpoint
	// rejects it, exactly the load-balanced-fleet scenario a poll-count
	// hysteresis cannot see through (a poll and a send are independent
	// requests that can land on different backends). No number of
	// subsequent positive polls may re-upgrade.
	for range 10 {
		tr.refreshAgentFeatures()
		require.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol(), "the downgrade must stay sticky through repeated positive polls")
	}

	// A second span still gets through, now correctly encoded as v0.4.
	s2 := tr.StartSpan("op2")
	s2.Finish()
	flushAgentTracerTest(t, tr, agent, 1)
	assert.Equal(t, []string{tracesAPIPath}, agent.Requests())
}

// TestTraceProtocolChangeLoggedOncePerTransition verifies that repeated,
// identical polls do not re-log (or re-report to config telemetry) an
// unchanged effective protocol — ReportEffectiveTraceProtocol dedupes so a
// periodic poll cannot spam config telemetry seqIDs.
func TestTraceProtocolChangeLoggedOncePerTransition(t *testing.T) {
	tp := new(log.RecordLogger)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"endpoints":       []string{"/v0.4/traces", "/v0.6/stats"}, // no v1: startup resolves to v0.4
			"client_drop_p0s": true,
		})
	}))
	defer srv.Close()

	tr, err := newTracer(WithAgentAddr(strings.TrimPrefix(srv.URL, "http://")), WithAgentTimeout(2), WithLogger(tp))
	require.NoError(t, err)
	defer tr.Stop()

	tp.Reset()
	for range 5 {
		tr.refreshAgentFeatures() // /info response never changes
	}

	var changes int
	for _, l := range tp.Logs() {
		if strings.Contains(l, "trace protocol changed") {
			changes++
		}
	}
	assert.Zero(t, changes, "an unchanged effective protocol must never re-log a transition")
}
