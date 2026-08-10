// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// isV1WireByte reports whether b is the first byte of a msgpack map — the
// shape of a v1.0 trace payload. Mirrors the sniffing logic in decode().
func isV1WireByte(b byte) bool {
	return b == msgpackMap16 || b == msgpackMap32 || b&0xf0 == msgpackMapFix
}

// TestNewConfigKeepsV1WhenCSSDisabled is the headline regression pin for the
// CSS<->trace-protocol decoupling: disabling client-side stats computation
// must not downgrade the wire protocol. Before the fix, this failed — the
// tracer POSTed a v0.4 array body to /v0.4/traces even though the agent
// advertised /v1.0/traces.
func TestNewConfigKeepsV1WhenCSSDisabled(t *testing.T) {
	agent := startTestAgent(t)
	tr := newTracerTest(t, agent, WithStatsComputation(false))
	defer stopTracerTest(tr)

	require.False(t, tr.config.canComputeStats(), "sanity check: CSS must actually be off")
	assert.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol())
	assert.Equal(t, agent.URL()+tracesAPIPathV1, tr.config.ddTransport.endpoint(tr.config.effectiveTraceProtocol()))

	span := tr.StartSpan("op")
	wantTraceID := span.Context().TraceIDLower()
	span.Finish()
	flushAgentTracerTest(t, tr, agent, 1)

	assert.Equal(t, []string{tracesAPIPathV1}, agent.Requests(), "the request must land on /v1.0/traces")
	firstBytes := agent.RequestFirstBytes()
	require.Len(t, firstBytes, 1)
	assert.True(t, isV1WireByte(firstBytes[0]), "expected a msgpack map (v1) body, got byte 0x%02x", firstBytes[0])

	spans := agent.Spans()
	require.Len(t, spans, 1)
	assert.Equal(t, wantTraceID, spans[0].traceID, "the trace ID must round-trip through the v1 chunk-level encoding")
}

// TestTraceProtocolDecoupling is the decision matrix: the effective protocol
// must be a function of (requested protocol, agent v1 availability) alone.
// Client-side stats may not move it. The expected value is computed, not
// hand-enumerated, so the invariant itself — not a fixed table of outcomes —
// is what's being pinned.
func TestTraceProtocolDecoupling(t *testing.T) {
	requested := []struct {
		name string
		env  string // "" means DD_TRACE_AGENT_PROTOCOL_VERSION is left unset
	}{
		{"unset", ""},
		{"v1", "1.0"},
		{"v04", "0.4"},
		{"invalid", "garbage"}, // must behave exactly like "unset" (falls back to the default, v1)
	}

	for _, req := range requested {
		for _, v1Advertised := range []bool{true, false} {
			for _, cssOff := range []bool{false, true} {
				t.Run(fmt.Sprintf("requested=%s/v1_advertised=%t/css_off=%t", req.name, v1Advertised, cssOff), func(t *testing.T) {
					if req.env != "" {
						t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", req.env)
					}
					agent := startTestAgent(t)
					endpoints := `"/v0.4/traces","/v0.6/stats"`
					if v1Advertised {
						endpoints = `"/v0.4/traces","/v1.0/traces","/v0.6/stats"`
					}
					agent.SetInfo(`{"endpoints":[` + endpoints + `],"client_drop_p0s":true}`)

					var opts []StartOption
					if cssOff {
						opts = append(opts, WithStatsComputation(false))
					}
					tr := newTracerTest(t, agent, opts...)
					defer stopTracerTest(tr)

					// The invariant under test: v1 iff v1 wasn't explicitly
					// declined and the agent advertises it. CSS state never
					// enters into it.
					wantV1 := req.env != "0.4" && v1Advertised
					wantProtocol := traceProtocolV04
					wantPath := tracesAPIPath
					if wantV1 {
						wantProtocol = traceProtocolV1
						wantPath = tracesAPIPathV1
					}

					assert.Equal(t, wantProtocol, tr.config.effectiveTraceProtocol())
					assert.Equal(t, agent.URL()+wantPath, tr.config.ddTransport.endpoint(tr.config.effectiveTraceProtocol()))

					w := newAgentTraceWriter(tr.config, newPrioritySampler(), &statsdtest.TestStatsdClient{})
					assert.Equal(t, wantProtocol, w.payload.protocol())
				})
			}
		}
	}
}

