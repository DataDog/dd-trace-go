// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package chi

import (
	"math"
	"net/http"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/emitter/httpsec"
)

type config struct {
	serviceName        string
	spanOpts           []tracer.StartSpanOption // additional span options to be applied
	analyticsRate      float64
	isStatusError      func(statusCode int) bool
	ignoreRequest      func(r *http.Request) bool
	modifyResourceName func(resourceName string) string
	headerTags         instrumentation.HeaderTags
	resourceNamer      func(r *http.Request) string
	appsecDisabled     bool
	appsecConfig       httpsec.Config
}

// Option describes options for the Chi.v5 integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to Middleware.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func defaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentServer, nil)
	cfg.analyticsRate = instr.AnalyticsRate(true)
	cfg.headerTags = instr.HTTPHeadersAsTags()
	cfg.ignoreRequest = func(_ *http.Request) bool { return false }
	cfg.modifyResourceName = func(s string) string { return s }
	// for backward compatibility with modifyResourceName, initialize resourceName as nil.
	cfg.resourceNamer = nil
	cfg.appsecDisabled = false
	cfg.appsecConfig.Framework = "github.com/go-chi/chi/v5"
}

// WithService sets the given service name for the router.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithSpanOptions applies the given set of options to the spans started
// by the router.
func WithSpanOptions(opts ...tracer.StartSpanOption) OptionFn {
	return func(cfg *config) {
		cfg.spanOpts = opts
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
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
func WithAnalyticsRate(rate float64) OptionFn {
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
func WithStatusCheck(fn func(statusCode int) bool) OptionFn {
	return func(cfg *config) {
		cfg.isStatusError = fn
	}
}

// WithIgnoreRequest specifies a function to use for determining if the
// incoming HTTP request tracing should be skipped.
func WithIgnoreRequest(fn func(r *http.Request) bool) OptionFn {
	return func(cfg *config) {
		cfg.ignoreRequest = fn
	}
}

// WithModifyResourceName specifies a function to use to modify the resource name.
func WithModifyResourceName(fn func(resourceName string) string) OptionFn {
	return func(cfg *config) {
		cfg.modifyResourceName = fn
	}
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headers []string) OptionFn {
	return func(cfg *config) {
		cfg.headerTags = instrumentation.NewHeaderTags(headers)
	}
}

// WithResourceNamer specifies a function to use for determining the resource
// name of the span.
func WithResourceNamer(fn func(r *http.Request) string) OptionFn {
	return func(cfg *config) {
		cfg.resourceNamer = fn
	}
}

// WithNoAppsec opts this router out of AppSec management. This allows a particular router to bypass
// appsec, while the rest of the application is still being monitored/managed. This has not effect
// if AppSec is not enabled globally (e.g, via the DD_APPSEC_ENABLED environment variable).
func WithNoAppsec(disabled bool) OptionFn {
	return func(cfg *config) {
		cfg.appsecDisabled = disabled
	}
}

// WithResponseHeaderCopier provides a function to fetch the response headers from the
// http.ResponseWriter. This allows for custom implementations as needed if you over-ride the
// default http.ResponseWriter, such as to add synchronization. Provided functions may elect to
// return a copy of the http.Header map instead of a reference to the original (e.g: to not risk
// breaking synchronization). This is currently only used by AppSec.
func WithResponseHeaderCopier(f func(http.ResponseWriter) http.Header) OptionFn {
	return func(cfg *config) {
		cfg.appsecConfig.ResponseHeaderCopier = f
	}
}
