// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"math"
	"net/http"
	"os"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

type commonConfig struct {
	analyticsRate float64
	ignoreRequest func(*http.Request) bool
	serviceName   string
	resourceNamer func(*http.Request) string
	spanOpts      []tracer.StartSpanOption
}

const (
	defaultServiceName = "http.router"
	// envClientQueryStringEnabled is the name of the env var used to specify whether query string collection is enabled for http client spans.
	envClientQueryStringEnabled = "DD_TRACE_HTTP_CLIENT_TAG_QUERY_STRING"
	// envClientErrorStatuses is the name of the env var that specifies error status codes on http client spans
	envClientErrorStatuses = "DD_TRACE_HTTP_CLIENT_ERROR_STATUSES"
)

type config struct {
	commonConfig
	finishOpts    []tracer.FinishOption
	headerTags    instrumentation.HeaderTags
	isStatusError func(int) bool
}

// Option describes options for http.ServeMux.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to NewServeMux and WrapHandler.
type OptionFn func(*commonConfig)

func (o OptionFn) apply(cfg *config) {
	o(&cfg.commonConfig)
}

func (o OptionFn) applyRoundTripper(cfg *roundTripperConfig) {
	o(&cfg.commonConfig)
}

// HandlerOptionFn represents options applicable to NewServeMux and WrapHandler.
type HandlerOptionFn func(*config)

func (o HandlerOptionFn) apply(cfg *config) {
	o(cfg)
}

func defaults(cfg *config) {
	if options.GetBoolEnv("DD_TRACE_HTTP_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = instr.AnalyticsRate(true)
	}
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentServer, nil)
	cfg.headerTags = instr.HTTPHeadersAsTags()
	cfg.spanOpts = []tracer.StartSpanOption{tracer.Measured()}
	if !math.IsNaN(cfg.analyticsRate) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
	cfg.ignoreRequest = func(_ *http.Request) bool { return false }
	cfg.resourceNamer = func(_ *http.Request) string { return "" }
}

// WithIgnoreRequest holds the function to use for determining if the
// incoming HTTP request should not be traced.
func WithIgnoreRequest(f func(*http.Request) bool) OptionFn {
	return func(cfg *commonConfig) {
		cfg.ignoreRequest = f
	}
}

// WithService sets the given service name for the returned ServeMux.
func WithService(name string) OptionFn {
	return func(cfg *commonConfig) {
		cfg.serviceName = name
	}
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headers []string) HandlerOptionFn {
	return func(cfg *config) {
		cfg.headerTags = instrumentation.NewHeaderTags(headers)
	}
}

// WithStatusCheck sets a span to be an error if the passed function
// returns true for a given status code.
func WithStatusCheck(fn func(statusCode int) bool) HandlerOptionFn {
	return func(cfg *config) {
		cfg.isStatusError = fn
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
	return func(cfg *commonConfig) {
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
func WithAnalyticsRate(rate float64) OptionFn {
	return func(cfg *commonConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
			cfg.spanOpts = append(cfg.spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithSpanOptions defines a set of additional tracer.StartSpanOption to be added
// to spans started by the integration.
func WithSpanOptions(opts ...tracer.StartSpanOption) OptionFn {
	return func(cfg *commonConfig) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}

// WithResourceNamer populates the name of a resource based on a custom function.
func WithResourceNamer(namer func(req *http.Request) string) OptionFn {
	return func(cfg *commonConfig) {
		cfg.resourceNamer = namer
	}
}

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() HandlerOptionFn {
	return func(cfg *config) {
		cfg.finishOpts = append(cfg.finishOpts, tracer.NoDebugStack())
	}
}

// A RoundTripperBeforeFunc can be used to modify a span before an http
// RoundTrip is made.
type RoundTripperBeforeFunc func(*http.Request, *tracer.Span)

// A RoundTripperAfterFunc can be used to modify a span after an http
// RoundTrip is made. It is possible for the http Response to be nil.
type RoundTripperAfterFunc func(*http.Response, *tracer.Span)

type roundTripperConfig struct {
	commonConfig
	before        RoundTripperBeforeFunc
	after         RoundTripperAfterFunc
	spanNamer     func(req *http.Request) string
	propagation   bool
	errCheck      func(err error) bool
	queryString   bool // reports whether the query string is included in the URL tag for http client spans
	isStatusError func(statusCode int) bool
}

// RoundTripperOption describes options for http.RoundTripper.
type RoundTripperOption interface {
	applyRoundTripper(*roundTripperConfig)
}

// RoundTripperOptionFn represents options applicable to WrapClient and WrapRoundTripper.
type RoundTripperOptionFn func(*roundTripperConfig)

func (o RoundTripperOptionFn) applyRoundTripper(cfg *roundTripperConfig) {
	o(cfg)
}

func newRoundTripperConfig() *roundTripperConfig {
	defaultResourceNamer := func(_ *http.Request) string {
		return "http.request"
	}
	spanName := instr.OperationName(instrumentation.ComponentClient, nil)
	defaultSpanNamer := func(_ *http.Request) string {
		return spanName
	}
	sharedCfg := commonConfig{
		serviceName:   instr.ServiceName(instrumentation.ComponentClient, nil),
		analyticsRate: instr.GlobalAnalyticsRate(),
		resourceNamer: defaultResourceNamer,
		ignoreRequest: func(_ *http.Request) bool { return false },
	}
	rtConfig := roundTripperConfig{
		commonConfig:  sharedCfg,
		propagation:   true,
		spanNamer:     defaultSpanNamer,
		queryString:   options.GetBoolEnv(envClientQueryStringEnabled, false),
		isStatusError: isClientError,
	}
	v := os.Getenv(envClientErrorStatuses)
	if fn := httptrace.GetErrorCodesFromInput(v); fn != nil {
		rtConfig.isStatusError = fn
	}
	return &rtConfig
}

// WithBefore adds a RoundTripperBeforeFunc to the RoundTripper
// config.
func WithBefore(f RoundTripperBeforeFunc) RoundTripperOptionFn {
	return func(cfg *roundTripperConfig) {
		cfg.before = f
	}
}

// WithAfter adds a RoundTripperAfterFunc to the RoundTripper
// config.
func WithAfter(f RoundTripperAfterFunc) RoundTripperOptionFn {
	return func(cfg *roundTripperConfig) {
		cfg.after = f
	}
}

// RTWithResourceNamer specifies a function which will be used to
// obtain the resource name for a given request.
func RTWithResourceNamer(namer func(req *http.Request) string) RoundTripperOptionFn {
	return func(cfg *roundTripperConfig) {
		cfg.resourceNamer = namer
	}
}

// WithSpanNamer specifies a function which will be used to
// obtain the span operation name for a given request.
func WithSpanNamer(namer func(req *http.Request) string) RoundTripperOptionFn {
	return func(cfg *roundTripperConfig) {
		cfg.spanNamer = namer
	}
}

// WithPropagation enables/disables propagation for tracing headers.
// Disabling propagation will disconnect this trace from any downstream traces.
func WithPropagation(propagation bool) RoundTripperOptionFn {
	return func(cfg *roundTripperConfig) {
		cfg.propagation = propagation
	}
}

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever an http operation
// finishes with an error
func WithErrorCheck(fn func(err error) bool) RoundTripperOptionFn {
	return func(cfg *roundTripperConfig) {
		cfg.errCheck = fn
	}
}

func isClientError(statusCode int) bool {
	return statusCode >= 400 && statusCode < 500
}
