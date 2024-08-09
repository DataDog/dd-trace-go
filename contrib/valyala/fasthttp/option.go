// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type config struct {
	serviceName   string
	spanName      string
	spanOpts      []tracer.StartSpanOption
	isStatusError func(int) bool
	resourceNamer func(*fasthttp.RequestCtx) string
	ignoreRequest func(*fasthttp.RequestCtx) bool
}

// Option describes options for the FastHTTP integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to WrapHandler.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

func newConfig() *config {
	return &config{
		serviceName:   instr.ServiceName(instrumentation.ComponentServer, nil),
		spanName:      instr.OperationName(instrumentation.ComponentServer, nil),
		isStatusError: defaultIsServerError,
		resourceNamer: defaultResourceNamer,
		ignoreRequest: defaultIgnoreRequest,
	}
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

// WithStatusCheck allows customization over which status code(s) to consider "error"
func WithStatusCheck(fn func(statusCode int) bool) OptionFn {
	return func(cfg *config) {
		cfg.isStatusError = fn
	}
}

// WithResourceNamer specifies a function which will be used to
// obtain the resource name for a given request
func WithResourceNamer(fn func(fctx *fasthttp.RequestCtx) string) OptionFn {
	return func(cfg *config) {
		cfg.resourceNamer = fn
	}
}

// WithIgnoreRequest specifies a function to use for determining if the
// incoming HTTP request tracing should be skipped.
func WithIgnoreRequest(f func(fctx *fasthttp.RequestCtx) bool) OptionFn {
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
