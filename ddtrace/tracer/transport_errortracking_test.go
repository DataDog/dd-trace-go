// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMalformedAgentInfoReportedToTelemetry(t *testing.T) {
	client, capture := telemetrytest.NewCapturingClient(t)
	defer client.Close()
	defer telemetry.MockClient(client)()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":]`))
	}))
	defer server.Close()
	agentURL, err := url.Parse(server.URL)
	require.NoError(t, err)

	_, state := loadAgentFeatures(false, agentURL, server.Client())
	assert.Equal(t, protoUnknown, state)
	client.Flush()

	logs := capture.LogMessages()
	require.Len(t, logs, 1)
	entry := logs[0]
	assert.Equal(t, "ERROR", string(entry.Level))
	assert.Equal(t, uint32(1), entry.Count)
	assert.Contains(t, entry.Message, "Failed to decode agent info response: error.error_type=encoding/json.SyntaxError")
	assert.NotContains(t, entry.Message, "invalid character")
	assert.Contains(t, entry.StackTrace, "option.go")
	assert.Contains(t, entry.StackTrace, "loadAgentFeatures")
}

func TestStatsSerializationErrorsReportedToTelemetry(t *testing.T) {
	client, capture := telemetrytest.NewCapturingClient(t)
	defer client.Close()
	defer telemetry.MockClient(client)()

	reportStatsSendError(&statsSerializationError{format: "msgpack", err: errors.New("msgpack failed")})
	reportStatsSendError(&statsSerializationError{format: "otlp", err: errors.New("otlp failed")})
	reportStatsSendError(errors.New("network failed"))
	client.Flush()

	logs := capture.LogMessages()
	require.Len(t, logs, 2, "transport errors must not be reported")
	formats := make(map[string]bool, len(logs))
	for _, entry := range logs {
		assert.Equal(t, "ERROR", string(entry.Level))
		assert.Equal(t, uint32(1), entry.Count)
		assert.Equal(t, "Error serializing stats payload: error.error_type=errors.errorString", entry.Message)
		assert.Contains(t, entry.StackTrace, "stats.go")
		assert.Contains(t, entry.StackTrace, "reportStatsSendError")
		formats[entry.Tags] = true
	}
	assert.Equal(t, map[string]bool{"format:msgpack": true, "format:otlp": true}, formats)
}
