// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package tracer

import (
	"testing"

	pb "github.com/DataDog/datadog-agent/pkg/proto/pbgo/trace"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/telemetry"
	"github.com/DataDog/dd-trace-go/v2/internal/telemetry/telemetrytest"
)

func TestSketchDecodeErrorReportedToTelemetry(t *testing.T) {
	client, capture := telemetrytest.NewCapturingClient(t)
	defer client.Close()
	defer telemetry.MockClient(client)()

	point := decodeAndBuildDataPoint(&pb.ClientGroupedStats{}, []byte{0xff}, 1, 2, false)
	assert.Nil(t, point)
	client.Flush()

	logs := capture.LogMessages()
	require.Len(t, logs, 1)
	entry := logs[0]
	assert.Equal(t, "ERROR", string(entry.Level))
	assert.Equal(t, uint32(1), entry.Count)
	assert.Contains(t, entry.Message, "stats_to_otlp_metrics: failed to decode sketch: error.error_type=")
	assert.NotContains(t, entry.Message, "unexpected EOF")
	assert.Contains(t, entry.StackTrace, "stats_to_otlp_metrics.go")
	assert.Contains(t, entry.StackTrace, "decodeAndBuildDataPoint")
}
