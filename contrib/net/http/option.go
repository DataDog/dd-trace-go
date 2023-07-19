// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"math"
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/normalizer"
)

const defaultServiceName = "http.router"

type config struct {
	serviceName   string
	analyticsRate float64
	spanOpts      []ddtrace.StartSpanOption
	finishOpts    []ddtrace.FinishOption
	ignoreRequest func(*http.Request) bool
	resourceNamer func(*http.Request) string
	headerTags    *internal.LockMap
}

// MuxOption has been deprecated in favor of Option.
type MuxOption = Option

// Option represents an option that can be passed to NewServeMux or WrapHandler.
type Option func(*config)

func defaults(cfg *config) {
	if internal.BoolEnv("DD_TRACE_HTTP_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = globalconfig.AnalyticsRate()
	}
	cfg.serviceName = namingschema.ServiceName(defaultServiceName)
	cfg.headerTags = globalconfig.HeaderTagMap()
	cfg.spanOpts = []ddtrace.StartSpanOption{tracer.Measured()}
	if !math.IsNaN(cfg.analyticsRate) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	cfg.ignoreRequest = func(_ *http.Request) bool { return false }
	cfg.resourceNamer = func(_ *http.Request) string { return "" }
}

// WithIgnoreRequest holds the function to use for determining if the
// incoming HTTP request should not be traced.
func WithIgnoreRequest(f func(*http.Request) bool) MuxOption {
	return func(cfg *config) {
		cfg.ignoreRequest = f
	}
}

// WithServiceName sets the given service name for the returned ServeMux.
func WithServiceName(name string) MuxOption {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headers []string) Option {
	headerTagsMap := normalizer.HeaderTagSlice(headers)
	return func(cfg *config) {
		cfg.headerTags = internal.NewLockMap(headerTagsMap)
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) MuxOption {
	return func(cfg *config) {
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
func WithAnalyticsRate(rate float64) MuxOption {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
			cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithSpanOptions defines a set of additional ddtrace.StartSpanOption to be added
// to spans started by the integration.
func WithSpanOptions(opts ...ddtrace.StartSpanOption) Option {
	return func(cfg *config) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}

// WithResourceNamer populates the name of a resource based on a custom function.
func WithResourceNamer(namer func(req *http.Request) string) Option {
	return func(cfg *config) {
		cfg.resourceNamer = namer
	}
}

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() Option {
	return func(cfg *config) {
		cfg.finishOpts = append(cfg.finishOpts, tracer.NoDebugStack())
	}
}

// A RoundTripperBeforeFunc can be used to modify a span before an http
// RoundTrip is made.
type RoundTripperBeforeFunc func(*http.Request, ddtrace.Span)

// A RoundTripperAfterFunc can be used to modify a span after an http
// RoundTrip is made. It is possible for the http Response to be nil.
type RoundTripperAfterFunc func(*http.Response, ddtrace.Span)

type roundTripperConfig struct {
	before        RoundTripperBeforeFunc
	after         RoundTripperAfterFunc
	analyticsRate float64
	serviceName   string
	resourceNamer func(req *http.Request) string
	spanNamer     func(req *http.Request) string
	ignoreRequest func(*http.Request) bool
	spanOpts      []ddtrace.StartSpanOption
	propagation   bool
	errCheck      func(err error) bool
}

func newRoundTripperConfig() *roundTripperConfig {
	defaultResourceNamer := func(_ *http.Request) string {
		return "http.request"
	}
	spanName := namingschema.OpName(namingschema.HTTPClient)
	defaultSpanNamer := func(_ *http.Request) string {
		return spanName
	}
	return &roundTripperConfig{
		serviceName:   namingschema.ServiceNameOverrideV0("", ""),
		analyticsRate: globalconfig.AnalyticsRate(),
		resourceNamer: defaultResourceNamer,
		propagation:   true,
		spanNamer:     defaultSpanNamer,
		ignoreRequest: func(_ *http.Request) bool { return false },
	}
}

// A RoundTripperOption represents an option that can be passed to
// WrapRoundTripper.
type RoundTripperOption func(*roundTripperConfig)

// WithBefore adds a RoundTripperBeforeFunc to the RoundTripper
// config.
func WithBefore(f RoundTripperBeforeFunc) RoundTripperOption {
	return func(cfg *roundTripperConfig) {
		cfg.before = f
	}
}

// WithAfter adds a RoundTripperAfterFunc to the RoundTripper
// config.
func WithAfter(f RoundTripperAfterFunc) RoundTripperOption {
	return func(cfg *roundTripperConfig) {
		cfg.after = f
	}
}

// RTWithResourceNamer specifies a function which will be used to
// obtain the resource name for a given request.
func RTWithResourceNamer(namer func(req *http.Request) string) RoundTripperOption {
	return func(cfg *roundTripperConfig) {
		cfg.resourceNamer = namer
	}
}

// RTWithSpanNamer specifies a function which will be used to
// obtain the span operation name for a given request.
func RTWithSpanNamer(namer func(req *http.Request) string) RoundTripperOption {
	return func(cfg *roundTripperConfig) {
		cfg.spanNamer = namer
	}
}

// RTWithSpanOptions defines a set of additional ddtrace.StartSpanOption to be added
// to spans started by the integration.
func RTWithSpanOptions(opts ...ddtrace.StartSpanOption) RoundTripperOption {
	return func(cfg *roundTripperConfig) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}

// RTWithServiceName sets the given service name for the RoundTripper.
func RTWithServiceName(name string) RoundTripperOption {
	return func(cfg *roundTripperConfig) {
		cfg.serviceName = name
	}
}

// RTWithAnalytics enables Trace Analytics for all started spans.
func RTWithAnalytics(on bool) RoundTripperOption {
	return func(cfg *roundTripperConfig) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// RTWithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func RTWithAnalyticsRate(rate float64) RoundTripperOption {
	return func(cfg *roundTripperConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// RTWithPropagation enables/disables propagation for tracing headers.
// Disabling propagation will disconnect this trace from any downstream traces.
func RTWithPropagation(propagation bool) RoundTripperOption {
	return func(cfg *roundTripperConfig) {
		cfg.propagation = propagation
	}
}

// RTWithIgnoreRequest holds the function to use for determining if the
// outgoing HTTP request should not be traced.
func RTWithIgnoreRequest(f func(*http.Request) bool) RoundTripperOption {
	return func(cfg *roundTripperConfig) {
		cfg.ignoreRequest = f
	}
}

// RTWithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever an http operation
// finishes with an error
func RTWithErrorCheck(fn func(err error) bool) RoundTripperOption {
	return func(cfg *roundTripperConfig) {
		cfg.errCheck = fn
	}
}
