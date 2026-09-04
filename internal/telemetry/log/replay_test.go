// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package log

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

// replayCaptureRoundTripper records every request body sent through it,
// decoded as a transport.Body, and answers 200 OK without making a real
// network call. RoundTrip runs synchronously inside client.Flush's HTTP
// call, so bodies is fully populated by the time Flush returns.
type replayCaptureRoundTripper struct {
	t      *testing.T
	bodies []transport.Body
}

func (rt *replayCaptureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	defer req.Body.Close()
	raw, err := io.ReadAll(req.Body)
	require.NoError(rt.t, err)

	var body transport.Body
	require.NoError(rt.t, json.Unmarshal(raw, &body))
	rt.bodies = append(rt.bodies, body)

	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil))}, nil
}

// extractLogMessages returns the transport.LogMessage entries carried by
// body, whether it's a direct "logs" request or a "message-batch" wrapping
// one alongside other payload types.
func extractLogMessages(t *testing.T, body transport.Body) []transport.LogMessage {
	t.Helper()
	switch payload := body.Payload.(type) {
	case *transport.Logs:
		return payload.Logs
	case transport.MessageBatch:
		var logs []transport.LogMessage
		for _, msg := range payload {
			if inner, ok := msg.Payload.(*transport.Logs); ok {
				logs = append(logs, inner.Logs...)
			}
		}
		return logs
	default:
		return nil
	}
}

// TestReportError_QueuedBeforeClientExists_StackPointsAtCallSite proves the
// fix for the replay-time stack capture bug: a report made before any
// telemetry client exists gets queued by globalClientCall, then replayed
// once a client is installed via SwapClient. Before WithStacktraceNow, the
// stack trace attached to the eventual wire payload pointed at the replay
// machinery (SwapClient/Replay/globalClientRecorder), not at this test's own
// call site, because capture was deferred all the way to loggerBackend.add,
// which only runs at replay time in this scenario.
func TestReportError_QueuedBeforeClientExists_StackPointsAtCallSite(t *testing.T) {
	telemetry.StopApp()
	t.Cleanup(telemetry.StopApp)

	require.Nil(t, telemetry.GlobalClient(), "must run before any client exists to exercise the replay path")

	ReportError("dogfood test: queued before a client existed", errors.New("boom"))

	rt := &replayCaptureRoundTripper{t: t}
	client, err := telemetry.NewClient("test-service", "test-env", "1.0.0", telemetry.ClientConfig{
		AgentURL:         "http://localhost:8126",
		HTTPClient:       &http.Client{Transport: rt},
		DependencyLoader: func() (*debug.BuildInfo, bool) { return nil, false }, // keep the flush free of unrelated noise
	})
	require.NoError(t, err)
	defer client.Close()

	// SwapClient (not StartApp) replays the queue synchronously and skips
	// AppStart plus the async flush goroutine StartApp also kicks off,
	// keeping this test deterministic and focused on exactly the replay
	// mechanism under test.
	telemetry.SwapClient(client)
	client.Flush()

	require.NotEmpty(t, rt.bodies, "the queued report must have been flushed")
	// Each body carries at least one log message, so len(bodies) is a lower
	// bound capacity hint; a message-batch may hold several and append grows past it.
	logs := make([]transport.LogMessage, 0, len(rt.bodies))
	for _, body := range rt.bodies {
		logs = append(logs, extractLogMessages(t, body)...)
	}
	require.Len(t, logs, 1)

	stack := logs[0].StackTrace
	assert.Contains(t, stack, "TestReportError_QueuedBeforeClientExists_StackPointsAtCallSite",
		"stack must show this test's own call site")
	assert.NotContains(t, stack, "Replay", "must not show the replay machinery")
	assert.NotContains(t, stack, "SwapClient")
	assert.NotContains(t, stack, "globalClientRecorder")
}
