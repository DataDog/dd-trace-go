// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gocql

import (
	"math"

	"gopkg.in/CodapeWild/dd-trace-go.v1/internal"
)

type queryConfig struct {
	serviceName, resourceName string
	noDebugStack              bool
	analyticsRate             float64
}

// WrapOption represents an option that can be passed to WrapQuery.
type WrapOption func(*queryConfig)

func defaults(cfg *queryConfig) {
	cfg.serviceName = "gocql.query"
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_GOCQL_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
}

// WithServiceName sets the given service name for the returned query.
func WithServiceName(name string) WrapOption {
	return func(cfg *queryConfig) {
		cfg.serviceName = name
	}
}

// WithResourceName sets a custom resource name to be used with the traced query.
// By default, the query statement is extracted automatically. This method should
// be used when a different resource name is desired or in performance critical
// environments. The gocql library returns the query statement using an fmt.Sprintf
// call, which can be costly when called repeatedly. Using WithResourceName will
// avoid that call. Under normal circumstances, it is safe to rely on the default.
func WithResourceName(name string) WrapOption {
	return func(cfg *queryConfig) {
		cfg.resourceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) WrapOption {
	return func(cfg *queryConfig) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) WrapOption {
	return func(cfg *queryConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() WrapOption {
	return func(cfg *queryConfig) {
		cfg.noDebugStack = true
	}
}
