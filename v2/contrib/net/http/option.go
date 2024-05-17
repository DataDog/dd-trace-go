// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

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

const defaultServiceName = "http.router"

type commonConfig struct {
	analyticsRate float64
	ignoreRequest func(*http.Request) bool
	serviceName   string
	resourceNamer func(*http.Request) string
	spanOpts      []tracer.StartSpanOption
}

type config struct {
	commonConfig
	finishOpts []tracer.FinishOption
	headerTags *internal.LockMap
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
	if internal.BoolEnv("DD_TRACE_HTTP_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = globalconfig.AnalyticsRate()
	}
	cfg.serviceName = namingschema.ServiceName(defaultServiceName)
	cfg.headerTags = globalconfig.HeaderTagMap()
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
	headerTagsMap := normalizer.HeaderTagSlice(headers)
	return func(cfg *config) {
		cfg.headerTags = internal.NewLockMap(headerTagsMap)
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
	before      RoundTripperBeforeFunc
	after       RoundTripperAfterFunc
	spanNamer   func(req *http.Request) string
	propagation bool
	errCheck    func(err error) bool
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
	spanName := namingschema.OpName(namingschema.HTTPClient)
	defaultSpanNamer := func(_ *http.Request) string {
		return spanName
	}
	sharedCfg := commonConfig{
		serviceName:   namingschema.ServiceNameOverrideV0("", ""),
		analyticsRate: globalconfig.AnalyticsRate(),
		resourceNamer: defaultResourceNamer,
		ignoreRequest: func(_ *http.Request) bool { return false },
	}
	return &roundTripperConfig{
		commonConfig: sharedCfg,
		propagation:  true,
		spanNamer:    defaultSpanNamer,
	}
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
