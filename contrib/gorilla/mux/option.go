// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package mux

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

type routerConfig struct {
	serviceName   string
	spanOpts      []ddtrace.StartSpanOption // additional span options to be applied
	analyticsRate float64
}

// RouterOption represents an option that can be passed to NewRouter.
type RouterOption func(*routerConfig)

func defaults(cfg *routerConfig) {
	cfg.analyticsRate = globalconfig.AnalyticsRate()
	cfg.serviceName = "mux.router"
}

// WithServiceName sets the given service name for the router.
func WithServiceName(name string) RouterOption {
	return func(cfg *routerConfig) {
		cfg.serviceName = name
	}
}

// WithSpanOptions applies the given set of options to the spans started
// by the router.
func WithSpanOptions(opts ...ddtrace.StartSpanOption) RouterOption {
	return func(cfg *routerConfig) {
		cfg.spanOpts = opts
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) RouterOption {
	return func(cfg *routerConfig) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) RouterOption {
	return func(cfg *routerConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}
