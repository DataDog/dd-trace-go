// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTrackDataStreamsTransactionPublicAPI verifies that TrackDataStreamsTransaction correctly
// delegates to the underlying DSM processor when one is active.
func TestTrackDataStreamsTransactionPublicAPI(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	Start(withNoopStats())
	defer Stop()

	tr, ok := getGlobalTracer().(dataStreamsContainer)
	assert.True(t, ok, "global tracer should implement dataStreamsContainer")
	assert.NotNil(t, tr.GetDataStreamsProcessor(), "DSM processor should be non-nil when DD_DATA_STREAMS_ENABLED=true")

	// Should not panic and should reach the processor.
	TrackDataStreamsTransaction(context.Background(), "msg-001", "ingested")
}

// TestTrackDataStreamsTransactionTagsSpan verifies that the active span in the context
// is tagged with the DSM transaction ID and checkpoint name.
func TestTrackDataStreamsTransactionTagsSpan(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	Start(withNoopStats())
	defer Stop()

	span, ctx := StartSpanFromContext(context.Background(), "test.op")
	defer span.Finish()

	TrackDataStreamsTransaction(ctx, "tx-span-tag", "processed")

	s, ok := SpanFromContext(ctx)
	require.True(t, ok)
	meta := s.getMetadata()
	assert.Equal(t, "tx-span-tag", meta[ext.DSMTransactionID])
	assert.Equal(t, "processed", meta[ext.DSMTransactionCheckpoint])
}

// TestTrackDataStreamsTransactionNoSpanInContextNoops verifies that when the context
// contains no span, tagging is silently skipped and the function does not panic.
func TestTrackDataStreamsTransactionNoSpanInContextNoops(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	Start(withNoopStats())
	defer Stop()

	assert.NotPanics(t, func() {
		TrackDataStreamsTransaction(context.Background(), "tx-no-span", "ingested")
	})
}

// TestTrackDataStreamsTransactionAtDelegatesToProcessor verifies that
// TrackDataStreamsTransactionAt forwards the provided time to the processor.
func TestTrackDataStreamsTransactionAtDelegatesToProcessor(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	Start(withNoopStats())
	defer Stop()

	fixedTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	assert.NotPanics(t, func() {
		TrackDataStreamsTransactionAt(context.Background(), "tx-at-001", "delivered", fixedTime)
	})
}

// TestTrackDataStreamsTransactionAtTagsSpan verifies that TrackDataStreamsTransactionAt
// also tags the active span in the context.
func TestTrackDataStreamsTransactionAtTagsSpan(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	Start(withNoopStats())
	defer Stop()

	span, ctx := StartSpanFromContext(context.Background(), "test.op")
	defer span.Finish()

	fixedTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	TrackDataStreamsTransactionAt(ctx, "tx-at-span", "delivered", fixedTime)

	s, ok := SpanFromContext(ctx)
	require.True(t, ok)
	meta := s.getMetadata()
	assert.Equal(t, "tx-at-span", meta[ext.DSMTransactionID])
	assert.Equal(t, "delivered", meta[ext.DSMTransactionCheckpoint])
}
