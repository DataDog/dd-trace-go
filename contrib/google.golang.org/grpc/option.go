// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"math"

	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"

	"google.golang.org/grpc/codes"
)

// Option specifies a configuration option for the grpc package. Not all options apply
// to all instrumented structures.
type Option func(*config)

type config struct {
	serviceName         string
	nonErrorCodes       map[codes.Code]bool
	analyticsRate       float64
	traceStreamCalls    bool
	traceStreamMessages bool
	noDebugStack        bool
	ignoredMethods      map[string]struct{}
	withMetadataTags    bool
	ignoredMetadata     map[string]struct{}
	withRequestTags     bool
}

func (cfg *config) serverServiceName() string {
	if cfg.serviceName != "" {
		return cfg.serviceName
	}
	if svc := globalconfig.ServiceName(); svc != "" {
		return svc
	}
	return "grpc.server"
}

func (cfg *config) clientServiceName() string {
	if cfg.serviceName == "" {
		return "grpc.client"
	}
	return cfg.serviceName
}

// InterceptorOption represents an option that can be passed to the grpc unary
// client and server interceptors.
// InterceptorOption is deprecated in favor of Option.
type InterceptorOption = Option

func defaults(cfg *config) {
	// cfg.serviceName defaults are set in interceptors
	cfg.traceStreamCalls = true
	cfg.traceStreamMessages = true
	cfg.nonErrorCodes = map[codes.Code]bool{codes.Canceled: true}
	// cfg.analyticsRate = globalconfig.AnalyticsRate()
	if internal.BoolEnv("DD_TRACE_GRPC_ANALYTICS_ENABLED", false) {
		cfg.analyticsRate = 1.0
	} else {
		cfg.analyticsRate = math.NaN()
	}
	cfg.ignoredMetadata = map[string]struct{}{
		"x-datadog-trace-id":          {},
		"x-datadog-parent-id":         {},
		"x-datadog-sampling-priority": {},
	}
}

// WithServiceName sets the given service name for the intercepted client.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = name
	}
}

// WithStreamCalls enables or disables tracing of streaming calls. This option does not apply to the
// stats handler.
func WithStreamCalls(enabled bool) Option {
	return func(cfg *config) {
		cfg.traceStreamCalls = enabled
	}
}

// WithStreamMessages enables or disables tracing of streaming messages. This option does not apply
// to the stats handler.
func WithStreamMessages(enabled bool) Option {
	return func(cfg *config) {
		cfg.traceStreamMessages = enabled
	}
}

// NoDebugStack disables debug stacks for traces with errors. This is useful in situations
// where errors are frequent and the overhead of calling debug.Stack may affect performance.
func NoDebugStack() Option {
	return func(cfg *config) {
		cfg.noDebugStack = true
	}
}

// NonErrorCodes determines the list of codes which will not be considered errors in instrumentation.
// This call overrides the default handling of codes.Canceled as a non-error.
func NonErrorCodes(cs ...codes.Code) InterceptorOption {
	return func(cfg *config) {
		cfg.nonErrorCodes = make(map[codes.Code]bool, len(cs))
		for _, c := range cs {
			cfg.nonErrorCodes[c] = true
		}
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
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
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			cfg.analyticsRate = rate
		} else {
			cfg.analyticsRate = math.NaN()
		}
	}
}

// WithIgnoredMethods specifies full methods to be ignored by the server side interceptor.
// When an incoming request's full method is in ms, no spans will be created.
func WithIgnoredMethods(ms ...string) Option {
	ims := make(map[string]struct{}, len(ms))
	for _, e := range ms {
		ims[e] = struct{}{}
	}
	return func(cfg *config) {
		cfg.ignoredMethods = ims
	}
}

// WithMetadataTags specifies whether gRPC metadata should be added to spans as tags.
func WithMetadataTags() Option {
	return func(cfg *config) {
		cfg.withMetadataTags = true
	}
}

// WithIgnoredMetadata specifies keys to be ignored while tracing the metadata. Must be used
// in conjunction with WithMetadataTags.
func WithIgnoredMetadata(ms ...string) Option {
	return func(cfg *config) {
		for _, e := range ms {
			cfg.ignoredMetadata[e] = struct{}{}
		}
	}
}

// WithRequestTags specifies whether gRPC requests should be added to spans as tags.
func WithRequestTags() Option {
	return func(cfg *config) {
		cfg.withRequestTags = true
	}
}
