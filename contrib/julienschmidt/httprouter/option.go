// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httprouter

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
)

type routerConfig struct {
	serviceName   string
	spanOpts      []ddtrace.StartSpanOption
	analyticsRate float64
	finishOpts    []ddtrace.FinishOption
}

// RouterOption represents an option that can be passed to New.
type RouterOption func(*routerConfig)

func defaults(cfg *routerConfig) {
	if internal.BoolEnv("DD_TRACE_HTTPROUTER_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = globalconfig.AnalyticsRate()
	}
	cfg.serviceName = "http.router"
	if svc := globalconfig.ServiceName(); svc != "" {
		cfg.serviceName = svc
	}
	cfg.spanOpts = []ddtrace.StartSpanOption{tracer.Measured()}
	if !math.IsNaN(cfg.analyticsRate) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
}

// WithServiceName sets the given service name for the returned router.
func WithServiceName(name string) RouterOption {
	return func(cfg *routerConfig) {
		cfg.serviceName = name
	}
}

// WithSpanOptions applies the given set of options to the span started by the router.
func WithSpanOptions(opts ...ddtrace.StartSpanOption) RouterOption {
	return func(cfg *routerConfig) {
		cfg.spanOpts = opts
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) RouterOption {
	return func(cfg *routerConfig) {
		if on {
			cfg.analyticsRate = 1.0
			cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) RouterOption {
	return func(cfg *routerConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
			cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() RouterOption {
	return func(cfg *routerConfig) {
		cfg.finishOpts = append(cfg.finishOpts, tracer.NoDebugStack())
	}
}
