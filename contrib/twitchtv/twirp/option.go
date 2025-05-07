// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package twirp

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type config struct {
	serviceName   string
	spanName      string
	analyticsRate float64
}

// Option describes options for the Twirp integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to NewServerHooks, WrapClient and WrapServer.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func defaults(cfg *config) {
	cfg.analyticsRate = instr.AnalyticsRate(true)
}

func clientDefaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentClient, nil)
	cfg.spanName = instr.OperationName(instrumentation.ComponentClient, nil)
	defaults(cfg)
}

func serverDefaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentServer, nil)
	// spanName is calculated dynamically since V0 span names are based on the twirp service name.
	defaults(cfg)
}

// WithService sets the given service name for the dialled connection.
// When the service name is not explicitly set, it will be inferred based on the
// request to the twirp service.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
	if on {
		return WithAnalyticsRate(1.0)
	}
	return WithAnalyticsRate(math.NaN())
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
