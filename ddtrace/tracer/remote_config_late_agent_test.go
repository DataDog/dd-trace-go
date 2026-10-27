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
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DataDog/datadog-agent/pkg/remoteconfig/state"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/remoteconfig"
)

// lateAgent is a minimal agent: /info advertises /v0.7/config when
// rcAdvertised is true, and /v0.7/config records whether the tracer's remote
// config client asked for the APM_TRACING product.
type lateAgent struct {
	rcAdvertised       atomic.Bool
	apmTracingRequests atomic.Int32
}

func (a *lateAgent) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/info":
		endpoints := []string{"/v0.4/traces"}
		if a.rcAdvertised.Load() {
			endpoints = append(endpoints, "/v0.7/config")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"endpoints": endpoints})
	case "/v0.7/config":
		var req struct {
			Client struct {
				Products []string `json:"products"`
			} `json:"client"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if slices.Contains(req.Client.Products, state.ProductAPMTracing) {
			a.apmTracingRequests.Add(1)
		}
		_, _ = w.Write([]byte("{}"))
	default:
		w.WriteHeader(http.StatusOK)
	}
}

// requireAPMTracingPolledEventually asserts from the agent's side that the
// tracer started remote config and polls for APM_TRACING (sampling rules etc.).
func requireAPMTracingPolledEventually(t *testing.T, a *lateAgent) {
	t.Helper()
	assert.Eventually(t, func() bool { return a.apmTracingRequests.Load() > 0 },
		5*time.Second, 20*time.Millisecond,
		"remote config should start and poll APM_TRACING once the agent advertises /v0.7/config")
}

// TestRemoteConfigStartsWhenAgentBecomesReachable reproduces a tracer that
// starts before its agent is listening (common with sidecar agents, a DDOT
// collector started alongside the app, or a node agent still rolling out).
// The startup /info fetch fails with "connection refused", so the tracer used
// to skip remote config for the whole process lifetime: remote sampling rules
// and other APM_TRACING settings were never applied, even though the /info
// poller later saw the agent advertise /v0.7/config.
func TestRemoteConfigStartsWhenAgentBecomesReachable(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "false") // keep AppSec from starting the shared RC client on its own
	t.Cleanup(remoteconfig.Reset)

	// Reserve an address, then free it so nothing listens there at tracer start.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())

	tr, err := newTracer(
		WithAgentAddr(addr),
		WithAgentTimeout(1),
		withAgentInfoPollInterval(20*time.Millisecond),
	)
	require.NoError(t, err)
	defer tr.Stop()
	tr.startAppSec()

	_, err = remoteconfig.HasProduct(state.ProductAPMTracing)
	require.ErrorIs(t, err, remoteconfig.ErrClientNotStarted, "agent is down: remote config cannot start yet")

	// The agent comes up on the same address, with remote config enabled.
	agent := &lateAgent{}
	agent.rcAdvertised.Store(true)
	agentLn, err := net.Listen("tcp", addr)
	require.NoError(t, err)
	srv := httptest.NewUnstartedServer(agent)
	srv.Listener = agentLn
	srv.Start()
	defer srv.Close()

	requireAPMTracingPolledEventually(t, agent)
}

// TestRemoteConfigStartsWhenAgentEnablesItLater covers an agent that is
// reachable at tracer start but only enables remote configuration afterwards.
func TestRemoteConfigStartsWhenAgentEnablesItLater(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "false")
	t.Cleanup(remoteconfig.Reset)

	agent := &lateAgent{}
	srv := httptest.NewServer(agent)
	defer srv.Close()

	tr, err := newTracer(
		WithAgentAddr(srv.Listener.Addr().String()),
		WithAgentTimeout(1),
		withAgentInfoPollInterval(20*time.Millisecond),
	)
	require.NoError(t, err)
	defer tr.Stop()
	tr.startAppSec()

	_, err = remoteconfig.HasProduct(state.ProductAPMTracing)
	require.ErrorIs(t, err, remoteconfig.ErrClientNotStarted, "agent does not advertise remote config yet")

	agent.rcAdvertised.Store(true)

	requireAPMTracingPolledEventually(t, agent)
}
