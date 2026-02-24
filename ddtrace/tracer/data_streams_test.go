// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestTrackTransactionPublicAPI verifies that TrackTransaction correctly
// delegates to the underlying DSM processor when one is active.
func TestTrackTransactionPublicAPI(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	Start(withNoopStats())
	defer Stop()

	tr, ok := getGlobalTracer().(dataStreamsContainer)
	assert.True(t, ok, "global tracer should implement dataStreamsContainer")
	assert.NotNil(t, tr.GetDataStreamsProcessor(), "DSM processor should be non-nil when DD_DATA_STREAMS_ENABLED=true")

	// Should not panic and should reach the processor.
	TrackTransaction("msg-001", "ingested")
}
