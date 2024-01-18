// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package mux

import (
	"math"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/globalconfig"
	"github.com/DataDog/dd-trace-go/v2/internal/namingschema"
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"
)

const defaultServiceName = "mux.router"

type routerConfig struct {
	serviceName   string
	spanOpts      []tracer.StartSpanOption // additional span options to be applied
	finishOpts    []tracer.FinishOption    // span finish options to be applied
	analyticsRate float64
	resourceNamer func(*Router, *http.Request) string
	ignoreRequest func(*http.Request) bool
	queryParams   bool
	headerTags    *internal.LockMap
}

// RouterOption describes options for the Gorilla mux integration.
type RouterOption interface {
	apply(config *routerConfig)
}

// RouterOptionFn represents options applicable to NewRouter and WrapRouter.
type RouterOptionFn func(*routerConfig)

func (fn RouterOptionFn) apply(cfg *routerConfig) {
	fn(cfg)
}

func newConfig(opts []RouterOption) *routerConfig {
	cfg := new(routerConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	if !math.IsNaN(cfg.analyticsRate) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	return cfg
}

func defaults(cfg *routerConfig) {
	if internal.BoolEnv("DD_TRACE_MUX_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = globalconfig.AnalyticsRate()
	}
	cfg.headerTags = globalconfig.HeaderTagMap()
	cfg.serviceName = namingschema.ServiceName(defaultServiceName)
	cfg.resourceNamer = defaultResourceNamer
	cfg.ignoreRequest = func(_ *http.Request) bool { return false }
}

// WithIgnoreRequest holds the function to use for determining if the
// incoming HTTP request tracing should be skipped.
func WithIgnoreRequest(f func(*http.Request) bool) RouterOptionFn {
	return func(cfg *routerConfig) {
		cfg.ignoreRequest = f
	}
}

// WithService sets the given service name for the router.
func WithService(name string) RouterOptionFn {
	return func(cfg *routerConfig) {
		cfg.serviceName = name
	}
}

// WithSpanOptions applies the given set of options to the spans started
// by the router.
func WithSpanOptions(opts ...tracer.StartSpanOption) RouterOptionFn {
	return func(cfg *routerConfig) {
		cfg.spanOpts = opts
	}
}

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() RouterOptionFn {
	return func(cfg *routerConfig) {
		cfg.finishOpts = append(cfg.finishOpts, tracer.NoDebugStack())
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

// WithResourceNamer specifies a quantizing function which will be used to
// obtain the resource name for a given request.
func WithResourceNamer(namer func(router *Router, req *http.Request) string) RouterOptionFn {
	return func(cfg *routerConfig) {
		cfg.resourceNamer = namer
	}
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headers []string) RouterOptionFn {
	headerTagsMap := normalizer.HeaderTagSlice(headers)
	return func(cfg *routerConfig) {
		cfg.headerTags = internal.NewLockMap(headerTagsMap)
	}
}

// WithQueryParams specifies that the integration should attach request query parameters as APM tags.
// Warning: using this feature can risk exposing sensitive data such as authorization tokens
// to Datadog.
func WithQueryParams() RouterOptionFn {
	return func(cfg *routerConfig) {
		cfg.queryParams = true
	}
}
