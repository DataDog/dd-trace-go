// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fiber

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"github.com/gofiber/fiber/v2"
)

type config struct {
	serviceName   string
	spanName      string
	isStatusError func(statusCode int) bool
	spanOpts      []tracer.StartSpanOption // additional span options to be applied
	analyticsRate float64
	resourceNamer func(*fiber.Ctx) string
	ignoreRequest func(*fiber.Ctx) bool
}

// Option describes options for the Fiber.v2 integration.
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
	cfg.spanName = instr.OperationName(instrumentation.ComponentServer, nil)
	cfg.isStatusError = isServerError
	cfg.resourceNamer = defaultResourceNamer
	cfg.ignoreRequest = defaultIgnoreRequest
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

// WithStatusCheck allow setting of a function to tell whether a status code is an error
func WithStatusCheck(fn func(statusCode int) bool) OptionFn {
	return func(cfg *config) {
		cfg.isStatusError = fn
	}
}

// WithResourceNamer specifies a function which will be used to
// obtain the resource name for a given request taking the go-fiber context
// as input
func WithResourceNamer(fn func(*fiber.Ctx) string) OptionFn {
	return func(cfg *config) {
		cfg.resourceNamer = fn
	}
}

// WithIgnoreRequest specifies a function which will be used to
// determining if the incoming HTTP request tracing should be skipped.
func WithIgnoreRequest(fn func(*fiber.Ctx) bool) OptionFn {
	return func(cfg *config) {
		cfg.ignoreRequest = fn
	}
}

func defaultResourceNamer(c *fiber.Ctx) string {
	r := c.Route()
	return r.Method + " " + r.Path
}

func defaultIgnoreRequest(*fiber.Ctx) bool {
	return false
}

func isServerError(statusCode int) bool {
	return statusCode >= 500 && statusCode < 600
}
