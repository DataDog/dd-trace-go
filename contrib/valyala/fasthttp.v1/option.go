// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"github.com/valyala/fasthttp"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

const defaultServiceName = "fasthttp"

type config struct {
	serviceName   string
	spanName      string
	spanOpts      []ddtrace.StartSpanOption
	isStatusError func(int) bool
	resourceNamer func(*fasthttp.RequestCtx) string
	ignoreRequest func(*fasthttp.RequestCtx) bool
}

type Option func(*config)

func newConfig() *config {
	return &config{
		serviceName:   namingschema.ServiceName(defaultServiceName),
		spanName:      namingschema.OpName(namingschema.HTTPServer),
		isStatusError: defaultIsServerError,
		resourceNamer: defaultResourceNamer,
		ignoreRequest: defaultIgnoreRequest,
	}
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

// WithStatusCheck allows customization over which status code(s) to consider "error"
func WithStatusCheck(fn func(statusCode int) bool) Option {
	return func(cfg *config) {
		cfg.isStatusError = fn
	}
}

// WithResourceNamer specifies a function which will be used to
// obtain the resource name for a given request
func WithResourceNamer(fn func(fctx *fasthttp.RequestCtx) string) Option {
	return func(cfg *config) {
		cfg.resourceNamer = fn
	}
}

// WithIgnoreRequest specifies a function to use for determining if the
// incoming HTTP request tracing should be skipped.
func WithIgnoreRequest(f func(fctx *fasthttp.RequestCtx) bool) Option {
	return func(cfg *config) {
		cfg.ignoreRequest = f
	}
}

func defaultIsServerError(statusCode int) bool {
	return statusCode >= 500 && statusCode < 600
}

func defaultResourceNamer(fctx *fasthttp.RequestCtx) string {
	return string(fctx.Method()) + " " + string(fctx.Path())
}

func defaultIgnoreRequest(_ *fasthttp.RequestCtx) bool {
	return false
}
