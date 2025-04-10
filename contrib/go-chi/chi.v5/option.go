// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"net/http"

	v2 "github.com/DataDog/dd-trace-go/contrib/go-chi/chi.v5/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

// Option represents an option that can be passed to NewRouter.
type Option = v2.Option

// WithServiceName sets the given service name for the router.
func WithServiceName(name string) Option {
	return v2.WithService(name)
}

// WithSpanOptions applies the given set of options to the spans started
// by the router.
func WithSpanOptions(opts ...ddtrace.StartSpanOption) Option {
	return v2.WithSpanOptions(tracer.ApplyV1Options(opts...))
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return v2.WithAnalytics(on)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return v2.WithAnalyticsRate(rate)
}

// WithStatusCheck specifies a function fn which reports whether the passed
// statusCode should be considered an error.
func WithStatusCheck(fn func(statusCode int) bool) Option {
	return v2.WithStatusCheck(fn)
}

// WithIgnoreRequest specifies a function to use for determining if the
// incoming HTTP request tracing should be skipped.
func WithIgnoreRequest(fn func(r *http.Request) bool) Option {
	return v2.WithIgnoreRequest(fn)
}

// WithModifyResourceName specifies a function to use to modify the resource name.
func WithModifyResourceName(fn func(resourceName string) string) Option {
	return v2.WithModifyResourceName(fn)
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headers []string) Option {
	return v2.WithHeaderTags(headers)
}

// WithResourceNamer specifies a function to use for determining the resource
// name of the span.
func WithResourceNamer(fn func(r *http.Request) string) Option {
	return v2.WithResourceNamer(fn)
}

// WithNoAppsec opts this router out of AppSec management. This allows a particular router to bypass
// appsec, while the rest of the application is still being monitored/managed. This has not effect
// if AppSec is not enabled globally (e.g, via the DD_APPSEC_ENABLED environment variable).
func WithNoAppsec(disabled bool) Option {
	return v2.WithNoAppsec(disabled)
}

// WithResponseHeaderCopier provides a function to fetch the response headers from the
// http.ResponseWriter. This allows for custom implementations as needed if you over-ride the
// default http.ResponseWriter, such as to add synchronization. Provided functions may elect to
// return a copy of the http.Header map instead of a reference to the original (e.g: to not risk
// breaking synchronization). This is currently only used by AppSec.
func WithResponseHeaderCopier(f func(http.ResponseWriter) http.Header) Option {
	return v2.WithResponseHeaderCopier(f)
}
