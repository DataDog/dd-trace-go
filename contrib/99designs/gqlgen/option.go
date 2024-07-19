// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2022 Datadog, Inc.

package gqlgen

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type config struct {
	serviceName   string
	analyticsRate float64
	tags          map[string]interface{}
}

// An Option describes options for the gqlgen integration.
type Option interface {
	apply(*config)
}

// OptionFn represents an option that can be passed to gqlgen tracer.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func defaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentDefault, nil)
	cfg.analyticsRate = instr.AnalyticsRate()
	cfg.tags = make(map[string]interface{})
}

// WithAnalytics enables or disables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
	if on {
		return WithAnalyticsRate(1.0)
	}
	return WithAnalyticsRate(math.NaN())
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events correlated to started spans.
func WithAnalyticsRate(rate float64) OptionFn {
	return func(cfg *config) {
		cfg.analyticsRate = rate
	}
}

// WithService sets the given service name for the gqlgen server.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithCustomTag will attach the value to the span tagged by the key.
func WithCustomTag(key string, value interface{}) OptionFn {
	return func(cfg *config) {
		if cfg.tags == nil {
			cfg.tags = make(map[string]interface{})
		}
		cfg.tags[key] = value
	}
}
