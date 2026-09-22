// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package nethttp

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/agenttest"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/x/tracertest"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httpmem"
)

// TestTracerInternalTransportIsNotTraced pins down the one aspect no other test
// in this package reaches: the HTTP clients dd-trace-go builds for itself are
// left untraced. Tracing them makes each flush produce the spans the next flush
// sends, which never settles.
//
// httpmem is one of the packages that aspect covers, and it is exported, so its
// client carries a transport marked exactly as the agent, remote-config,
// telemetry and profiler clients are. It also serves over (*http.Server).Serve,
// which the server aspect does wrap, so the server span doubles as proof that
// this binary was woven at all. Without it "no client span" would also be the
// reading of a build where nothing applied.
//
// The suites in this package cannot cover this: their mock agent hands the
// tracer its own RoundTripper and answers in-process, so no *http.Transport is
// involved in the tracer's own traffic.
func TestTracerInternalTransportIsNotTraced(t *testing.T) {
	tr, agent, err := tracertest.Bootstrap(t,
		tracer.WithSampler(tracer.NewAllSampler()),
		tracer.WithLogStartup(false),
	)
	require.NoError(t, err)

	srv, client := httpmem.ServerAndClient(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(func() { assert.NoError(t, srv.Close()) })

	resp, err := client.Get("http://httpmem.invalid/probe")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)

	tr.Stop()

	require.NotNil(t,
		agent.FindSpan(agenttest.With().Tag("component", "net/http").Tag("span.kind", "server")),
		"expected the server span, so that the absence of a client span below means the guard held "+
			"rather than that nothing was instrumented")

	assert.Nil(t,
		agent.FindSpan(agenttest.With().Tag("component", "net/http").Tag("span.kind", "client")),
		"dd-trace-go's own HTTP client must not be traced")
}
