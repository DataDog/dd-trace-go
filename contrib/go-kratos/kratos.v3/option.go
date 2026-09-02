// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package kratos

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/env"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/options"
)

const (
	envClientErrorStatuses      = "DD_TRACE_HTTP_CLIENT_ERROR_STATUSES"
	envClientQueryStringEnabled = "DD_TRACE_HTTP_CLIENT_TAG_QUERY_STRING"
	envQueryStringDisabled      = "DD_TRACE_HTTP_URL_QUERY_STRING_DISABLED"
	envServerErrorStatuses      = "DD_TRACE_HTTP_SERVER_ERROR_STATUSES"
)

type config struct {
	serviceName   string
	serviceSource string
	noDebugStack  bool
	spanOpts      []tracer.StartSpanOption
	headerTags    instrumentation.HeaderTags
	analyticsRate float64
	queryString   bool
	isStatusError func(int) bool
}

// Option configures the Kratos tracing middleware.
type Option func(*config)

func applyOptions(cfg *config, opts []Option) {
	for _, opt := range opts {
		opt(cfg)
	}
}

func defaults(cfg *config) {
	cfg.headerTags = instr.HTTPHeadersAsTags()
	cfg.analyticsRate = instr.AnalyticsRate(true)
	cfg.queryString = !options.GetBoolEnv(envQueryStringDisabled, false)
}

func serverDefaults(cfg *config) {
	defaults(cfg)
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentServer, nil)
	cfg.serviceSource = string(component)
	cfg.isStatusError = isServerError
	if fn := httptrace.GetErrorCodesFromInput(env.Get(envServerErrorStatuses)); fn != nil {
		cfg.isStatusError = fn
	}
}

func clientDefaults(cfg *config) {
	defaults(cfg)
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentClient, nil)
	cfg.serviceSource = string(component)
	cfg.queryString = cfg.queryString && options.GetBoolEnv(envClientQueryStringEnabled, true)
	cfg.isStatusError = isClientError
	if fn := httptrace.GetErrorCodesFromInput(env.Get(envClientErrorStatuses)); fn != nil {
		cfg.isStatusError = fn
	}
}

func isServerError(statusCode int) bool {
	return statusCode >= 500 && statusCode < 600
}

func isClientError(statusCode int) bool {
	return statusCode >= 400 && statusCode < 500
}

// WithService sets the service name for spans created by the middleware.
func WithService(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
		cfg.serviceSource = instrumentation.ServiceSourceWithServiceOption
	}
}

// NoDebugStack prevents error spans from collecting a Go stack trace.
func NoDebugStack() Option {
	return func(cfg *config) {
		cfg.noDebugStack = true
	}
}

// WithAnalytics enables or disables Trace Analytics for spans created by the middleware.
func WithAnalytics(on bool) Option {
	return func(cfg *config) {
		if on {
			cfg.analyticsRate = 1.0
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithAnalyticsRate sets the Trace Analytics sampling rate for spans created by the middleware.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithStatusCheck sets the function used to determine whether an HTTP status code is an error.
func WithStatusCheck(fn func(statusCode int) bool) Option {
	return func(cfg *config) {
		cfg.isStatusError = fn
	}
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers cannot be sub-selected. For example, an entire Cookie header would be transmitted,
// without the ability to select individual cookies.
func WithHeaderTags(headers []string) Option {
	return func(cfg *config) {
		cfg.headerTags = instrumentation.NewHeaderTags(headers)
	}
}

// WithSpanOptions adds options to every span created by the middleware.
func WithSpanOptions(opts ...tracer.StartSpanOption) Option {
	return func(cfg *config) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}
