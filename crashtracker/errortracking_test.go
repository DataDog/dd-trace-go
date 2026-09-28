// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package crashtracker

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestUnexpectedMonitorExitReportedToTelemetry(t *testing.T) {
	client, rt := telemetrytest.NewCapturingClient(t)
	defer telemetry.MockClient(client)()

	reportMonitorExitError(errors.New("exit status 2"))
	client.Flush()

	logs := rt.LogMessages()
	require.Len(t, logs, 1)
	assert.True(t, strings.HasPrefix(logs[0].Message, "crashtracker: monitor process exited unexpectedly"))
	assert.Contains(t, logs[0].Message, "error.error_type=errors.errorString")
	assert.Equal(t, telemetry.LogError, logs[0].Level)
	assert.EqualValues(t, 1, logs[0].Count)
	assert.Contains(t, logs[0].StackTrace, "reportMonitorExitError")
}
