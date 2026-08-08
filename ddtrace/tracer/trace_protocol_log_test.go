// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// TestTraceProtocolDowngradeLog pins the log-level rule for a denied v1
// request: Warn only when v1 was explicitly requested (not merely defaulted),
// so the entire pre-v1 Agent install base — a fully supported configuration —
// doesn't see a spurious warning on every startup.
func TestTraceProtocolDowngradeLog(t *testing.T) {
	v04OnlyAgent := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/info" {
				w.Write([]byte(`{"endpoints":["/v0.4/traces"],"client_drop_p0s":true}`))
			}
		}))
	}

	t.Run("explicit v1 denied warns", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", "1.0")
		srv := v04OnlyAgent()
		defer srv.Close()
		u, err := url.Parse(srv.URL)
		require.NoError(t, err)

		tp := new(log.RecordLogger)
		trc, err := newTracer(WithAgentAddr(u.Host), WithLogger(tp))
		require.NoError(t, err)
		defer trc.Stop()

		found := false
		for _, l := range tp.Logs() {
			if strings.Contains(l, "WARN") && strings.Contains(l, "trace protocol v1.0 was requested") {
				found = true
			}
		}
		assert.True(t, found, "expected a WARN log for a denied explicit v1 request; got: %v", tp.Logs())
	})

	t.Run("defaulted v1 denied logs at debug not warn", func(t *testing.T) {
		srv := v04OnlyAgent()
		defer srv.Close()
		u, err := url.Parse(srv.URL)
		require.NoError(t, err)

		tp := new(log.RecordLogger)
		trc, err := newTracer(WithAgentAddr(u.Host), WithLogger(tp), WithDebugMode(true))
		require.NoError(t, err)
		defer trc.Stop()

		for _, l := range tp.Logs() {
			assert.NotContains(t, l, "trace protocol v1.0 was requested", "a defaulted (not explicitly requested) v1 must never warn")
		}
	})

	t.Run("explicit v0.4 requested no denial log", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", "0.4")
		srv := v04OnlyAgent()
		defer srv.Close()
		u, err := url.Parse(srv.URL)
		require.NoError(t, err)

		tp := new(log.RecordLogger)
		trc, err := newTracer(WithAgentAddr(u.Host), WithLogger(tp), WithDebugMode(true))
		require.NoError(t, err)
		defer trc.Stop()

		for _, l := range tp.Logs() {
			assert.NotContains(t, l, "trace protocol v1.0 was requested")
			assert.NotContains(t, l, "does not expose the")
		}
	})

	t.Run("credentials in agent URL are redacted", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", "1.0")
		srv := v04OnlyAgent()
		defer srv.Close()
		u, err := url.Parse(srv.URL)
		require.NoError(t, err)
		// Must come from the env var: WithAgentAddr/WithAgentURL rebuild the URL
		// as {Scheme, Host} and drop userinfo, so an option-driven version of
		// this test would pass vacuously.
		t.Setenv("DD_TRACE_AGENT_URL", "http://user:s3cret@"+u.Host)

		tp := new(log.RecordLogger) // deliberately no Ignore(commonLogIgnore...)
		trc, err := newTracer(WithLogger(tp))
		require.NoError(t, err)
		defer trc.Stop()

		found := false
		for _, l := range tp.Logs() {
			if strings.Contains(l, "trace protocol v1.0 was requested") {
				found = true
				assert.NotContains(t, l, "s3cret")
				assert.Contains(t, l, "user:xxxxx@") // url.Redacted's marker
			}
		}
		assert.True(t, found, "expected a WARN for the denied explicit v1 request; got: %v", tp.Logs())
	})

	t.Run("unreachable agent does not claim a missing endpoint", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", "1.0")
		// Port 9 (discard) refuses immediately: /info never answers, so
		// v1ProtocolAvailable is "unknown", not "denied".
		t.Setenv("DD_TRACE_AGENT_URL", "http://127.0.0.1:9")

		tp := new(log.RecordLogger)
		trc, err := newTracer(WithLogger(tp))
		require.NoError(t, err)
		defer trc.Stop()

		for _, l := range tp.Logs() {
			assert.NotContains(t, l, "does not expose the",
				"an unreachable agent must not be reported as lacking /v1.0/traces")
		}
	})

	t.Run("OTLP export mode with denied v1 does not warn", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", "1.0")
		srv := v04OnlyAgent()
		defer srv.Close()
		u, err := url.Parse(srv.URL)
		require.NoError(t, err)

		tp := new(log.RecordLogger)
		trc, err := newTracer(WithAgentAddr(u.Host), WithLogger(tp), func(c *config) {
			c.internalConfig.SetOTLPExportMode(true, internalconfig.OriginCode)
		})
		require.NoError(t, err)
		defer trc.Stop()

		for _, l := range tp.Logs() {
			assert.NotContains(t, l, "trace protocol v1.0 was requested",
				"no Datadog trace payload is ever sent in OTLP export mode, so a denied v1 must not warn")
		}
	})

	t.Run("pre-7.28 agent (404 on /info) denies v1 and warns", func(t *testing.T) {
		t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", "1.0")
		// A reachable Agent that 404s on /info is affirmative evidence it
		// predates /v1.0/traces too (the latter postdates /info support), unlike
		// a silent, unreachable Agent whose v1 support is merely unknown.
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/info" {
				w.WriteHeader(http.StatusNotFound)
			}
		}))
		defer srv.Close()
		u, err := url.Parse(srv.URL)
		require.NoError(t, err)

		tp := new(log.RecordLogger)
		trc, err := newTracer(WithAgentAddr(u.Host), WithLogger(tp))
		require.NoError(t, err)
		defer trc.Stop()

		found := false
		for _, l := range tp.Logs() {
			if strings.Contains(l, "WARN") && strings.Contains(l, "trace protocol v1.0 was requested") {
				found = true
			}
		}
		assert.True(t, found, "a 404 on /info denies v1 just like an advertised endpoint list without it; got: %v", tp.Logs())
	})
}

