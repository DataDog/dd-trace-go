// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package sql

import (
	"testing"
	"time"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"github.com/stretchr/testify/assert"
)

func TestAnalyticsSettings(t *testing.T) {
	t.Run("global", func(t *testing.T) {
		t.Skip("global flag disabled")
		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		cfg := new(registerConfig)
		defaults(cfg, "", nil)
		assert.Equal(t, 0.4, cfg.analyticsRate)
	})

	t.Run("enabled", func(t *testing.T) {
		cfg := new(registerConfig)
		defaults(cfg, "", nil)
		WithAnalytics(true)(cfg)
		assert.Equal(t, 1.0, cfg.analyticsRate)
	})

	t.Run("override", func(t *testing.T) {
		rate := globalconfig.AnalyticsRate()
		defer globalconfig.SetAnalyticsRate(rate)
		globalconfig.SetAnalyticsRate(0.4)

		cfg := new(registerConfig)
		defaults(cfg, "", nil)
		WithAnalyticsRate(0.2)(cfg)
		assert.Equal(t, 0.2, cfg.analyticsRate)
	})
}

func TestWithDBStats(t *testing.T) {
	t.Run("default off", func(t *testing.T) {
		cfg := new(config)
		defaults(cfg, "", nil)
		assert.True(t, int64(cfg.dbStats) == 0)
		assert.False(t, dbStatsEnabled(cfg))
	})
	t.Run("on", func(t *testing.T) {
		cfg := new(config)
		defaults(cfg, "", nil)
		WithDBStats(1 * time.Second)
		assert.True(t, int64(cfg.dbStats) == 1)
		assert.True(t, dbStatsEnabled(cfg))
	})
	t.Run("interval 0", func(t *testing.T) {
		// this test demonstrates that the logic for checking whether DBStats is enabled, is for the interval to be > 0
		cfg := new(config)
		defaults(cfg, "", nil)
		WithDBStats(0 * time.Second)
		assert.True(t, int64(cfg.dbStats) == 0)
		assert.False(t, dbStatsEnabled(cfg))
	})
}
