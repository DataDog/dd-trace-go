// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"
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
	t.Setenv("DD_TRACE_AGENT_PROTOCOL_VERSION", "1.0")
	agent := startTestAgent(t)
	tr := newTracerTest(t, agent, WithStatsComputation(false))
	defer stopTracerTest(tr)

	require.False(t, tr.config.canComputeStats(), "sanity check: CSS must actually be off")
	assert.Equal(t, traceProtocolV1, tr.config.effectiveTraceProtocol())
	assert.Equal(t, agent.URL()+tracesAPIPathV1, tr.config.ddTransport.endpoint(tr.config.effectiveTraceProtocol()))

	span := tr.StartSpan("op")
	span.Finish()
	flushAgentTracerTest(t, tr, agent, 1)

	assert.Equal(t, []string{tracesAPIPathV1}, agent.Requests(), "the request must land on /v1.0/traces")
	firstBytes := agent.RequestFirstBytes()
	require.Len(t, firstBytes, 1)
	assert.True(t, isV1WireByte(firstBytes[0]), "expected a msgpack map (v1) body, got byte 0x%02x", firstBytes[0])
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
		{"invalid", "garbage"}, // must behave exactly like "unset" (falls back to the v2.10 default, v0.4)
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

					// The invariant under test: v1 iff it was explicitly requested
					// and the agent advertises it. CSS state never enters into it.
					wantV1 := req.env == "1.0" && v1Advertised
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
