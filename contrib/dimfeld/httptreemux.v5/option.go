// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httptreemux provides functions to trace the dimfeld/httptreemux/v5 package (https://github.com/dimfeld/httptreemux).
package httptreemux

import (
	"net/http"

	"github.com/dimfeld/httptreemux/v5"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type routerConfig struct {
	serviceName   string
	spanOpts      []tracer.StartSpanOption
	resourceNamer func(*httptreemux.TreeMux, http.ResponseWriter, *http.Request) string
}

// RouterOption describes options for the router.
type RouterOption interface {
	apply(*routerConfig)
}

// RouterOptionFn represents options applicable to New and NewWithContext.
type RouterOptionFn func(*routerConfig)

func (fn RouterOptionFn) apply(cfg *routerConfig) {
	fn(cfg)
}

func defaults(cfg *routerConfig) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentServer, nil)
	cfg.resourceNamer = defaultResourceNamer
}

// WithService sets the given service name for the returned router.
func WithService(name string) RouterOptionFn {
	return func(cfg *routerConfig) {
		cfg.serviceName = name
	}
}

// WithSpanOptions applies the given set of options to the span started by the router.
func WithSpanOptions(opts ...tracer.StartSpanOption) RouterOptionFn {
	return func(cfg *routerConfig) {
		cfg.spanOpts = opts
	}
}

// WithResourceNamer specifies a function which will be used to obtain the
// resource name for a given request.
func WithResourceNamer(namer func(router *httptreemux.TreeMux, w http.ResponseWriter, req *http.Request) string) RouterOptionFn {
	return func(cfg *routerConfig) {
		cfg.resourceNamer = namer
	}
}
