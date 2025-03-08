// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mgo

import (
	"context"
	"math"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type mongoConfig struct {
	ctx           context.Context
	serviceName   string
	spanName      string
	analyticsRate float64
}

func newConfig() *mongoConfig {
	return &mongoConfig{
		serviceName:   instr.ServiceName(instrumentation.ComponentDefault, nil),
		spanName:      instr.OperationName(instrumentation.ComponentDefault, nil),
		ctx:           context.Background(),
		analyticsRate: instr.AnalyticsRate(false),
	}
}

type DialOption interface {
	apply(*mongoConfig)
}

// DialOptionFn represents an option that can be passed to Dial
type DialOptionFn func(*mongoConfig)

func (fn DialOptionFn) apply(cfg *mongoConfig) {
	fn(cfg)
}

// WithService sets the service name for a given MongoDB context.
func WithService(name string) DialOptionFn {
	return func(cfg *mongoConfig) {
		cfg.serviceName = name
	}
}

// WithContext sets the context.
func WithContext(ctx context.Context) DialOptionFn {
	return func(cfg *mongoConfig) {
		cfg.ctx = ctx
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) DialOptionFn {
	return func(cfg *mongoConfig) {
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
	return func(cfg *mongoConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}
