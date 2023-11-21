// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"math"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
)

func TestDataStreamsActivation(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cfg := newConfig()
		assert.False(t, cfg.dataStreamsEnabled)
	})
	t.Run("withOption", func(t *testing.T) {
		cfg := newConfig(WithDataStreams())
		assert.True(t, cfg.dataStreamsEnabled)
	})
	t.Run("withEnv", func(t *testing.T) {
		t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
		cfg := newConfig()
		assert.True(t, cfg.dataStreamsEnabled)
	})
	t.Run("optionOverridesEnv", func(t *testing.T) {
		t.Setenv("DD_DATA_STREAMS_ENABLED", "false")
		cfg := newConfig(WithDataStreams())
		assert.True(t, cfg.dataStreamsEnabled)
	})
}

func TestAnalyticsSettings(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg := newConfig()
		assert.True(t, math.IsNaN(cfg.analyticsRate))
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		cfg := newConfig()
		assert.Equal(t, 0.4, cfg.analyticsRate)
	})

	t.Run("enabled", func(t *testing.T) {
		cfg := newConfig(WithAnalytics(true))
		assert.Equal(t, 1.0, cfg.analyticsRate)
	})

	t.Run("override", func(t *testing.T) {
		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		cfg := newConfig(WithAnalyticsRate(0.2))
		assert.Equal(t, 0.2, cfg.analyticsRate)
	})
}
