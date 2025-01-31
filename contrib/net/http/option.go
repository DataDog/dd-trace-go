// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package http

import (
	"math"
	"net/http"
	"os"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/internal/httptrace"
	internalconfig "gopkg.in/DataDog/dd-trace-go.v1/contrib/net/http/internal/config"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/normalizer"
)

const (
	// envClientQueryStringEnabled is the name of the env var used to specify whether query string collection is enabled for http client spans.
	envClientQueryStringEnabled = "DD_TRACE_HTTP_CLIENT_TAG_QUERY_STRING"
	// envClientErrorStatuses is the name of the env var that specifies error status codes on http client spans
	envClientErrorStatuses = "DD_TRACE_HTTP_CLIENT_ERROR_STATUSES"
)

// Option represents an option that can be passed to NewServeMux or WrapHandler.
type Option = internalconfig.Option

// MuxOption has been deprecated in favor of Option.
type MuxOption = Option

// WithIgnoreRequest holds the function to use for determining if the
// incoming HTTP request should not be traced.
func WithIgnoreRequest(f func(*http.Request) bool) MuxOption {
	return func(cfg *internalconfig.Config) {
		cfg.IgnoreRequest = f
	}
}

// WithServiceName sets the given service name for the returned ServeMux.
func WithServiceName(name string) MuxOption {
	return func(cfg *internalconfig.Config) {
		cfg.ServiceName = name
	}
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headers []string) Option {
	headerTagsMap := normalizer.HeaderTagSlice(headers)
	return func(cfg *internalconfig.Config) {
		cfg.HeaderTags = internal.NewLockMap(headerTagsMap)
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) MuxOption {
	return func(cfg *internalconfig.Config) {
		if on {
			cfg.AnalyticsRate = 1.0
			cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.EventSampleRate, cfg.AnalyticsRate))
		} else {
			cfg.AnalyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *internalconfig.Config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.AnalyticsRate = rate
			cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.EventSampleRate, cfg.AnalyticsRate))
		} else {
			cfg.AnalyticsRate = math.NaN()
		}
	}
}

// WithSpanOptions defines a set of additional ddtrace.StartSpanOption to be added
// to spans started by the integration.
func WithSpanOptions(opts ...ddtrace.StartSpanOption) Option {
	return func(cfg *internalconfig.Config) {
		cfg.SpanOpts = append(cfg.SpanOpts, opts...)
	}
}

// WithResourceNamer populates the name of a resource based on a custom function.
func WithResourceNamer(namer func(req *http.Request) string) Option {
	return internalconfig.WithResourceNamer(namer)
}

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() Option {
	return func(cfg *internalconfig.Config) {
		cfg.FinishOpts = append(cfg.FinishOpts, tracer.NoDebugStack())
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
	queryString   bool // reports whether the query string is included in the URL tag for http client spans
	isStatusError func(statusCode int) bool
}

func newRoundTripperConfig() *roundTripperConfig {
	defaultResourceNamer := func(_ *http.Request) string {
		return "http.request"
	}
	spanName := namingschema.OpName(namingschema.HTTPClient)
	defaultSpanNamer := func(_ *http.Request) string {
		return spanName
	}

	c := &roundTripperConfig{
		serviceName:   namingschema.ServiceNameOverrideV0("", ""),
		analyticsRate: globalconfig.AnalyticsRate(),
		resourceNamer: defaultResourceNamer,
		propagation:   true,
		spanNamer:     defaultSpanNamer,
		ignoreRequest: func(_ *http.Request) bool { return false },
		queryString:   internal.BoolEnv(envClientQueryStringEnabled, true),
		isStatusError: isClientError,
	}
	v := os.Getenv(envClientErrorStatuses)
	if fn := httptrace.GetErrorCodesFromInput(v); fn != nil {
		c.isStatusError = fn
	}
	return c
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

// WithStatusCheck sets a span to be an error if the passed function
// returns true for a given status code.
func WithStatusCheck(fn func(statusCode int) bool) Option {
	return func(cfg *internalconfig.Config) {
		cfg.IsStatusError = fn
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

func isClientError(statusCode int) bool {
	return statusCode >= 400 && statusCode < 500
}
