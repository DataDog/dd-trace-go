// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package gearbox

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/gogearbox/gearbox"
)

const defaultServiceName = "gearbox"

type config struct {
	serviceName   string
	spanName      string
	spanOpts      []ddtrace.StartSpanOption
	isStatusError func(int) bool
	resourceNamer func(gearbox.Context) string
	ignoreRequest func(gearbox.Context) bool
}

type Option func(*config)

// MTOFF: Do we want to support app analytics on new integrations? As in, should I add support for it?
func newConfig() *config {
	return &config{
		serviceName:   namingschema.NewDefaultServiceName(defaultServiceName).GetName(),
		spanName:      namingschema.NewHTTPServerOp().GetName(),
		isStatusError: isServerError,
		resourceNamer: defaultResourceNamer,
		ignoreRequest: defaultResourcesIgnored,
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
// obtain the resource name for a given request taking the gearbox.Context context
// as input
func WithResourceNamer(fn func(gctx gearbox.Context) string) Option {
	return func(cfg *config) {
		cfg.resourceNamer = fn
	}
}

// WithIgnoreRequest specifies a function to use for determining if the
// incoming HTTP request tracing should be skipped.
func WithIgnoreRequest(f func(gctx gearbox.Context) bool) Option {
	return func(cfg *config) {
		cfg.ignoreRequest = f
	}
}

func isServerError(statusCode int) bool {
	return statusCode >= 500 && statusCode < 600
}

func defaultResourceNamer(gctx gearbox.Context) string {
	fctx := gctx.Context()
	return string(fctx.Method()) + " " + string(fctx.Path())
}

func defaultResourcesIgnored(gearbox.Context) bool {
	return false
}
