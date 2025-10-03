// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package http

import (
	"math"
	"net/http"

	internal "github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

// Option describes options for http.ServeMux.
type Option = internal.Option

// OptionFn represents options applicable to NewServeMux and WrapHandler.
type OptionFn = internal.OptionFn

// HandlerOptionFn represents options applicable to NewServeMux and WrapHandler.
type HandlerOptionFn = internal.HandlerOptionFn

// WithIgnoreRequest holds the function to use for determining if the
// incoming HTTP request should not be traced.
func WithIgnoreRequest(f func(*http.Request) bool) OptionFn {
	return func(cfg *internal.CommonConfig) {
		cfg.IgnoreRequest = f
	}
}

// WithService sets the given service name for the returned ServeMux.
func WithService(name string) OptionFn {
	return func(cfg *internal.CommonConfig) {
		cfg.ServiceName = name
	}
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headers []string) HandlerOptionFn {
	return func(cfg *internal.Config) {
		cfg.HeaderTags = instrumentation.NewHeaderTags(headers)
	}
}

// WithStatusCheck sets a span to be an error if the passed function
// returns true for a given status code.
func WithStatusCheck(fn func(statusCode int) bool) OptionFn {
	return func(cfg *internal.CommonConfig) {
		cfg.IsStatusError = fn
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
	return func(cfg *internal.CommonConfig) {
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
func WithAnalyticsRate(rate float64) OptionFn {
	return func(cfg *internal.CommonConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.AnalyticsRate = rate
			cfg.SpanOpts = append(cfg.SpanOpts, tracer.Tag(ext.EventSampleRate, cfg.AnalyticsRate))
		} else {
			cfg.AnalyticsRate = math.NaN()
		}
	}
}

// WithSpanOptions defines a set of additional tracer.StartSpanOption to be added
// to spans started by the integration.
func WithSpanOptions(opts ...tracer.StartSpanOption) OptionFn {
	return func(cfg *internal.CommonConfig) {
		cfg.SpanOpts = append(cfg.SpanOpts, opts...)
	}
}

// WithResourceNamer populates the name of a resource based on a custom function.
func WithResourceNamer(namer func(req *http.Request) string) OptionFn {
	return internal.WithResourceNamer(namer)
}

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() HandlerOptionFn {
	return func(cfg *internal.Config) {
		cfg.FinishOpts = append(cfg.FinishOpts, tracer.NoDebugStack())
	}
}

// RoundTripperOption describes options for http.RoundTripper.
type RoundTripperOption = internal.RoundTripperOption

// RoundTripperOptionFn represents options applicable to WrapClient and WrapRoundTripper.
type RoundTripperOptionFn = internal.RoundTripperOptionFn

func newRoundTripperConfig() *internal.RoundTripperConfig {
	defaultResourceNamer := func(_ *http.Request) string {
		return "http.request"
	}
	instr := internal.Instrumentation
	spanName := instr.OperationName(instrumentation.ComponentClient, nil)
	defaultSpanNamer := func(_ *http.Request) string {
		return spanName
	}
	sharedCfg := internal.CommonConfig{
		ServiceName:   instr.ServiceName(instrumentation.ComponentClient, nil),
		AnalyticsRate: instr.GlobalAnalyticsRate(),
		ResourceNamer: defaultResourceNamer,
		IgnoreRequest: func(_ *http.Request) bool { return false },
		IsStatusError: isClientError,
	}

	v := env.Get(internal.EnvClientErrorStatuses)
	if fn := httptrace.GetErrorCodesFromInput(v); fn != nil {
		sharedCfg.IsStatusError = fn
	}

	rtConfig := internal.RoundTripperConfig{
		CommonConfig: sharedCfg,
		Propagation:  true,
		SpanNamer:    defaultSpanNamer,
		QueryString:  options.GetBoolEnv(internal.EnvClientQueryStringEnabled, true),
	}

	return &rtConfig
}

// A RoundTripperBeforeFunc can be used to modify a span before an http
// RoundTrip is made.
type RoundTripperBeforeFunc = internal.RoundTripperBeforeFunc

// A RoundTripperAfterFunc can be used to modify a span after an http
// RoundTrip is made. It is possible for the http Response to be nil.
type RoundTripperAfterFunc = internal.RoundTripperAfterFunc

// WithBefore adds a RoundTripperBeforeFunc to the RoundTripper
// config.
func WithBefore(f RoundTripperBeforeFunc) RoundTripperOptionFn {
	return func(cfg *internal.RoundTripperConfig) {
		cfg.Before = f
	}
}

// WithAfter adds a RoundTripperAfterFunc to the RoundTripper
// config.
func WithAfter(f RoundTripperAfterFunc) RoundTripperOptionFn {
	return func(cfg *internal.RoundTripperConfig) {
		cfg.After = f
	}
}

// WithSpanNamer specifies a function which will be used to
// obtain the span operation name for a given request.
func WithSpanNamer(namer func(req *http.Request) string) RoundTripperOptionFn {
	return func(cfg *internal.RoundTripperConfig) {
		cfg.SpanNamer = namer
	}
}

// WithPropagation enables/disables propagation for tracing headers.
// Disabling propagation will disconnect this trace from any downstream traces.
func WithPropagation(propagation bool) RoundTripperOptionFn {
	return func(cfg *internal.RoundTripperConfig) {
		cfg.Propagation = propagation
	}
}

// WithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever an http operation
// finishes with an error
func WithErrorCheck(fn func(err error) bool) RoundTripperOptionFn {
	return func(cfg *internal.RoundTripperConfig) {
		cfg.ErrCheck = fn
	}
}

// WithClientTimings enables detailed HTTP request tracing using httptrace.ClientTrace.
// When enabled, the integration will add timing information for DNS lookups,
// connection establishment, TLS handshakes, and other HTTP request events as span tags.
// This feature is disabled by default and adds minimal overhead when enabled.
func WithClientTimings(enabled bool) RoundTripperOptionFn {
	return func(cfg *internal.RoundTripperConfig) {
		cfg.ClientTimings = enabled
	}
}

func isClientError(statusCode int) bool {
	return statusCode >= 400 && statusCode < 500
}
