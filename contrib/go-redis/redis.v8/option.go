// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package redis

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

const defaultServiceName = "redis.client"

type clientConfig struct {
	serviceName   string
	spanName      string
	analyticsRate float64
	skipRaw       bool
	errCheck      func(err error) bool
}

// ClientOption describes options for the Redis integration.
type ClientOption interface {
	apply(*clientConfig)
}

// ClientOptionFn represents options applicable to NewClient and WrapClient.
type ClientOptionFn func(*clientConfig)

func (fn ClientOptionFn) apply(cfg *clientConfig) {
	fn(cfg)
}

func defaults(cfg *clientConfig) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentDefault, nil)
	cfg.spanName = instr.OperationName(instrumentation.ComponentDefault, nil)
	cfg.analyticsRate = instr.AnalyticsRate(false)
	cfg.errCheck = func(error) bool { return true }
}

// WithSkipRawCommand reports whether to skip setting the "redis.raw_command" tag
// on instrumenation spans. This may be useful if the Datadog Agent is not
// set up to obfuscate this value and it could contain sensitive information.
func WithSkipRawCommand(skip bool) ClientOptionFn {
	return func(cfg *clientConfig) {
		cfg.skipRaw = skip
	}
}

// WithService sets the given service name for the client.
func WithService(name string) ClientOptionFn {
	return func(cfg *clientConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) ClientOptionFn {
	return func(cfg *clientConfig) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) ClientOptionFn {
	return func(cfg *clientConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error.
func WithErrorCheck(fn func(err error) bool) ClientOptionFn {
	return func(cfg *clientConfig) {
		cfg.errCheck = fn
	}
}