// TestPinTestTracerToV04RotatesWriterPayload pins a gap flagged in review:
// startTestTracer's v1-capability override used to run after newTracer had already
// built the writer's initial payload, so on a developer machine where a real Agent at
// the default address advertises v1, that payload had already latched onto v1 before
// the override ran. Overriding config alone does not retroactively change an
// already-built payload — only an empty-payload flush re-reads the effective protocol
// — so without one, the first real trace would still encode as v1 despite the
// override's intent to force v0.4.
//
// A real v1-capable Agent at the default address isn't reproducible in CI, so simulate
// the precondition directly: force the writer's payload to v1 and the agent snapshot
// back to "v1 available", then confirm pinTestTracerToV04 corrects both.
func TestPinTestTracerToV04RotatesWriterPayload(t *testing.T) {
	tr, _, _, stop, err := startTestTracer(t)
	require.NoError(t, err)
	defer stop()

	w, ok := tr.traceWriter.(*agentTraceWriter)
	require.True(t, ok)

	w.mu.Lock()
	w.payload = newPayload(traceProtocolV1)
	w.mu.Unlock()
	// A real v1-capable Agent at the default address isn't reproducible in CI
	// (startTestTracer's own pinTestTracerToV04 call has already forced this
	// tracer's protocol state to protoV04, the lattice's terminal value), so
	// force the precondition directly rather than trying to drive it through
	// a poll.
	setTraceProtocolStateForTest(tr.config, protoV1)
	af := tr.config.agent.load()
	tr.config.internalConfig.SetTraceProtocol(traceProtocolV1, internalconfig.OriginCode)
	require.Equal(t, traceProtocolV1, w.payload.protocol(), "sanity check: simulated precondition")

	pinTestTracerToV04(tr, af)

	assert.Equal(t, traceProtocolV04, tr.config.effectiveTraceProtocol())
	assert.Equal(t, traceProtocolV04, tr.config.internalConfig.RequestedTraceProtocol())
	assert.Equal(t, traceProtocolV04, w.payload.protocol(),
		"the writer's already-built payload must rotate off v1, not just the config")
}

