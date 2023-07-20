// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

// Package httptreemux provides functions to trace the dimfeld/httptreemux/v5 package (https://github.com/dimfeld/httptreemux).
package httptreemux

import (
	"net/http"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"github.com/dimfeld/httptreemux/v5"
)

const defaultServiceName = "http.router"

type routerConfig struct {
	serviceName   string
	spanOpts      []ddtrace.StartSpanOption
	resourceNamer func(*httptreemux.TreeMux, http.ResponseWriter, *http.Request) string
}

// RouterOption represents an option that can be passed to New.
type RouterOption func(*routerConfig)

func defaults(cfg *routerConfig) {
	cfg.serviceName = namingschema.ServiceName(defaultServiceName)
	cfg.resourceNamer = defaultResourceNamer
}

// WithServiceName sets the given service name for the returned router.
func WithServiceName(name string) RouterOption {
	return func(cfg *routerConfig) {
		cfg.serviceName = name
	}
}

// WithSpanOptions applies the given set of options to the span started by the router.
func WithSpanOptions(opts ...ddtrace.StartSpanOption) RouterOption {
	return func(cfg *routerConfig) {
		cfg.spanOpts = opts
	}
}

// WithResourceNamer specifies a function which will be used to obtain the
// resource name for a given request.
func WithResourceNamer(namer func(router *httptreemux.TreeMux, w http.ResponseWriter, req *http.Request) string) RouterOption {
	return func(cfg *routerConfig) {
		cfg.resourceNamer = namer
	}
}
