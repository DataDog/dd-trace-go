// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"
)

const (
	defaultClientServiceName = "grpc.client"
	defaultServerServiceName = "grpc.server"
)

type interceptorConfig struct {
	serviceName string
	spanName    string
	spanOpts    []ddtrace.StartSpanOption
}

// InterceptorOption represents an option that can be passed to the grpc unary
// client and server interceptors.
type InterceptorOption func(*interceptorConfig)

func defaults(cfg *interceptorConfig) {
	// cfg.serviceName default set in interceptor
	// cfg.spanOpts = append(cfg.spanOpts, tracer.AnalyticsRate(globalconfig.AnalyticsRate()))
	if internal.BoolEnv("DD_TRACE_GRPC_ANALYTICS_ENABLED", false) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.AnalyticsRate(1.0))
	}
}

func clientDefaults(cfg *interceptorConfig) {
	cfg.serviceName = namingschema.NewServiceNameSchema(
		"",
		defaultClientServiceName,
		namingschema.WithVersionOverride(namingschema.SchemaV0, defaultClientServiceName),
	).GetName()
	cfg.spanName = namingschema.NewGRPCClientOp().GetName()
	defaults(cfg)
}

func serverDefaults(cfg *interceptorConfig) {
	cfg.serviceName = namingschema.NewServiceNameSchema(
		"",
		defaultServerServiceName,
	).GetName()
	cfg.spanName = namingschema.NewGRPCServerOp().GetName()
	defaults(cfg)
}

// WithServiceName sets the given service name for the intercepted client.
func WithServiceName(name string) InterceptorOption {
	return func(cfg *interceptorConfig) {
		cfg.serviceName = name
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) InterceptorOption {
	return func(cfg *interceptorConfig) {
		if on {
			WithSpanOptions(tracer.AnalyticsRate(1.0))(cfg)
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) InterceptorOption {
	return func(cfg *interceptorConfig) {
		if rate >= 0.0 && rate <= 1.0 {
			WithSpanOptions(tracer.AnalyticsRate(rate))(cfg)
		}
	}
}

// WithSpanOptions defines a set of additional ddtrace.StartSpanOption to be added
// to spans started by the integration.
func WithSpanOptions(opts ...ddtrace.StartSpanOption) InterceptorOption {
	return func(cfg *interceptorConfig) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}
