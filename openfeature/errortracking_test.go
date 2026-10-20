// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package openfeature

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestInternalFeatureFlagFailuresReportedToTelemetry(t *testing.T) {
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	reportMalformedAgentlessConfiguration(errors.New("decode"))
	reportFlagEvalPayloadEncodingError(errors.New("encode"))
	handleFlagEvalMetricsCreationError(errors.New("instrument"))
	handleFlagEvalMetricsCreationError(&flagEvalMeterProviderError{err: errors.New("user configuration")})
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 3)
	for _, message := range []string{
		"openfeature: agentless: received malformed configuration",
		"openfeature: failed to encode flag evaluation payload",
		"openfeature: failed to create flag evaluation metrics",
	} {
		got := -1
		for i, candidate := range logs {
			if strings.HasPrefix(candidate.Message, message) {
				got = i
				break
			}
		}
		require.NotEqual(t, -1, got)
		assert.Contains(t, logs[got].Message, "error.error_type=errors.errorString")
		assert.Equal(t, telemetry.LogError, logs[got].Level)
		assert.EqualValues(t, 1, logs[got].Count)
		assert.Contains(t, logs[got].StackTrace, "report")
	}
}
