// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package bun

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

type config struct {
	serviceName   string
	analyticsRate float64
}

// Option represents an option that can be used to create or wrap a client.
type Option func(*config)

func defaults(cfg *config) {
	service := "bun.db"
	if svc := globalconfig.ServiceName(); svc != "" {
		service = svc
	}
	cfg.serviceName = service
	if internal.BoolEnv("DD_TRACE_BUN_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
}

// WithService sets the given service name for the client.
func WithService(name string) Option {
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
