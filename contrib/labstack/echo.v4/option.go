// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package echo

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/labstack/echo.v4/v2"

	"github.com/labstack/echo/v4"
)

// Option represents an option that can be passed to Middleware.
type Option = v2.Option

// IgnoreRequestFunc determines if tracing will be skipped for a request.
type IgnoreRequestFunc = v2.IgnoreRequestFunc

// WithServiceName sets the given service name for the system.
func WithServiceName(name string) Option {
	return v2.WithService(name)
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

// NoDebugStack prevents stack traces from being attached to spans finishing
// with an error. This is useful in situations where errors are frequent and
// performance is critical.
func NoDebugStack() Option {
	return v2.NoDebugStack()
}

// WithIgnoreRequest sets a function which determines if tracing will be
// skipped for a given request.
func WithIgnoreRequest(ignoreRequestFunc IgnoreRequestFunc) Option {
	return v2.WithIgnoreRequest(ignoreRequestFunc)
}

// WithErrorTranslator sets a function to translate Go errors into echo Errors.
// This is used for extracting the HTTP response status code.
func WithErrorTranslator(fn func(err error) (*echo.HTTPError, bool)) Option {
	return v2.WithErrorTranslator(fn)
}

// WithStatusCheck specifies a function fn which reports whether the passed
// statusCode should be considered an error.
func WithStatusCheck(fn func(statusCode int) bool) Option {
	return v2.WithStatusCheck(fn)
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers can not be sub-selected. E.g., an entire Cookie header would be transmitted, without the ability to choose specific Cookies.
func WithHeaderTags(headers []string) Option {
	return v2.WithHeaderTags(headers)
}

// WithErrorCheck sets the func which determines if err would be ignored (if it returns true, the error is not tagged).
// This function also checks the errors created from the WithStatusCheck option.
func WithErrorCheck(errCheck func(error) bool) Option {
	return v2.WithErrorCheck(errCheck)
}

// WithCustomTag will attach the value to the span tagged by the key. Standard
// span tags cannot be replaced.
func WithCustomTag(key string, value interface{}) Option {
	return v2.WithCustomTag(key, value)
}
