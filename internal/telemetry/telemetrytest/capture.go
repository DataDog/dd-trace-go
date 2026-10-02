// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package telemetrytest

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"runtime/debug"
	"sync"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

// CaptureRoundTripper records every request body sent through it, decoded as
// a transport.Body — the path transport.Body.UnmarshalJSON documents as
// "used to test the telemetry client end to end" — and answers 200 OK
// without making a real network call.
type CaptureRoundTripper struct {
	t  testing.TB
	mu sync.Mutex

	bodies []transport.Body
}

// RoundTrip implements http.RoundTripper.
func (rt *CaptureRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	defer req.Body.Close()
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		rt.t.Fatalf("telemetrytest.CaptureRoundTripper: reading request body: %s", err.Error())
	}

	var body transport.Body
	if err := json.Unmarshal(raw, &body); err != nil {
		rt.t.Fatalf("telemetrytest.CaptureRoundTripper: decoding transport.Body: %s", err.Error())
	}

	rt.mu.Lock()
	rt.bodies = append(rt.bodies, body)
	rt.mu.Unlock()

	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(bytes.NewReader(nil))}, nil
}

// Bodies returns every transport.Body captured so far.
func (rt *CaptureRoundTripper) Bodies() []transport.Body {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	bodies := make([]transport.Body, len(rt.bodies))
	copy(bodies, rt.bodies)
	return bodies
}

// LogMessages returns every transport.LogMessage carried by the captured
// bodies, whether a direct "logs" request or nested inside a "message-batch"
// alongside other payload types.
func (rt *CaptureRoundTripper) LogMessages() []transport.LogMessage {
	var logs []transport.LogMessage
	for _, body := range rt.Bodies() {
		switch payload := body.Payload.(type) {
		case *transport.Logs:
			logs = append(logs, payload.Logs...)
		case transport.MessageBatch:
			for _, msg := range payload {
				if inner, ok := msg.Payload.(*transport.Logs); ok {
					logs = append(logs, inner.Logs...)
				}
			}
		}
	}
	return logs
}

// NewCapturingClient builds a real telemetry.Client wired to a
// CaptureRoundTripper instead of a network endpoint, so its outbound
// requests can be asserted on directly instead of requiring a real agent or
// backend. AgentURL is a placeholder the round tripper never dials — it
// exists only because telemetry.NewClient requires a non-empty endpoint.
func NewCapturingClient(t testing.TB) (telemetry.Client, *CaptureRoundTripper) {
	t.Helper()

	rt := &CaptureRoundTripper{t: t}
	client, err := telemetry.NewClient("test-service", "test-env", "1.0.0", telemetry.ClientConfig{
		AgentURL:         "http://localhost:8126",
		HTTPClient:       &http.Client{Transport: rt},
		DependencyLoader: func() (*debug.BuildInfo, bool) { return nil, false },
	})
	if err != nil {
		t.Fatalf("telemetrytest.NewCapturingClient: %s", err.Error())
	}

	return client, rt
}
