// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package gin

import (
	"math"

	"github.com/gin-gonic/gin"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

type config struct {
	analyticsRate float64
	resourceNamer func(c *gin.Context) string
}

func newConfig() *config {
	return &config{
		analyticsRate: globalconfig.AnalyticsRate(),
		resourceNamer: defaultResourceNamer,
	}
}

// Option specifies instrumentation configuration options.
type Option func(*config)

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(cfg *config) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithResourceNamer specifies a function which will be used to obtain a resource name for a given
// gin request, using the request's context.
func WithResourceNamer(namer func(c *gin.Context) string) Option {
	return func(cfg *config) {
		cfg.resourceNamer = namer
	}
}

func defaultResourceNamer(c *gin.Context) string {
	return c.HandlerName()
}
