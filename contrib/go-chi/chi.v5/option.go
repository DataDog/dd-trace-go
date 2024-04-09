// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"math"
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/emitter/httpsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/normalizer"
)

const defaultServiceName = "chi.router"

type config struct {
	serviceName        string
	spanOpts           []ddtrace.StartSpanOption // additional span options to be applied
	analyticsRate      float64
	isStatusError      func(statusCode int) bool
	ignoreRequest      func(r *http.Request) bool
	modifyResourceName func(resourceName string) string
	headerTags         *internal.LockMap
	resourceNamer      func(r *http.Request) string
	appsecDisabled     bool
	appsecConfig       httpsec.Config
}

// Option represents an option that can be passed to NewRouter.
type Option func(*config)

func defaults(cfg *config) {
	cfg.serviceName = namingschema.ServiceName(defaultServiceName)
	if internal.BoolEnv("DD_TRACE_CHI_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = globalconfig.AnalyticsRate()
	}
	cfg.headerTags = globalconfig.HeaderTagMap()
	cfg.isStatusError = isServerError
	cfg.ignoreRequest = func(_ *http.Request) bool { return false }
	cfg.modifyResourceName = func(s string) string { return s }
	// for backward compatibility with modifyResourceName, initialize resourceName as nil.
	cfg.resourceNamer = nil
	cfg.appsecDisabled = false
}

// WithServiceName sets the given service name for the router.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithSpanOptions applies the given set of options to the spans started
// by the router.
func WithSpanOptions(opts ...ddtrace.StartSpanOption) Option {
	return func(cfg *config) {
		cfg.spanOpts = opts
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(cfg *config) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithStatusCheck specifies a function fn which reports whether the passed
// statusCode should be considered an error.
func WithStatusCheck(fn func(statusCode int) bool) Option {
	return func(cfg *config) {
		cfg.isStatusError = fn
	}
}

func isServerError(statusCode int) bool {
	return statusCode >= 500 && statusCode < 600
}

// WithIgnoreRequest specifies a function to use for determining if the
// incoming HTTP request tracing should be skipped.
func WithIgnoreRequest(fn func(r *http.Request) bool) Option {
	return func(cfg *config) {
		cfg.ignoreRequest = fn
	}
}

// WithModifyResourceName specifies a function to use to modify the resource name.
func WithModifyResourceName(fn func(resourceName string) string) Option {
	return func(cfg *config) {
		cfg.modifyResourceName = fn
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

// WithResourceNamer specifies a function to use for determining the resource
// name of the span.
func WithResourceNamer(fn func(r *http.Request) string) Option {
	return func(cfg *config) {
		cfg.resourceNamer = fn
	}
}

// WithNoAppsec opts this router out of AppSec management. This allows a particular router to bypass
// appsec, while the rest of the application is still being monitored/managed. This has not effect
// if AppSec is not enabled globally (e.g, via the DD_APPSEC_ENABLED environment variable).
func WithNoAppsec(disabled bool) Option {
	return func(cfg *config) {
		cfg.appsecDisabled = disabled
	}
}

// WithResponseHeaderCopier provides a function to fetch the response headers from the
// http.ResponseWriter. This allows for custom implementations as needed if you over-ride the
// default http.ResponseWriter, such as to add synchronization. Provided functions may elect to
// return a copy of the http.Header map instead of a reference to the original (e.g: to not risk
// breaking synchronization). This is currently only used by AppSec.
func WithResponseHeaderCopier(f func(http.ResponseWriter) http.Header) Option {
	return func(cfg *config) {
		cfg.appsecConfig.ResponseHeaderCopier = f
	}
}
