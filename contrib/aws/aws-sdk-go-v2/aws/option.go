// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package aws

import (
	"math"
)

type config struct {
	serviceName   string
	analyticsRate float64
	errCheck      func(err error) bool
}

// Option describes options for the AWS integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to AppendMiddleware.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func defaults(cfg *config) {
	cfg.analyticsRate = instr.AnalyticsRate(false)
}

// WithService sets the given service name for the dialled connection.
// When the service name is not explicitly set it will be inferred based on the
// request to AWS.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
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

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever an aws operation
// finishes with an error.
func WithErrorCheck(fn func(err error) bool) OptionFn {
	return func(cfg *config) {
		cfg.errCheck = fn
	}
}
