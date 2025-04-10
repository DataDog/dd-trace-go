// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fasthttp

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/valyala/fasthttp/v2"
	"github.com/valyala/fasthttp"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

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

// WithStatusCheck allows customization over which status code(s) to consider "error"
func WithStatusCheck(fn func(statusCode int) bool) Option {
	return v2.WithStatusCheck(fn)
}

// WithResourceNamer specifies a function which will be used to
// obtain the resource name for a given request
func WithResourceNamer(fn func(fctx *fasthttp.RequestCtx) string) Option {
	return v2.WithResourceNamer(fn)
}

// WithIgnoreRequest specifies a function to use for determining if the
// incoming HTTP request tracing should be skipped.
func WithIgnoreRequest(f func(fctx *fasthttp.RequestCtx) bool) Option {
	return v2.WithIgnoreRequest(f)
}
