// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/statsdtest"
)

// TestSendURLMatchesPayloadEvenAfterConfigChanges pins the invariant that makes
// a wire-format/URL mismatch unrepresentable: send() picks its URL from the
// payload's own protocol, never from live config. A payload encoded as v1.0
// therefore still goes to /v1.0/traces even if the configured protocol changed
// after it was created — the alternative (posting v1.0 bytes to /v0.4/traces)
// is the failure this design rules out.
func TestSendURLMatchesPayloadEvenAfterConfigChanges(t *testing.T) {
	agent := startTestAgent(t)
	tr := newTracerTest(t, agent)
	defer stopTracerTest(tr)

	require.Equal(t, traceProtocolV1, tr.config.internalConfig.RequestedTraceProtocol(),
		"sanity check: the mock agent advertises /v1.0/traces and /v0.6/stats, so v1 must survive newConfig")

	w := newAgentTraceWriter(tr.config, newPrioritySampler(), tr.statsd)
	require.Equal(t, traceProtocolV1, w.payload.protocol(), "payload created while v1 was in effect must be v1")

	// Change the configured protocol out from under the already-encoded payload.
	tr.config.internalConfig.SetTraceProtocol(traceProtocolV04, internalconfig.OriginCalculated)
	require.Equal(t, traceProtocolV04, tr.config.internalConfig.RequestedTraceProtocol(), "sanity check: config did change")

	w.add([]*Span{makeSpan(1)})
	w.flush()
	w.wg.Wait()

	assert.Equal(t, []string{tracesAPIPathV1}, agent.Requests(),
		"the payload's own protocol must win, not the config's current protocol")
}

// TestSendReportsAPIErrorsWithCorrectEndpointTag covers a reporting bug the
// per-protocol URLs make impossible: the api.errors metric hardcoded
// /v0.4/traces, so a v1.0 send failure was attributed to the wrong endpoint.
func TestSendReportsAPIErrorsWithCorrectEndpointTag(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/info" {
			w.Write([]byte(`{"endpoints":["/v1.0/traces","/v0.6/stats"],"client_drop_p0s":true}`))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	require.NoError(t, err)
	var tg statsdtest.TestStatsdClient
	trc, err := newTracer(WithAgentAddr(u.Host), withStatsdClient(&tg))
	require.NoError(t, err)
	setGlobalTracer(trc)
	defer func() {
		trc.Stop()
		setGlobalTracer(&NoopTracer{})
	}()

	require.Equal(t, traceProtocolV1, trc.config.internalConfig.RequestedTraceProtocol(), "sanity check: agent must resolve to v1")

	p := newPayload(traceProtocolV1)
	_, err = p.push(getTestTrace(1, 1)[0])
	require.NoError(t, err)

	_, err = trc.config.ddTransport.send(p)
	require.Error(t, err)

	calls := statsdtest.FilterCallsByName(tg.IncrCalls(), "datadog.tracer.api.errors")
	require.Len(t, calls, 1)
	assert.Equal(t, []string{"reason:server_response_500", "endpoint:" + tracesAPIPathV1}, calls[0].Tags())
}
