// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redigo // import "github.com/DataDog/dd-trace-go/v2/contrib/gomodule/redigo"

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
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

// DialOption describes options for the Redis integration.
type DialOption interface {
	apply(*dialConfig)
}

// DialOptionFn represents options applicable to Dial, DialContext and DialURL.
type DialOptionFn func(*dialConfig)

func (fn DialOptionFn) apply(cfg *dialConfig) {
	fn(cfg)
}

func defaults(cfg *dialConfig) {
	cfg.serviceName = namingschema.NewDefaultServiceName(
		defaultServiceName,
		namingschema.WithOverrideV0(defaultServiceName),
	).GetName()
	cfg.spanName = namingschema.NewRedisOutboundOp().GetName()
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_REDIGO_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}

	// Default to withTimeout to maintain backwards compatibility.
	cfg.connectionType = connectionTypeWithTimeout
}

// WithService sets the given service name for the dialled connection.
func WithService(name string) DialOptionFn {
	return func(cfg *dialConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) DialOptionFn {
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
func WithAnalyticsRate(rate float64) DialOptionFn {
	return func(cfg *dialConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithTimeoutConnection wraps the connection with redis.ConnWithTimeout.
func WithTimeoutConnection() DialOptionFn {
	return func(cfg *dialConfig) {
		cfg.connectionType = connectionTypeWithTimeout
	}
}

// WithContextConnection wraps the connection with redis.ConnWithContext.
func WithContextConnection() DialOptionFn {
	return func(cfg *dialConfig) {
		cfg.connectionType = connectionTypeWithContext
	}
}

// WithDefaultConnection overrides the default connectionType to not be connectionTypeWithTimeout.
func WithDefaultConnection() DialOptionFn {
	return func(cfg *dialConfig) {
		cfg.connectionType = connectionTypeDefault
	}
}