// TestV1StatsWorkaroundDoesNotDropP0s is a decision-matrix pin for the
// 7.77/7.78 v1.0 stats workaround (see agentOmitsLangInV1Stats): client-side
// stats and P0 dropping must be forced on together — by design, not by
// oversight — exactly when the agent is affected, the effective protocol is
// v1.0, and the escape hatch and exclusions don't apply.
func TestV1StatsWorkaroundDoesNotDropP0s(t *testing.T) {
	cases := []struct {
		name              string
		agentVersion      string // "" omits the "version" field from /info entirely
		v1Advertised      bool
		statsAdvertised   bool
		dropP0sAdvertised bool
		envProtocol       string // DD_TRACE_AGENT_PROTOCOL_VERSION, "" leaves it unset
		wantComputeStats  bool
		wantDropP0s       bool
		wantProtocol      float64
	}{
		{
			name: "7.77 forces stats and P0 dropping on", agentVersion: "7.77.0",
			v1Advertised: true, statsAdvertised: true, dropP0sAdvertised: true,
			wantComputeStats: true, wantDropP0s: true, wantProtocol: traceProtocolV1,
		},
		{
			name: "7.78 forces stats and P0 dropping on", agentVersion: "7.78.3",
			v1Advertised: true, statsAdvertised: true, dropP0sAdvertised: true,
			wantComputeStats: true, wantDropP0s: true, wantProtocol: traceProtocolV1,
		},
		{
			name: "7.79.0-rc.4 (unfixed prerelease) also forces the override on", agentVersion: "7.79.0-rc.4",
			v1Advertised: true, statsAdvertised: true, dropP0sAdvertised: true,
			wantComputeStats: true, wantDropP0s: true, wantProtocol: traceProtocolV1,
		},
		{
			name: "7.79.0-rc.6 is fixed: no override", agentVersion: "7.79.0-rc.6",
			v1Advertised: true, statsAdvertised: true, dropP0sAdvertised: true,
			wantComputeStats: false, wantDropP0s: false, wantProtocol: traceProtocolV1,
		},
		{
			name: "7.79.0 is fixed: no override", agentVersion: "7.79.0",
			v1Advertised: true, statsAdvertised: true, dropP0sAdvertised: true,
			wantComputeStats: false, wantDropP0s: false, wantProtocol: traceProtocolV1,
		},
		{
			// A real 7.76.9 agent never advertises /v1.0/traces (it is gated
			// behind apm_config.enable_v1_trace_endpoint, default off through
			// 7.76.x), so the protocol guard alone excludes it in practice —
			// this is the case the "true defect boundary" recommendation
			// argued was equivalent to the literal 7.77/7.78 window.
			name: "7.76.9 doesn't advertise v1.0 in practice: no override, protocol stays v0.4", agentVersion: "7.76.9",
			v1Advertised: false, statsAdvertised: true, dropP0sAdvertised: true,
			wantComputeStats: false, wantDropP0s: false, wantProtocol: traceProtocolV04,
		},
		{
			name: "unldflagged 6.0.0 fallback: no override", agentVersion: "6.0.0",
			v1Advertised: true, statsAdvertised: true, dropP0sAdvertised: true,
			wantComputeStats: false, wantDropP0s: false, wantProtocol: traceProtocolV1,
		},
		{
			name: "absent version: no override", agentVersion: "",
			v1Advertised: true, statsAdvertised: true, dropP0sAdvertised: true,
			wantComputeStats: false, wantDropP0s: false, wantProtocol: traceProtocolV1,
		},
		{
			name: "non-Agent version string: no override", agentVersion: "datadogexporter-otelcol-0.155.0",
			v1Advertised: true, statsAdvertised: true, dropP0sAdvertised: true,
			wantComputeStats: false, wantDropP0s: false, wantProtocol: traceProtocolV1,
		},
		{
			name: "escape hatch: DD_TRACE_AGENT_PROTOCOL_VERSION=0.4 downgrades, no override", agentVersion: "7.77.0",
			v1Advertised: true, statsAdvertised: true, dropP0sAdvertised: true, envProtocol: "0.4",
			wantComputeStats: false, wantDropP0s: false, wantProtocol: traceProtocolV04,
		},
		{
			name: "agent doesn't advertise v1.0: no override (v0.4 anyway)", agentVersion: "7.77.0",
			v1Advertised: false, statsAdvertised: true, dropP0sAdvertised: true,
			wantComputeStats: false, wantDropP0s: false, wantProtocol: traceProtocolV04,
		},
		{
			name: "agent doesn't advertise /v0.6/stats: no override, no protocol downgrade", agentVersion: "7.77.0",
			v1Advertised: true, statsAdvertised: false, dropP0sAdvertised: true,
			wantComputeStats: false, wantDropP0s: false, wantProtocol: traceProtocolV1,
		},
		{
			name: "agent has the probabilistic sampler enabled (client_drop_p0s=false): no override, no protocol downgrade", agentVersion: "7.77.0",
			v1Advertised: true, statsAdvertised: true, dropP0sAdvertised: false,
			wantComputeStats: false, wantDropP0s: false, wantProtocol: traceProtocolV1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.envProtocol != "" {
				t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", tc.envProtocol)
			}

			agent := startTestAgent(t)
			endpoints := []string{`"/v0.4/traces"`}
			if tc.v1Advertised {
				endpoints = append(endpoints, `"/v1.0/traces"`)
			}
			if tc.statsAdvertised {
				endpoints = append(endpoints, `"/v0.6/stats"`)
			}
			body := `{"endpoints":[` + strings.Join(endpoints, ",") + `],"client_drop_p0s":` + strconv.FormatBool(tc.dropP0sAdvertised)
			if tc.agentVersion != "" {
				body += `,"version":"` + tc.agentVersion + `"`
			}
			body += `}`
			agent.SetInfo(body)

			tr := newTracerTest(t, agent, WithStatsComputation(false))
			defer stopTracerTest(tr)

			assert.Equal(t, tc.wantComputeStats, tr.config.canComputeStats(), "canComputeStats")
			assert.Equal(t, tc.wantDropP0s, tr.config.canDropP0s(), "canDropP0s")
			assert.Equal(t, tc.wantProtocol, tr.config.effectiveTraceProtocol(), "effectiveTraceProtocol")
		})
	}
}

