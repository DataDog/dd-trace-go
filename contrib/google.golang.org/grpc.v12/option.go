// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

type interceptorConfig struct {
	serviceName   string
	analyticsRate float64
}

// InterceptorOption represents an option that can be passed to the grpc unary
// client and server interceptors.
type InterceptorOption func(*interceptorConfig)

func defaults(cfg *interceptorConfig) {
	// cfg.serviceName default set in interceptor
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_GRPC_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
}

// WithServiceName sets the given service name for the intercepted client.
func WithServiceName(name string) InterceptorOption {
	return func(cfg *interceptorConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) InterceptorOption {
	return func(cfg *interceptorConfig) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) InterceptorOption {
	return func(cfg *interceptorConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}
