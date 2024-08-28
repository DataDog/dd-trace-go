// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package leveldb

import (
	"context"
	"math"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type config struct {
	ctx           context.Context
	serviceName   string
	spanName      string
	analyticsRate float64
}

func newConfig(opts ...Option) *config {
	cfg := &config{
		serviceName:   instr.ServiceName(instrumentation.ComponentDefault, nil),
		spanName:      instr.OperationName(instrumentation.ComponentDefault, nil),
		ctx:           context.Background(),
		analyticsRate: instr.AnalyticsRate(false),
	}
	for _, opt := range opts {
		opt.apply(cfg)
	}
	return cfg
}

// Option describes options for the LevelDB integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to Open, OpenFile, WrapDB, WrapIterator, WrapSnapshot and WrapTransaction.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

// WithContext sets the tracing context for the db.
func WithContext(ctx context.Context) OptionFn {
	return func(cfg *config) {
		cfg.ctx = ctx
	}
}

// WithService sets the given service name for the db.
func WithService(serviceName string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = serviceName
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
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
func WithAnalyticsRate(rate float64) OptionFn {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

func withConfig(cfg *config) OptionFn {
	return func(c *config) {
		*c = *cfg
	}
}
