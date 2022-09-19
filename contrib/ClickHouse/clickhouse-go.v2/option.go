// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package clickhouse

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
)

const (
	serviceName = "clickhouse"
)

type connectionConfig struct {
	serviceName   string
	analyticsRate float64
	resourceName  string
	errCheck      func(err error) bool
	withOltpSpan  bool
	withStats     bool
}

// Option represents an option that can be passed to Dial.
type Option func(*connectionConfig)

func defaults(cfg *connectionConfig) {
	cfg.serviceName = serviceName
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_CLICKHOUSE_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
}

// WithServiceName sets the given service name for the dialled connection.
func WithServiceName(name string) Option {
	return func(cfg *connectionConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(cfg *connectionConfig) {
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
	return func(cfg *connectionConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithResourceName sets the given resource name for the dialled connection.
func WithResourceName(resourceName string) Option {
	return func(cfg *connectionConfig) {
		cfg.resourceName = resourceName
	}
}

// WithOLTPspan tells the dialled connection to pass DataDog span identifiers to OLTP integration.
func WithOLTPspan() Option {
	return func(cfg *connectionConfig) {
		cfg.withOltpSpan = true
	}
}

// WithStats tells the dialled connection to gather connection metrics.
func WithStats() Option {
	return func(cfg *connectionConfig) {
		cfg.withStats = true
	}
}

func (c *connectionConfig) shouldIgnoreError(err error) bool {
	return c != nil && c.errCheck != nil && !c.errCheck(err)
}

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever a ClickHouse request
// finishes with an error.
func WithErrorCheck(fn func(err error) bool) Option {
	return func(cfg *connectionConfig) {
		// When the error is explicitly marked as not-an-error, that is
		// when this errCheck function returns false, the APM code will
		// just skip the error and pretend the span was successful.

		// This only affects whether the span/trace is marked as success/error,
		// the calls to the ClickHouse API still return the upstream error code.
		cfg.errCheck = fn
	}
}
