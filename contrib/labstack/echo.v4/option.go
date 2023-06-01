// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/normalizer"

	"github.com/labstack/echo/v4"
)

const defaultServiceName = "echo"

type config struct {
	serviceName       string
	analyticsRate     float64
	noDebugStack      bool
	ignoreRequestFunc IgnoreRequestFunc
	isStatusError     func(statusCode int) bool
	headerTagsLocal bool
}

var headerTagsMap = make(map[string]string)

func headerTag(header string) (tag string, ok bool) {
	tag, ok = headerTagsMap[header]
	return
}

// Option represents an option that can be passed to Middleware.
type Option func(*config)

// IgnoreRequestFunc determines if tracing will be skipped for a request.
type IgnoreRequestFunc func(c echo.Context) bool

func defaults(cfg *config) {
	cfg.serviceName = namingschema.NewServiceNameSchema("", defaultServiceName).GetName()
	if svc := globalconfig.ServiceName(); svc != "" {
		cfg.serviceName = svc
	}
	cfg.analyticsRate = math.NaN()
	cfg.isStatusError = isServerError
	cfg.headerTagsLocal = false
}

// WithServiceName sets the given service name for the system.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
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

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() Option {
	return func(cfg *config) {
		cfg.noDebugStack = true
	}
}

// WithIgnoreRequest sets a function which determines if tracing will be
// skipped for a given request.
func WithIgnoreRequest(ignoreRequestFunc IgnoreRequestFunc) Option {
	return func(cfg *config) {
		cfg.ignoreRequestFunc = ignoreRequestFunc
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

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Cookies will not be sub-selected. If the header Cookie is activated, then all cookies will be transmitted.
func WithHeaderTags(headers []string) Option {
	for _, h := range headers {
		header, tag := normalizer.NormalizeHeaderTag(h)
		headerTagsMap[header] = tag
	}
	return func(cfg *config) {
		cfg.headerTagsLocal = true
	}
}
