// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"math"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"

	"github.com/stretchr/testify/assert"
)

func TestTracerAnalyticsSettings(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		tr := NewTracer(KafkaConfig{})
		assert.True(t, math.IsNaN(tr.cfg.analyticsRate))
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		testutils.SetGlobalAnalyticsRate(t, 0.4)

		tr := NewTracer(KafkaConfig{})
		assert.Equal(t, 0.4, tr.cfg.analyticsRate)
	})

	t.Run("enabled", func(t *testing.T) {
		tr := NewTracer(KafkaConfig{}, WithAnalytics(true))
		assert.Equal(t, 1.0, tr.cfg.analyticsRate)
	})

	t.Run("override", func(t *testing.T) {
		testutils.SetGlobalAnalyticsRate(t, 0.4)

		tr := NewTracer(KafkaConfig{}, WithAnalyticsRate(0.2))
		assert.Equal(t, 0.2, tr.cfg.analyticsRate)
	})

	t.Run("withEnv", func(t *testing.T) {
		t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
		tr := NewTracer(KafkaConfig{})
		assert.True(t, tr.cfg.dataStreamsEnabled)
	})

	t.Run("optionOverridesEnv", func(t *testing.T) {
		t.Setenv("DD_DATA_STREAMS_ENABLED", "false")
		tr := NewTracer(KafkaConfig{})
		WithDataStreams().apply(tr.cfg)
		assert.True(t, tr.cfg.dataStreamsEnabled)
	})
}
