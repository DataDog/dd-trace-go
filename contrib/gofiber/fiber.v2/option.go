// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fiber

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"github.com/gofiber/fiber/v2"
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

// WithStatusCheck allow setting of a function to tell whether a status code is an error
func WithStatusCheck(fn func(statusCode int) bool) Option {
	return v2.WithStatusCheck(fn)
}

// WithResourceNamer specifies a function which will be used to
// obtain the resource name for a given request taking the go-fiber context
// as input
func WithResourceNamer(fn func(*fiber.Ctx) string) Option {
	return v2.WithResourceNamer(fn)
}

// WithIgnoreRequest specifies a function which will be used to
// determining if the incoming HTTP request tracing should be skipped.
func WithIgnoreRequest(fn func(*fiber.Ctx) bool) Option {
	return v2.WithIgnoreRequest(fn)
}
