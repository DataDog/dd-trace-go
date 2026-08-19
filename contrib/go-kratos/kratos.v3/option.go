// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package kratos

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

type config struct {
	serviceName   string
	serviceSource string
	noDebugStack  bool
	spanOpts      []tracer.StartSpanOption
	headerTags    instrumentation.HeaderTags
}

// Option configures the Kratos tracing middleware.
type Option func(*config)

func applyOptions(cfg *config, opts []Option) {
	for _, opt := range opts {
		opt(cfg)
	}
}

func defaults(cfg *config) {
	cfg.headerTags = instr.HTTPHeadersAsTags()
}

func serverDefaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentServer, nil)
	cfg.serviceSource = string(component)
	defaults(cfg)
}

func clientDefaults(cfg *config) {
	cfg.serviceName = instr.ServiceName(instrumentation.ComponentClient, nil)
	cfg.serviceSource = string(component)
	defaults(cfg)
}

// WithService sets the service name for spans created by the middleware.
func WithService(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
		cfg.serviceSource = instrumentation.ServiceSourceWithServiceOption
	}
}

// NoDebugStack prevents error spans from collecting a Go stack trace.
func NoDebugStack() Option {
	return func(cfg *config) {
		cfg.noDebugStack = true
	}
}

// WithHeaderTags enables the integration to attach HTTP request headers as span tags.
// Warning:
// Using this feature can risk exposing sensitive data such as authorization tokens to Datadog.
// Special headers cannot be sub-selected. For example, an entire Cookie header would be transmitted,
// without the ability to select individual cookies.
func WithHeaderTags(headers []string) Option {
	return func(cfg *config) {
		cfg.headerTags = instrumentation.NewHeaderTags(headers)
	}
}

// WithSpanOptions adds options to every span created by the middleware.
func WithSpanOptions(opts ...tracer.StartSpanOption) Option {
	return func(cfg *config) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}
