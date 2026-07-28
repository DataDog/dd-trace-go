// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package metric

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestRuntimeProducerEmitsScheduleDuration(t *testing.T) {
	p := NewRuntimeProducer()
	require.NotNil(t, p)

	scopes, err := p.Produce(context.Background())
	require.NoError(t, err)
	require.Len(t, scopes, 1, "producer should emit a single ScopeMetrics")

	scope := scopes[0]
	assert.Equal(t, "go.runtime", scope.Scope.Name)
	require.Len(t, scope.Metrics, 1, "producer should emit a single metric")

	m := scope.Metrics[0]
	assert.Equal(t, "go.schedule.duration", m.Name)
	assert.Equal(t, "s", m.Unit)

	hist, ok := m.Data.(metricdata.Histogram[float64])
	require.True(t, ok, "data should be Histogram[float64]")
	assert.Equal(t, metricdata.CumulativeTemporality, hist.Temporality)
	require.Len(t, hist.DataPoints, 1)

	dp := hist.DataPoints[0]
	// Bounds and BucketCounts must be consistent with the OTel histogram shape:
	// BucketCounts has one more entry than Bounds (the implicit +Inf overflow).
	assert.Equal(t, len(dp.Bounds)+1, len(dp.BucketCounts),
		"BucketCounts must have len(Bounds)+1 entries")
	assert.GreaterOrEqual(t, dp.Count, uint64(0))
	assert.GreaterOrEqual(t, dp.Sum, float64(0))
	assert.False(t, dp.StartTime.IsZero(), "StartTime must be set")
	assert.False(t, dp.Time.IsZero(), "Time must be set")
}
