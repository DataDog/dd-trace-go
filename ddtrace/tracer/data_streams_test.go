// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The tag keys the removed transaction tracking used to set. Spelled out as
// literals rather than referencing the deprecated ext constants, so this test
// pins the wire keys that must no longer appear on spans.
const (
	dsmTransactionIDTag         = "dsm.transaction.id"
	dsmTransactionCheckpointTag = "dsm.transaction.checkpoint"
)

// TestTrackDataStreamsTransactionIsNoop verifies that the deprecated transaction
// tracking API is safe to call and records nothing: no panic, and no transaction
// tags on the active span.
func TestTrackDataStreamsTransactionIsNoop(t *testing.T) {
	t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
	t.Setenv("DD_INSTRUMENTATION_TELEMETRY_ENABLED", "false")
	Start(withNoopStats())
	defer Stop()

	// The DSM processor is still running; the removed feature must simply not feed it.
	tr, ok := getGlobalTracer().(dataStreamsContainer)
	require.True(t, ok, "global tracer should implement dataStreamsContainer")
	require.NotNil(t, tr.GetDataStreamsProcessor(), "DSM processor should be non-nil when DD_DATA_STREAMS_ENABLED=true")

	fixedTime := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	t.Run("safe with no span in context", func(t *testing.T) {
		assert.NotPanics(t, func() {
			TrackDataStreamsTransaction(context.Background(), "tx-no-span", "ingested")
			TrackDataStreamsTransactionAt(context.Background(), "tx-no-span", "ingested", fixedTime)
		})
	})

	t.Run("safe with nil context", func(t *testing.T) {
		assert.NotPanics(t, func() {
			//nolint:staticcheck // SA1012: passing a nil context is exactly what this asserts is safe.
			TrackDataStreamsTransaction(nil, "tx-nil-ctx", "ingested")
		})
	})

	t.Run("does not tag the active span", func(t *testing.T) {
		span, ctx := StartSpanFromContext(context.Background(), "test.op")
		defer span.Finish()

		TrackDataStreamsTransaction(ctx, "tx-span-tag", "processed")
		TrackDataStreamsTransactionAt(ctx, "tx-at-span", "delivered", fixedTime)

		s, ok := SpanFromContext(ctx)
		require.True(t, ok)
		_, ok = s.meta.Get(dsmTransactionIDTag)
		assert.False(t, ok, "transaction tracking was removed; %s must not be set", dsmTransactionIDTag)
		_, ok = s.meta.Get(dsmTransactionCheckpointTag)
		assert.False(t, ok, "transaction tracking was removed; %s must not be set", dsmTransactionCheckpointTag)
	})
}
