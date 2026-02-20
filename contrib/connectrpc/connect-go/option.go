// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"math"

	"connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// Option describes options for the Connect RPC integration.
type Option interface {
	apply(*config)
}

// OptionFn represents options applicable to NewInterceptor.
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
	if instr.TracerInitialized() {
		cs.value = svc
	}
	return svc
}

type config struct {
	serviceName         *cachedServiceName
	nonErrorCodes       map[connect.Code]bool
	traceStreamCalls    bool
	traceStreamMessages bool
	noDebugStack        bool
	untracedMethods     map[string]struct{}
	withHeaderTags      bool
	ignoredHeaders      map[string]struct{}
	withRequestTags     bool
	spanOpts            []tracer.StartSpanOption
	tags                map[string]interface{}
}

func defaults(cfg *config) {
	cfg.traceStreamCalls = true
	cfg.traceStreamMessages = true
	cfg.nonErrorCodes = map[connect.Code]bool{connect.CodeCanceled: true}
	if rate := instr.AnalyticsRate(false); !math.IsNaN(rate) {
		cfg.spanOpts = append(cfg.spanOpts, tracer.AnalyticsRate(rate))
	}
	cfg.ignoredHeaders = map[string]struct{}{
		"x-datadog-trace-id":          {},
		"x-datadog-parent-id":         {},
		"x-datadog-sampling-priority": {},
	}
}

func (cfg *config) startSpanOptions(opts ...tracer.StartSpanOption) []tracer.StartSpanOption {
	if len(cfg.tags) == 0 && len(cfg.spanOpts) == 0 {
		return opts
	}
	ret := make([]tracer.StartSpanOption, 0, 1+len(cfg.tags)+len(opts))
	for _, opt := range opts {
		ret = append(ret, opt)
	}
	for _, opt := range cfg.spanOpts {
		ret = append(ret, opt)
	}
	for key, tag := range cfg.tags {
		ret = append(ret, tracer.Tag(key, tag))
	}
	return ret
}

func (cfg *config) serviceNameForComponent(component instrumentation.Component) string {
	if cfg.serviceName != nil {
		return cfg.serviceName.String()
	}
	return instr.ServiceName(component, nil)
}

func (cfg *config) operationNameForComponent(component instrumentation.Component) string {
	return instr.OperationName(component, nil)
}

// WithService sets the given service name for the interceptor.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = newCachedServiceName(func() string {
			return name
		})
	}
}

// WithStreamCalls enables or disables tracing of streaming calls.
func WithStreamCalls(enabled bool) OptionFn {
	return func(cfg *config) {
		cfg.traceStreamCalls = enabled
	}
}

// WithStreamMessages enables or disables tracing of streaming messages.
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
// This call overrides the default handling of CodeCanceled as a non-error.
func NonErrorCodes(cs ...connect.Code) OptionFn {
	return func(cfg *config) {
		cfg.nonErrorCodes = make(map[connect.Code]bool, len(cs))
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

// WithUntracedMethods specifies procedures to be ignored by the interceptor.
// When a request's procedure is in ms, no spans will be created.
func WithUntracedMethods(ms ...string) OptionFn {
	ums := make(map[string]struct{}, len(ms))
	for _, e := range ms {
		ums[e] = struct{}{}
	}
	return func(cfg *config) {
		cfg.untracedMethods = ums
	}
}

// WithHeaderTags specifies whether Connect RPC request headers should be added to spans as tags.
func WithHeaderTags() OptionFn {
	return func(cfg *config) {
		cfg.withHeaderTags = true
	}
}

// WithIgnoredHeaders specifies header keys to be ignored while tracing headers. Must be used
// in conjunction with WithHeaderTags.
func WithIgnoredHeaders(ms ...string) OptionFn {
	return func(cfg *config) {
		for _, e := range ms {
			cfg.ignoredHeaders[e] = struct{}{}
		}
	}
}

// WithRequestTags specifies whether Connect RPC requests should be added to spans as tags.
func WithRequestTags() OptionFn {
	return func(cfg *config) {
		cfg.withRequestTags = true
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
