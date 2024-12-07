// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/namingschema"

	"google.golang.org/grpc/codes"
)

const (
	defaultClientServiceName = "grpc.client"
	defaultServerServiceName = "grpc.server"
)

// Option specifies a configuration option for the grpc package. Not all options apply
// to all instrumented structures.
type Option func(*config)

type config struct {
	serviceName         func() string
	spanName            string
	nonErrorCodes       map[codes.Code]bool
	errCheck            func(method string, err error) bool
	traceStreamCalls    bool
	traceStreamMessages bool
	noDebugStack        bool
	ignoredMethods      map[string]struct{}
	untracedMethods     map[string]struct{}
	withMetadataTags    bool
	ignoredMetadata     map[string]struct{}
	withRequestTags     bool
	withErrorDetailTags bool
	spanOpts            []ddtrace.StartSpanOption
	tags                map[string]interface{}
}

// InterceptorOption represents an option that can be passed to the grpc unary
// client and server interceptors.
// InterceptorOption is deprecated in favor of Option.
type InterceptorOption = Option

func defaults(cfg *config) {
	cfg.traceStreamCalls = true
	cfg.traceStreamMessages = true
	cfg.nonErrorCodes = map[codes.Code]bool{codes.Canceled: true}
	// cfg.spanOpts = append(cfg.spanOpts, tracer.AnalyticsRate(globalconfig.AnalyticsRate()))
	if internal.BoolEnv("DD_TRACE_GRPC_ANALYTICS_ENABLED", false) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.AnalyticsRate(1.0))
	}
	cfg.ignoredMetadata = map[string]struct{}{
		"x-datadog-trace-id":          {},
		"x-datadog-parent-id":         {},
		"x-datadog-sampling-priority": {},
	}
}

func clientDefaults(cfg *config) {
	sn := namingschema.ServiceNameOverrideV0(defaultClientServiceName, defaultClientServiceName)
	cfg.serviceName = func() string { return sn }
	cfg.spanName = namingschema.OpName(namingschema.GRPCClient)
	defaults(cfg)
}

func serverDefaults(cfg *config) {
	// We check for a configured service name, so we don't break users who are incorrectly creating their server
	// before the call `tracer.Start()`
	if globalconfig.ServiceName() != "" {
		sn := namingschema.ServiceName(defaultServerServiceName)
		cfg.serviceName = func() string { return sn }
	} else {
		log.Warn("No global service name was detected. GRPC Server may have been created before calling tracer.Start(). Will dynamically fetch service name for every span. " +
			"Note this may have a slight performance cost, it is always recommended to start the tracer before initializing any traced packages.\n")
		cfg.serviceName = func() string { return namingschema.ServiceName(defaultServerServiceName) }
	}
	cfg.spanName = namingschema.OpName(namingschema.GRPCServer)
	defaults(cfg)
}

// WithServiceName sets the given service name for the intercepted client.
func WithServiceName(name string) Option {
	return func(cfg *config) {
		cfg.serviceName = func() string { return name }
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

// WithErrorCheck sets a custom function to determine whether an error should not be considered as an error for tracing purposes.
// This function is evaluated when an error occurs, and if it returns true, the error will not be recorded in the trace.
// f: A function taking the gRPC method and error as arguments, returning a boolean to indicate if the error should be ignored.
func WithErrorCheck(f func(method string, err error) bool) Option {
	return func(cfg *config) {
		cfg.errCheck = f
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return func(cfg *config) {
		if on {
			WithSpanOptions(tracer.AnalyticsRate(1.0))(cfg)
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			WithSpanOptions(tracer.AnalyticsRate(rate))(cfg)
		}
	}
}

// WithIgnoredMethods specifies full methods to be ignored by the server side interceptor.
// When an incoming request's full method is in ms, no spans will be created.
//
// Deprecated: This is deprecated in favor of WithUntracedMethods which applies to both
// the server side and client side interceptors.
func WithIgnoredMethods(ms ...string) Option {
	ims := make(map[string]struct{}, len(ms))
	for _, e := range ms {
		ims[e] = struct{}{}
	}
	return func(cfg *config) {
		cfg.ignoredMethods = ims
	}
}

// WithUntracedMethods specifies full methods to be ignored by the server side and client
// side interceptors. When a request's full method is in ms, no spans will be created.
func WithUntracedMethods(ms ...string) Option {
	ums := make(map[string]struct{}, len(ms))
	for _, e := range ms {
		ums[e] = struct{}{}
	}
	return func(cfg *config) {
		cfg.untracedMethods = ums
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

// WithErrorDetailTags specifies whether gRPC responses details contain should be added to spans as tags.
func WithErrorDetailTags() Option {
	return func(cfg *config) {
		cfg.withErrorDetailTags = true
	}
}

// WithCustomTag will attach the value to the span tagged by the key.
func WithCustomTag(key string, value interface{}) Option {
	return func(cfg *config) {
		if cfg.tags == nil {
			cfg.tags = make(map[string]interface{})
		}
		cfg.tags[key] = value
	}
}

// WithSpanOptions defines a set of additional ddtrace.StartSpanOption to be added
// to spans started by the integration.
func WithSpanOptions(opts ...ddtrace.StartSpanOption) Option {
	return func(cfg *config) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}