// v1StatsWorkaroundAffectedInfo is an /info body with every gate in
// forcesStatsForV1Agent lined up in favour of the override: an affected agent
// version, both /v1.0/traces and /v0.6/stats advertised, and client_drop_p0s
// on. Each case in TestV1StatsWorkaroundExclusions trips exactly one
// exclusion on top of it, so the exclusion is necessarily what changed the
// answer.
const v1StatsWorkaroundAffectedInfo = `{"endpoints":["/v0.4/traces","/v1.0/traces","/v0.6/stats"],"client_drop_p0s":true,"version":"7.77.0"}`

// TestV1StatsWorkaroundExclusions covers the exclusion branches of
// forcesStatsForV1Agent that TestV1StatsWorkaroundDoesNotDropP0s's table does
// not reach — the ones that suppress the override even though the agent is
// affected and every other gate is open:
//
//   - Lambda: the Datadog Lambda extension computes trace stats server-side,
//     and contrib/aws/datadog-lambda-go passes WithStatsComputation(false)
//     deliberately, so the override must not reverse that choice.
//   - Datadog-Client-Computed-Stats already sent for another reason
//     (tracing-as-transport, OTLP span metrics, OTLP export mode): the agent
//     never enters its buggy v1.0 concentrator, so forcing the override on
//     would be a no-op.
//
// In every case the wire protocol must stay v1.0: an exclusion suppresses the
// stats override, never the protocol.
func TestV1StatsWorkaroundExclusions(t *testing.T) {
	cases := []struct {
		name string
		// env is applied before the tracer is built, for exclusions whose
		// config is resolved during newConfig.
		env map[string]string
		// startOpt is applied before the agent snapshot is taken, matching a
		// real deployment where the flag is already set at startup.
		startOpt StartOption
		// afterStart is applied to the live config once the tracer is built.
		// The OTLP flags use this because setting them up front would swap the
		// trace writer and stats concentrator for real OTLP exporters (see
		// newTracer), which this test has no endpoint for. forcesStatsForV1Agent
		// reads both flags live, so the exclusion is exercised either way.
		afterStart func(*config)
	}{
		{
			name:     "lambda extension computes stats server-side",
			startOpt: func(c *config) { c.internalConfig.SetIsLambdaFunction(true, internalconfig.OriginCode) },
		},
		{
			name: "tracing-as-transport already sends the CSS header",
			env:  map[string]string{"DD_APM_TRACING_ENABLED": "false"},
		},
		{
			name:       "OTLP span metrics already send the CSS header",
			afterStart: func(c *config) { c.internalConfig.SetOTLPSpanMetricsEnabled(true, internalconfig.OriginCode) },
		},
		{
			name:       "OTLP export mode bypasses the agent's stats path",
			afterStart: func(c *config) { c.internalConfig.SetOTLPExportMode(true, internalconfig.OriginCode) },
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			agent := startTestAgent(t)
			agent.SetInfo(v1StatsWorkaroundAffectedInfo)

			opts := []StartOption{WithStatsComputation(false)}
			if tc.startOpt != nil {
				opts = append(opts, tc.startOpt)
			}
			tr := newTracerTest(t, agent, opts...)
			defer stopTracerTest(tr)

			if tc.afterStart != nil {
				tc.afterStart(tr.config)
			}

			// Guard against a vacuous pass: every agent-side gate must still be
			// open, so canComputeStats can only be false because of the
			// exclusion under test, not because the agent-capability gate in
			// canComputeStatsWithAgent tripped first. Setting
			// AWS_LAMBDA_FUNCTION_NAME instead of isLambdaFunction, for
			// instance, would also set logToStdout and disable the agent
			// outright — which these guards would catch.
			af := tr.config.agent.load()
			require.True(t, af.Stats, "guard: agent must advertise /v0.6/stats")
			require.True(t, af.DropP0s, "guard: agent must advertise client_drop_p0s")
			require.True(t, af.v1TracesAdvertised, "guard: agent must advertise /v1.0/traces")
			require.True(t, af.v1StatsLangUnfixed, "guard: agent version must be inside the affected window")

			assert.False(t, tr.config.canComputeStats(), "canComputeStats")
			assert.False(t, tr.config.canDropP0s(), "canDropP0s")
			assert.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol(), "effectiveTraceProtocol")
		})
	}
}