// TestOTLPExportModeDoesNotOverrideRequestedProtocolTelemetry pins that
// selecting a non-agent trace writer (here, OTLP export mode) prevents the
// agent's /info response from ever overriding DD_TRACE_AGENT_PROTOCOL_VERSION
// telemetry with a derived "0.4": no Datadog trace payload is sent in this
// mode, so the agent's lack of /v1.0/traces is not evidence the requested
// protocol was denied.
func TestOTLPExportModeDoesNotOverrideRequestedProtocolTelemetry(t *testing.T) {
	telemetryClient := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(telemetryClient)()

	t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", "1.0")
	agent := startTestAgent(t)
	agent.SetInfo(`{"endpoints":["/v0.4/traces","/v0.6/stats"],"client_drop_p0s":true}`)

	tr := newTracerTest(t, agent, func(c *config) {
		c.internalConfig.SetOTLPExportMode(true, internalconfig.OriginCode)
	})
	defer stopTracerTest(tr)

	require.False(t, tr.config.usesAgentTraceWriter(), "sanity check: OTLP export mode must not select the agent writer")

	// Exercise the runtime re-report path too, not just the one at startup.
	tr.refreshAgentFeatures()

	for _, c := range telemetryClient.Configuration {
		if c.Name != "DD_TRACE_AGENT_PROTOCOL_VERSION" {
			continue
		}
		assert.NotEqual(t, "0.4", c.Value,
			"OTLP export mode sends no Datadog trace payload, so the agent's v0.4-only /info response must never override the requested protocol")
	}
	telemetrytest.CheckConfig(t, telemetryClient.Configuration, "DD_TRACE_AGENT_PROTOCOL_VERSION", "1.0")
}
