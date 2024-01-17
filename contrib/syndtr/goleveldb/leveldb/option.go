// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package leveldb

import (
	"context"
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

const defaultServiceName = "leveldb"

type config struct {
	ctx           context.Context
	serviceName   string
	spanName      string
	analyticsRate float64
}

func newConfig(opts ...Option) *config {
	cfg := &config{
		serviceName: namingschema.ServiceNameOverrideV0(defaultServiceName, defaultServiceName),
		spanName:    namingschema.OpName(namingschema.LevelDBOutbound),
		ctx:         context.Background(),
		// cfg.analyticsRate: globalconfig.AnalyticsRate(),
		analyticsRate: math.NaN(),
	}
	if internal.BoolEnv("DD_TRACE_LEVELDB_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	}
	for _, opt := range opts {
		opt(cfg)
	}
	return cfg
}

// Option represents an option that can be used customize the db tracing config.
type Option func(*config)

// WithContext sets the tracing context for the db.
func WithContext(ctx context.Context) Option {
	return func(cfg *config) {
		cfg.ctx = ctx
	}
}

// WithServiceName sets the given service name for the db.
func WithServiceName(serviceName string) Option {
	return func(cfg *config) {
		cfg.serviceName = serviceName
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
