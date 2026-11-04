// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package log

import (
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/internal/transport"
)

// telemetrytest.RecordClient discards LogOptions and record attrs, keeping
// only {Level, Text} (see internal/telemetry/telemetrytest/record.go), so it
// cannot see anything ReportError/ReportPanic actually add: the error_type
// attribute, the stack trace, or dedup-by-count. These tests drive the real
// loggerBackend and a fake HTTP agent instead, asserting on the wire-format
// transport.Logs payload.

// fakeAgent captures every telemetry request body posted to it, decoded via
// transport.Body's own UnmarshalJSON (its doc says it exists for exactly this).
type fakeAgent struct {
	mu     sync.Mutex
	bodies []transport.Body
}

func newFakeAgent(t *testing.T) (*fakeAgent, telemetry.Client) {
	t.Helper()
	agent := &fakeAgent{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var body transport.Body
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		agent.mu.Lock()
		agent.bodies = append(agent.bodies, body)
		agent.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	client, err := telemetry.NewClient("test-service", "test-env", "1.0.0", telemetry.ClientConfig{
		AgentURL: srv.URL,
	})
	require.NoError(t, err)
	t.Cleanup(func() { client.Close() })

	return agent, client
}

// logMessages flattens every transport.Logs entry seen so far, whether sent
// standalone or wrapped in a transport.MessageBatch.
func (a *fakeAgent) logMessages() []transport.LogMessage {
	a.mu.Lock()
	defer a.mu.Unlock()

	var out []transport.LogMessage
	for _, body := range a.bodies {
		switch payload := body.Payload.(type) {
		case *transport.Logs:
			out = append(out, payload.Logs...)
		case transport.MessageBatch:
			for _, msg := range payload {
				if logs, ok := msg.Payload.(*transport.Logs); ok {
					out = append(out, logs.Logs...)
				}
			}
		}
	}
	return out
}

func TestReportError_WireFormat(t *testing.T) {
	agent, client := newFakeAgent(t)

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = client.Log

	ReportError("dogfood test: swallowed sdk error", errors.New("boom"))
	client.Flush()

	logs := agent.logMessages()
	require.Len(t, logs, 1)

	entry := logs[0]
	assert.Equal(t, transport.LogLevelError, entry.Level)
	assert.Equal(t, uint32(1), entry.Count)
	assert.Equal(t, "dogfood test: swallowed sdk error: error.error_type=errors.errorString", entry.Message)
	require.NotEmpty(t, entry.StackTrace, "ReportError always attaches a stack trace via telemetry.WithStacktrace()")
	assert.Contains(t, entry.StackTrace, "report_backend_test.go", "the real call site must be present and unredacted (it is dd-trace-go's own code)")
}

func TestReportError_DedupCollapsesByConstantMessageOnly(t *testing.T) {
	agent, client := newFakeAgent(t)

	orig := sendLog
	defer func() { sendLog = orig }()
	sendLog = client.Log

	// Same constant message, different error types: the dedup key is
	// (message, level, tags) computed from the pre-format record.Message, so
	// these collapse into a single entry whose attrs are frozen from whichever
	// call wins the race to create the entry — never both.
	ReportError("dogfood test: dedup case", errors.New("first"))
	ReportError("dogfood test: dedup case", &net.OpError{Op: "dial", Err: errors.New("second")})
	client.Flush()

	logs := agent.logMessages()
	require.Len(t, logs, 1, "identical (message, level, tags) must dedup into one entry regardless of differing error types")
	assert.Equal(t, uint32(2), logs[0].Count)
	assert.True(t, strings.HasPrefix(logs[0].Message, "dogfood test: dedup case: error.error_type="))
}
