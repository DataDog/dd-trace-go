// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package remoteconfig

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

// malformedConfigServer returns an httptest.Server whose /v0.7/config
// response is a 200 OK with a body that isn't valid JSON — the same fault
// internal/apps/telemetry-errors/rcfaultproxy injects in front of a real
// agent. It's guaranteed to fail json.Unmarshal and isn't one of the "{}"/
// "null" no-op literals updateState special-cases.
func malformedConfigServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"targets": this is not valid json`))
	}))
}

// TestUpdateState_MalformedJSON_ReportsWellFormedError proves updateState's
// LogAndReportError call produces a well-formed Error Tracking payload when
// the remote-config poll response isn't valid JSON, verified here with a
// plain httptest.Server — no real agent needed. See
// internal/apps/telemetry-errors/README.md's tier-0 checklist.
func TestUpdateState_MalformedJSON_ReportsWellFormedError(t *testing.T) {
	server := malformedConfigServer()
	defer server.Close()

	rcClient, err := newClient(ClientConfig{AgentURL: server.URL})
	require.NoError(t, err)

	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	rcClient.updateState()
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 1)
	msg := logs[0]

	// message_match
	assert.True(t, strings.HasPrefix(msg.Message, "remoteconfig: http request error: could not parse the json response body"),
		"message: %s", msg.Message)
	// error_type_present
	assert.Contains(t, msg.Message, "error.error_type=encoding/json.SyntaxError")
	assert.Equal(t, telemetry.LogError, msg.Level)
	assert.EqualValues(t, 1, msg.Count)
	// stack_points_at_call_site / no_replay_frames
	assert.Contains(t, msg.StackTrace, "updateState")
	assert.NotContains(t, msg.StackTrace, "Replay")
	assert.NotContains(t, msg.StackTrace, "globalClientRecorder")
	assert.NotContains(t, msg.StackTrace, "SwapClient")
}

// TestUpdateState_MalformedJSON_DedupCount mirrors the tracer-side dedup
// test: repeated failures with the same constant message dedup into a
// single log entry with an incremented count.
func TestUpdateState_MalformedJSON_DedupCount(t *testing.T) {
	server := malformedConfigServer()
	defer server.Close()

	rcClient, err := newClient(ClientConfig{AgentURL: server.URL})
	require.NoError(t, err)

	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	rcClient.updateState()
	rcClient.updateState()
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 1)
	assert.EqualValues(t, 2, logs[0].Count)
}
