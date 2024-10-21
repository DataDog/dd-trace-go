// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httprouter

import (
<<<<<<< HEAD
	"math"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
=======
	"gopkg.in/DataDog/dd-trace-go.v1/contrib/julienschmidt/httprouter/internal/tracing"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
>>>>>>> origin
)

const defaultServiceName = "http.router"

type routerConfig struct {
	serviceName   string
	spanOpts      []tracer.StartSpanOption
	analyticsRate float64
	headerTags    instrumentation.HeaderTags
}

<<<<<<< HEAD
// RouterOption describes options for the HTTPRouter integration.
type RouterOption interface {
	apply(*routerConfig)
}

// RouterOptionFn represents options applicable to New.
type RouterOptionFn func(*routerConfig)

func (fn RouterOptionFn) apply(cfg *routerConfig) {
	fn(cfg)
}

func defaults(cfg *routerConfig) {
	cfg.analyticsRate = instr.AnalyticsRate(true)
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentServer, nil)
	cfg.headerTags = instr.HTTPHeadersAsTags()
}

// WithService sets the given service name for the returned router.
func WithService(name string) RouterOptionFn {
	return func(cfg *routerConfig) {
		cfg.serviceName = name
	}
}

// WithSpanOptions applies the given set of options to the span started by the router.
func WithSpanOptions(opts ...tracer.StartSpanOption) RouterOptionFn {
	return func(cfg *routerConfig) {
		cfg.spanOpts = opts
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) RouterOptionFn {
	return func(cfg *routerConfig) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) RouterOptionFn {
	return func(cfg *routerConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}
=======
// RouterOption represents an option that can be passed to New.
type RouterOption = tracing.Option

// WithServiceName sets the given service name for the returned router.
var WithServiceName = tracing.WithServiceName

// WithSpanOptions applies the given set of options to the span started by the router.
var WithSpanOptions = tracing.WithSpanOptions

// WithAnalytics enables Trace Analytics for all started spans.
var WithAnalytics = tracing.WithAnalytics

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
var WithAnalyticsRate = tracing.WithAnalyticsRate
>>>>>>> origin

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
<<<<<<< HEAD
func WithHeaderTags(headers []string) RouterOptionFn {
	return func(cfg *routerConfig) {
		cfg.headerTags = instrumentation.NewHeaderTags(headers)
	}
}
=======
var WithHeaderTags = tracing.WithHeaderTags
>>>>>>> origin
