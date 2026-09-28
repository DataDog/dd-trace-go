// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package profiler

import (
	"errors"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigurationSerializationErrorReportedToTelemetry(t *testing.T) {
	client, capture := telemetrytest.NewCapturingClient(t)
	defer client.Close()
	defer telemetry.MockClient(client)()

	cfg, err := defaultConfig()
	require.NoError(t, err)
	logStartupWithMarshaler(cfg, func(any) ([]byte, error) {
		return nil, errors.New("configuration serialization failed")
	})
	client.Flush()

	logs := capture.LogMessages()
	require.Len(t, logs, 1)
	entry := logs[0]
	assert.Equal(t, "ERROR", string(entry.Level))
	assert.Equal(t, uint32(1), entry.Count)
	assert.Equal(t, "Marshaling profiler configuration: error.error_type=errors.errorString", entry.Message)
	assert.Contains(t, entry.StackTrace, "options.go")
	assert.Contains(t, entry.StackTrace, "logStartupWithMarshaler")
}
