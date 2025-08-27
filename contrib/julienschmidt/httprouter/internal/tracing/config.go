// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracing

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

type Config struct {
	headerTags    instrumentation.HeaderTags
	spanOpts      []tracer.StartSpanOption
	serviceName   string
	analyticsRate float64
}

func NewConfig(opts ...Option) *Config {
	cfg := new(Config)
	if options.GetBoolEnv("DD_TRACE_HTTPROUTER_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = instr.AnalyticsRate(true)
	}
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentServer, nil)
	cfg.headerTags = instr.HTTPHeadersAsTags()
	for _, fn := range opts {
		fn(cfg)
	}
	if !math.IsNaN(cfg.analyticsRate) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}

	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.SpanKind, ext.SpanKindServer))
	cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.Component, componentName))
	return cfg
}

type Option func(*Config)

// WithService sets the given service name for the returned router.
func WithService(name string) Option {
	return func(cfg *Config) {
		cfg.serviceName = name
	}
}

// WithSpanOptions applies the given set of options to the span started by the router.
func WithSpanOptions(opts ...tracer.StartSpanOption) Option {
	return func(cfg *Config) {
		cfg.spanOpts = opts
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(cfg *Config) {
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
	return func(cfg *Config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headers []string) Option {
	return func(cfg *Config) {
		cfg.headerTags = instrumentation.NewHeaderTags(headers)
	}
}
