// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	"math"

	"gopkg.in/CodapeWild/dd-trace-go.v1/internal"
	"gopkg.in/CodapeWild/dd-trace-go.v1/internal/globalconfig"
)

type config struct {
	serviceName   string
	analyticsRate float64
}

// Option represents an option that can be passed to Middleware.
type Option func(*config)

func defaults(cfg *config) {
	cfg.serviceName = "echo"
	if svc := globalconfig.ServiceName(); svc != "" {
		cfg.serviceName = svc
	}
	if internal.BoolEnv("DD_TRACE_ECHO_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
}

// WithServiceName sets the given service name for the system.
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
