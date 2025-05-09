// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.
package http

import (
	"net/http"

	v2 "github.com/DataDog/dd-trace-go/contrib/net/http/v2"
	v2tracer "github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// MuxOption has been deprecated in favor of Option.
type MuxOption = Option

// Option represents an option that can be passed to NewServeMux or WrapHandler.
type Option = v2.Option

// WithIgnoreRequest holds the function to use for determining if the
// incoming HTTP request should not be traced.
func WithIgnoreRequest(f func(*http.Request) bool) MuxOption {
	return v2.WithIgnoreRequest(f)
}

// WithServiceName sets the given service name for the returned ServeMux.
func WithServiceName(name string) MuxOption {
	return v2.WithService(name)
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headers []string) Option {
	return v2.WithHeaderTags(headers)
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) MuxOption {
	return v2.WithAnalytics(on)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) MuxOption {
	return v2.WithAnalyticsRate(rate)
}

// WithSpanOptions defines a set of additional ddtrace.StartSpanOption to be added
// to spans started by the integration.
func WithSpanOptions(opts ...ddtrace.StartSpanOption) Option {
	return v2.WithSpanOptions(tracer.ApplyV1Options(opts...))
}

// WithResourceNamer populates the name of a resource based on a custom function.
func WithResourceNamer(namer func(req *http.Request) string) Option {
	return v2.WithResourceNamer(namer)
}

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() Option {
	return v2.NoDebugStack()
}

// A RoundTripperBeforeFunc can be used to modify a span before an http
// RoundTrip is made.
type RoundTripperBeforeFunc func(*http.Request, ddtrace.Span)

// A RoundTripperAfterFunc can be used to modify a span after an http
// RoundTrip is made. It is possible for the http Response to be nil.
type RoundTripperAfterFunc func(*http.Response, ddtrace.Span)

// A RoundTripperOption represents an option that can be passed to
// WrapRoundTripper.
type RoundTripperOption = v2.RoundTripperOption

// WithBefore adds a RoundTripperBeforeFunc to the RoundTripper
// config.
func WithBefore(f RoundTripperBeforeFunc) RoundTripperOption {
	wrap := func(req *http.Request, span *v2tracer.Span) {
		f(req, tracer.WrapSpanV2(span))
	}
	return v2.WithBefore(wrap)
}

// WithAfter adds a RoundTripperAfterFunc to the RoundTripper
// config.
func WithAfter(f RoundTripperAfterFunc) RoundTripperOption {
	wrap := func(resp *http.Response, span *v2tracer.Span) {
		f(resp, tracer.WrapSpanV2(span))
	}
	return v2.WithAfter(wrap)
}

// RTWithResourceNamer specifies a function which will be used to
// obtain the resource name for a given request.
func RTWithResourceNamer(namer func(req *http.Request) string) RoundTripperOption {
	return v2.WithResourceNamer(namer)
}

// RTWithSpanNamer specifies a function which will be used to
// obtain the span operation name for a given request.
func RTWithSpanNamer(namer func(req *http.Request) string) RoundTripperOption {
	return v2.WithSpanNamer(namer)
}

// RTWithSpanOptions defines a set of additional ddtrace.StartSpanOption to be added
// to spans started by the integration.
func RTWithSpanOptions(opts ...ddtrace.StartSpanOption) RoundTripperOption {
	return v2.WithSpanOptions(tracer.ApplyV1Options(opts...))
}

// RTWithServiceName sets the given service name for the RoundTripper.
func RTWithServiceName(name string) RoundTripperOption {
	return v2.WithService(name)
}

// RTWithAnalytics enables Trace Analytics for all started spans.
func RTWithAnalytics(on bool) RoundTripperOption {
	return v2.WithAnalytics(on)
}

// RTWithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func RTWithAnalyticsRate(rate float64) RoundTripperOption {
	return v2.WithAnalyticsRate(rate)
}

// RTWithPropagation enables/disables propagation for tracing headers.
// Disabling propagation will disconnect this trace from any downstream traces.
func RTWithPropagation(propagation bool) RoundTripperOption {
	return v2.WithPropagation(propagation)
}

// RTWithIgnoreRequest holds the function to use for determining if the
// outgoing HTTP request should not be traced.
func RTWithIgnoreRequest(f func(*http.Request) bool) RoundTripperOption {
	return v2.WithIgnoreRequest(f)
}

// WithStatusCheck sets a span to be an error if the passed function
// returns true for a given status code.
func WithStatusCheck(fn func(statusCode int) bool) Option {
	return v2.WithStatusCheck(fn)
}

// RTWithErrorCheck specifies a function fn which determines whether the passed
// error should be marked as an error. The fn is called whenever an http operation
// finishes with an error
func RTWithErrorCheck(fn func(err error) bool) RoundTripperOption {
	return v2.WithErrorCheck(fn)
}

// RTWithStatusCheck sets a span to be an error if the passed function
// returns true for a given status code.
func RTWithStatusCheck(fn func(statusCode int) bool) RoundTripperOption {
	return v2.WithStatusCheck(fn)
}