// TestV1StatsWorkaroundStartupOrderVsTracingAsTransport pins where
// surfaceStatsOverride sits inside newConfig. tracingAsTransport is one of the
// exclusions in forcesStatsForV1Agent, but it is not set until
// apmTracingDisabled runs, which is later in newConfig than the agent snapshot.
// Surfacing the override before that point announces — in a warning and in
// config telemetry — an override that the exclusion immediately makes untrue.
func TestV1StatsWorkaroundStartupOrderVsTracingAsTransport(t *testing.T) {
	rec := new(telemetrytest.RecordClient)
	defer telemetry.MockClient(rec)()
	logs := new(log.RecordLogger)

	t.Setenv("DD_APM_TRACING_ENABLED", "false")
	agent := startTestAgent(t)
	agent.SetInfo(v1StatsWorkaroundAffectedInfo)
	tr := newTracerTest(t, agent, WithStatsComputation(false), WithLogger(logs))
	defer stopTracerTest(tr)

	require.True(t, tr.config.tracingAsTransport,
		"guard: DD_APM_TRACING_ENABLED=false must have taken effect")
	require.False(t, tr.config.canComputeStats(),
		"guard: the tracing-as-transport exclusion must suppress the override")

	// The structural assertion: nothing was ever reported, because the override
	// never applied. effectiveStatsReports is shared with
	// TestPollAgentInfoSurfacesV1StatsWorkaroundTransitions.
	assert.Empty(t, effectiveStatsReports(rec),
		"an override suppressed by an exclusion must not reach config telemetry")
	assert.NotContains(t, strings.Join(logs.Logs(), "\n"), "have been enabled because",
		"an override suppressed by an exclusion must not warn at startup")
}

// TestV1StatsWorkaroundWireBehavior is the end-to-end companion to
// TestV1StatsWorkaroundDoesNotDropP0s: it proves the override actually
// changes what goes out on the wire, which is what makes the agent skip its
// buggy v1.0 stats path in the first place.
func TestV1StatsWorkaroundWireBehavior(t *testing.T) {
	agent := startTestAgent(t)
	agent.SetInfo(`{"endpoints":["/v0.4/traces","/v1.0/traces","/v0.6/stats"],"client_drop_p0s":true,"version":"7.77.0"}`)
	tr := newTracerTest(t, agent, WithStatsComputation(false))
	defer stopTracerTest(tr)

	require.True(t, tr.config.canComputeStats(), "sanity check: the workaround must be active")

	span := tr.StartSpan("op")
	span.Finish()
	flushAgentTracerTest(t, tr, agent, 1)

	assert.Equal(t, []string{tracesAPIPathV1}, agent.Requests(), "the request must still land on /v1.0/traces")
	firstBytes := agent.RequestFirstBytes()
	require.Len(t, firstBytes, 1)
	assert.True(t, isV1WireByte(firstBytes[0]), "expected a msgpack map (v1) body, got byte 0x%02x", firstBytes[0])

	headers := agent.RequestHeaders()
	require.Len(t, headers, 1)
	assert.Equal(t, "yes", headers[0].Get("Datadog-Client-Computed-Stats"),
		"the agent only skips its own v1.0 stats concentrator when this header is set")
}
