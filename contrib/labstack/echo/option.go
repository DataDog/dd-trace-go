// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package echo

import (
	"math"
	"net/http"
)

type config struct {
	serviceName   string
	analyticsRate float64
	filter        func(r *http.Request) bool
}

// Option represents an option that can be passed to Middleware.
type Option func(*config)

func defaults(cfg *config) {
	cfg.serviceName = "echo"
	cfg.analyticsRate = math.NaN()
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

// WithFilter allows to select the requests that will have trace added. If
// filter returns true the trace will be added, otherwise all middleware actions
// will be skipped.
func WithFilter(filter func(r *http.Request) bool) Option {
	return func(cfg *config) {
		cfg.filter = filter
	}
}
