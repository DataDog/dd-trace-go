// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	v2 "github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"

	"google.golang.org/grpc/codes"
)

// Option specifies a configuration option for the grpc package. Not all options apply
// to all instrumented structures.
type Option = v2.Option

// InterceptorOption represents an option that can be passed to the grpc unary
// client and server interceptors.
// InterceptorOption is deprecated in favor of Option.
type InterceptorOption = Option

// WithServiceName sets the given service name for the intercepted client.
func WithServiceName(name string) Option {
	return v2.WithService(name)
}

// WithStreamCalls enables or disables tracing of streaming calls. This option does not apply to the
// stats handler.
func WithStreamCalls(enabled bool) Option {
	return v2.WithStreamCalls(enabled)
}

// WithStreamMessages enables or disables tracing of streaming messages. This option does not apply
// to the stats handler.
func WithStreamMessages(enabled bool) Option {
	return v2.WithStreamMessages(enabled)
}

// NoDebugStack disables debug stacks for traces with errors. This is useful in situations
// where errors are frequent and the overhead of calling debug.Stack may affect performance.
func NoDebugStack() Option {
	return v2.NoDebugStack()
}

// NonErrorCodes determines the list of codes which will not be considered errors in instrumentation.
// This call overrides the default handling of codes.Canceled as a non-error.
func NonErrorCodes(cs ...codes.Code) InterceptorOption {
	return v2.NonErrorCodes(cs...)
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) Option {
	return v2.WithAnalytics(on)
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) Option {
	return v2.WithAnalyticsRate(rate)
}

// WithIgnoredMethods specifies full methods to be ignored by the server side interceptor.
// When an incoming request's full method is in ms, no spans will be created.
//
// Deprecated: This is deprecated in favor of WithUntracedMethods which applies to both
// the server side and client side interceptors.
func WithIgnoredMethods(ms ...string) Option {
	return v2.WithUntracedMethods(ms...)
}

// WithUntracedMethods specifies full methods to be ignored by the server side and client
// side interceptors. When a request's full method is in ms, no spans will be created.
func WithUntracedMethods(ms ...string) Option {
	return v2.WithUntracedMethods(ms...)
}

// WithMetadataTags specifies whether gRPC metadata should be added to spans as tags.
func WithMetadataTags() Option {
	return v2.WithMetadataTags()
}

// WithIgnoredMetadata specifies keys to be ignored while tracing the metadata. Must be used
// in conjunction with WithMetadataTags.
func WithIgnoredMetadata(ms ...string) Option {
	return v2.WithIgnoredMetadata(ms...)
}

// WithRequestTags specifies whether gRPC requests should be added to spans as tags.
func WithRequestTags() Option {
	return v2.WithRequestTags()
}

// WithErrorDetailTags specifies whether gRPC responses details contain should be added to spans as tags.
func WithErrorDetailTags() Option {
	return v2.WithErrorDetailTags()
}

// WithCustomTag will attach the value to the span tagged by the key.
func WithCustomTag(key string, value interface{}) Option {
	return v2.WithCustomTag(key, value)
}

// WithSpanOptions defines a set of additional ddtrace.StartSpanOption to be added
// to spans started by the integration.
func WithSpanOptions(opts ...ddtrace.StartSpanOption) Option {
	return v2.WithSpanOptions(tracer.ApplyV1Options(opts...))
}
