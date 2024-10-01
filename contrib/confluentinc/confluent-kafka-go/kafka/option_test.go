// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package kafka

import (
	"math"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/confluentinc/confluent-kafka-go/kafka/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
)

func TestDataStreamsActivation(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		cfg := internal.NewConfig()
		assert.False(t, cfg.DataStreamsEnabled)
	})
	t.Run("withOption", func(t *testing.T) {
		cfg := internal.NewConfig(WithDataStreams())
		assert.True(t, cfg.DataStreamsEnabled)
	})
	t.Run("withEnv", func(t *testing.T) {
		t.Setenv("DD_DATA_STREAMS_ENABLED", "true")
		cfg := internal.NewConfig()
		assert.True(t, cfg.DataStreamsEnabled)
	})
	t.Run("optionOverridesEnv", func(t *testing.T) {
		t.Setenv("DD_DATA_STREAMS_ENABLED", "false")
		cfg := internal.NewConfig(WithDataStreams())
		assert.True(t, cfg.DataStreamsEnabled)
	})
}

func TestAnalyticsSettings(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		cfg := internal.NewConfig()
		assert.True(t, math.IsNaN(cfg.AnalyticsRate))
	})

	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		cfg := internal.NewConfig()
		assert.Equal(t, 0.4, cfg.AnalyticsRate)
	})

	t.Run("enabled", func(t *testing.T) {
		cfg := internal.NewConfig(WithAnalytics(true))
		assert.Equal(t, 1.0, cfg.AnalyticsRate)
	})

	t.Run("override", func(t *testing.T) {
		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		cfg := internal.NewConfig(WithAnalyticsRate(0.2))
		assert.Equal(t, 0.2, cfg.AnalyticsRate)
	})
}
