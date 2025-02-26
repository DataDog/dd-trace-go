// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"math"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"google.golang.org/grpc/codes"
)

// Option describes options for the gRPC integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to StreamClientInterceptor, UnaryClientInterceptor, StreamServerInterceptor,
// UnaryServerInterceptor, NewClientStatsHandler and NewServerStatsHandler.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

type cachedServiceName struct {
	value    string
	getValue func() string
}

func newCachedServiceName(getValue func() string) *cachedServiceName {
	c := &cachedServiceName{getValue: getValue}
	// warmup cache
	_ = c.String()
	return c
}

func (cs *cachedServiceName) String() string {
	if cs.value != "" {
		return cs.value
	}
	svc := cs.getValue()
	// cache only if the tracer has been started. This ensures we get the final value for service name, since this
	// is where the tracer configuration is resolved (including env variables and tracer options).
	if instr.TracerInitialized() {
		cs.value = svc
	}
	return svc
}

type config struct {
	serviceName         *cachedServiceName
	spanName            string
	nonErrorCodes       map[codes.Code]bool
	traceStreamCalls    bool
	traceStreamMessages bool
	noDebugStack        bool
	untracedMethods     map[string]struct{}
	withMetadataTags    bool
	ignoredMetadata     map[string]struct{}
	withRequestTags     bool
	withErrorDetailTags bool
	spanOpts            []tracer.StartSpanOption
	tags                map[string]interface{}
}

func defaults(cfg *config) {
	cfg.traceStreamCalls = true
	cfg.traceStreamMessages = true
	cfg.nonErrorCodes = map[codes.Code]bool{codes.Canceled: true}
	if rate := instr.AnalyticsRate(false); !math.IsNaN(rate) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.AnalyticsRate(rate))
	}
	cfg.ignoredMetadata = map[string]struct{}{
		"x-datadog-trace-id":          {},
		"x-datadog-parent-id":         {},
		"x-datadog-sampling-priority": {},
	}
}

func clientDefaults(cfg *config) {
	cfg.serviceName = newCachedServiceName(func() string {
		return instr.ServiceName(instrumentation.ComponentClient, nil)
	})
	cfg.spanName = instr.OperationName(instrumentation.ComponentClient, nil)
	defaults(cfg)
}

func serverDefaults(cfg *config) {
	cfg.serviceName = newCachedServiceName(func() string {
		return instr.ServiceName(instrumentation.ComponentServer, nil)
	})
	cfg.spanName = instr.OperationName(instrumentation.ComponentServer, nil)
	defaults(cfg)
}

// WithService sets the given service name for the intercepted client.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = newCachedServiceName(func() string {
			return name
		})
	}
}

// WithStreamCalls enables or disables tracing of streaming calls. This option does not apply to the
// stats handler.
func WithStreamCalls(enabled bool) OptionFn {
	return func(cfg *config) {
		cfg.traceStreamCalls = enabled
	}
}

// WithStreamMessages enables or disables tracing of streaming messages. This option does not apply
// to the stats handler.
func WithStreamMessages(enabled bool) OptionFn {
	return func(cfg *config) {
		cfg.traceStreamMessages = enabled
	}
}

// NoDebugStack disables debug stacks for traces with errors. This is useful in situations
// where errors are frequent, and the overhead of calling debug.Stack may affect performance.
func NoDebugStack() OptionFn {
	return func(cfg *config) {
		cfg.noDebugStack = true
	}
}

// NonErrorCodes determines the list of codes that will not be considered errors in instrumentation.
// This call overrides the default handling of codes.Canceled as a non-error.
func NonErrorCodes(cs ...codes.Code) OptionFn {
	return func(cfg *config) {
		cfg.nonErrorCodes = make(map[codes.Code]bool, len(cs))
		for _, c := range cs {
			cfg.nonErrorCodes[c] = true
		}
	}
}

// WithAnalytics enables Trace Analytics for all started spans.
func WithAnalytics(on bool) OptionFn {
	return func(cfg *config) {
		if on {
			WithSpanOptions(tracer.AnalyticsRate(1.0))(cfg)
		}
	}
}

// WithAnalyticsRate sets the sampling rate for Trace Analytics events
// correlated to started spans.
func WithAnalyticsRate(rate float64) OptionFn {
	return func(cfg *config) {
		if rate >= 0.0 && rate <= 1.0 {
			WithSpanOptions(tracer.AnalyticsRate(rate))(cfg)
		}
	}
}

// WithUntracedMethods specifies full methods to be ignored by the server side and client
// side interceptors. When a request's full method is in ms, no spans will be created.
func WithUntracedMethods(ms ...string) OptionFn {
	ums := make(map[string]struct{}, len(ms))
	for _, e := range ms {
		ums[e] = struct{}{}
	}
	return func(cfg *config) {
		cfg.untracedMethods = ums
	}
}

// WithMetadataTags specifies whether gRPC metadata should be added to spans as tags.
func WithMetadataTags() OptionFn {
	return func(cfg *config) {
		cfg.withMetadataTags = true
	}
}

// WithIgnoredMetadata specifies keys to be ignored while tracing the metadata. Must be used
// in conjunction with WithMetadataTags.
func WithIgnoredMetadata(ms ...string) OptionFn {
	return func(cfg *config) {
		for _, e := range ms {
			cfg.ignoredMetadata[e] = struct{}{}
		}
	}
}

// WithRequestTags specifies whether gRPC requests should be added to spans as tags.
func WithRequestTags() OptionFn {
	return func(cfg *config) {
		cfg.withRequestTags = true
	}
}

// WithErrorDetailTags specifies whether gRPC responses details contain should be added to spans as tags.
func WithErrorDetailTags() OptionFn {
	return func(cfg *config) {
		cfg.withErrorDetailTags = true
	}
}

// WithCustomTag will attach the value to the span tagged by the key.
func WithCustomTag(key string, value interface{}) OptionFn {
	return func(cfg *config) {
		if cfg.tags == nil {
			cfg.tags = make(map[string]interface{})
		}
		cfg.tags[key] = value
	}
}

// WithSpanOptions defines a set of additional tracer.StartSpanOption to be added
// to spans started by the integration.
func WithSpanOptions(opts ...tracer.StartSpanOption) OptionFn {
	return func(cfg *config) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}
