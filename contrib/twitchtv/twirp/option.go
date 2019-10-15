// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package twirp

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

type config struct {
	serviceName   string
	analyticsRate float64
}

// Option represents an option that can be passed to Dial.
type Option func(*config)

func defaults(cfg *config) {
	cfg.analyticsRate = globalconfig.AnalyticsRate()
}

// WithServiceName sets the given service name for the dialled connection.
// When the service name is not explicitly set, it will be inferred based on the
// request to the twirp service.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

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
