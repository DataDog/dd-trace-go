// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redigo // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/gomodule/redigo"

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

type dialConfig struct {
	serviceName    string
	spanName       string
	analyticsRate  float64
	connectionType int
}

const defaultServiceName = "redis.conn"

const (
	connectionTypeWithTimeout = iota
	connectionTypeWithContext
	connectionTypeDefault
)

// DialOption represents an option that can be passed to Dial.
type DialOption func(*dialConfig)

func defaults(cfg *dialConfig) {
	cfg.serviceName = namingschema.ServiceNameOverrideV0(defaultServiceName, defaultServiceName)
	cfg.spanName = namingschema.OpName(namingschema.RedisOutbound)
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_REDIGO_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}

	// Default to withTimeout to maintain backwards compatibility.
	cfg.connectionType = connectionTypeWithTimeout
}

// WithServiceName sets the given service name for the dialled connection.
func WithServiceName(name string) DialOption {
	return func(cfg *dialConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) DialOption {
	return func(cfg *dialConfig) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) DialOption {
	return func(cfg *dialConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithTimeoutConnection wraps the connection with redis.ConnWithTimeout.
func WithTimeoutConnection() DialOption {
	return func(cfg *dialConfig) {
		cfg.connectionType = connectionTypeWithTimeout
	}
}

// WithContextConnection wraps the connection with redis.ConnWithContext.
func WithContextConnection() DialOption {
	return func(cfg *dialConfig) {
		cfg.connectionType = connectionTypeWithContext
	}
}

// WithDefaultConnection overrides the default connectionType to not be connectionTypeWithTimeout.
func WithDefaultConnection() DialOption {
	return func(cfg *dialConfig) {
		cfg.connectionType = connectionTypeDefault
	}
}
