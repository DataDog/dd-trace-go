// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"math"
	"strings"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// Option describes an option for the Connect integration.
type Option interface {
	apply(*config)
}

// OptionFn implements Option.
type OptionFn func(*config)

func (fn OptionFn) apply(cfg *config) {
	fn(cfg)
}

type config struct {
	serviceName         string
	serviceSource       string
	nonErrorCodes       map[connectrpc.Code]bool
	errCheck            func(procedure string, err error) bool
	traceStreamCalls    bool
	traceStreamMessages bool
	noDebugStack        bool
	untracedMethods     map[string]struct{}
	withHeaderTags      bool
	ignoredHeaders      map[string]struct{}
	withRequestTags     bool
	withErrorDetailTags bool
	analyticsRate       float64
	spanOpts            []tracer.StartSpanOption
	tags                map[string]any
}

func newConfig(opts ...Option) *config {
	cfg := &config{
		nonErrorCodes:       map[connectrpc.Code]bool{connectrpc.CodeCanceled: true},
		traceStreamCalls:    true,
		traceStreamMessages: true,
		analyticsRate:       instr.AnalyticsRate(false),
		ignoredHeaders: map[string]struct{}{
			"authorization":               {},
			"baggage":                     {},
			"b3":                          {},
			"cookie":                      {},
			"proxy-authorization":         {},
			"set-cookie":                  {},
			"traceparent":                 {},
			"tracestate":                  {},
			"x-api-key":                   {},
			"x-auth-token":                {},
			"x-b3-flags":                  {},
			"x-b3-parentspanid":           {},
			"x-b3-sampled":                {},
			"x-b3-spanid":                 {},
			"x-b3-traceid":                {},
			"x-datadog-origin":            {},
			"x-datadog-parent-id":         {},
			"x-datadog-sampling-priority": {},
			"x-datadog-tags":              {},
			"x-datadog-trace-id":          {},
		},
	}
	for _, opt := range opts {
		opt.apply(cfg)
	}
	return cfg
}

func (cfg *config) service(component instrumentation.Component) (name, source string) {
	if cfg.serviceName != "" {
		return cfg.serviceName, cfg.serviceSource
	}
	return instr.ServiceName(component, nil), string(instrumentation.PackageConnectRPC)
}

// WithService sets the service name for spans created by the interceptor.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.serviceName = name
		cfg.serviceSource = instrumentation.ServiceSourceWithServiceOption
	}
}

// WithStreamCalls enables or disables tracing streaming calls.
func WithStreamCalls(enabled bool) OptionFn {
	return func(cfg *config) {
		cfg.traceStreamCalls = enabled
	}
}

// WithStreamMessages enables or disables tracing individual streaming messages.
func WithStreamMessages(enabled bool) OptionFn {
	return func(cfg *config) {
		cfg.traceStreamMessages = enabled
	}
}

// NoDebugStack disables stack traces for errors.
func NoDebugStack() OptionFn {
	return func(cfg *config) {
		cfg.noDebugStack = true
	}
}

// NonErrorCodes replaces the Connect codes that are not marked as errors.
// By default, CodeCanceled is not marked as an error. Stream EOF and errors
// wrapping context.Canceled are always treated as normal stream termination.
func NonErrorCodes(codes ...connectrpc.Code) OptionFn {
	return func(cfg *config) {
		cfg.nonErrorCodes = make(map[connectrpc.Code]bool, len(codes))
		for _, code := range codes {
			cfg.nonErrorCodes[code] = true
		}
	}
}

// WithErrorCheck sets a function fn which determines whether the passed error should be
// marked as an error. fn is called with the Connect RPC procedure and the error whenever
// an RPC finishes with a non-nil error. If fn returns false, the error is not recorded on
// the span.
func WithErrorCheck(fn func(procedure string, err error) (isError bool)) OptionFn {
	return func(cfg *config) {
		cfg.errCheck = fn
	}
}

// WithAnalytics enables or disables Trace Analytics.
func WithAnalytics(enabled bool) OptionFn {
	return func(cfg *config) {
		cfg.analyticsRate = math.NaN()
		if enabled {
			cfg.analyticsRate = 1
		}
	}
}

// WithAnalyticsRate sets the Trace Analytics sampling rate.
func WithAnalyticsRate(rate float64) OptionFn {
	return func(cfg *config) {
		if rate >= 0 && rate <= 1 {
			cfg.analyticsRate = rate
		}
	}
}

// WithUntracedMethods specifies procedures for which no spans are created.
func WithUntracedMethods(methods ...string) OptionFn {
	untraced := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		untraced[method] = struct{}{}
	}
	return func(cfg *config) {
		cfg.untracedMethods = untraced
	}
}

// WithHeaderTags enables request header tags. Common propagation and credential-bearing
// headers, along with binary headers, are excluded. Use WithIgnoredHeaders for any
// application-specific sensitive headers.
func WithHeaderTags() OptionFn {
	return func(cfg *config) {
		cfg.withHeaderTags = true
	}
}

// WithIgnoredHeaders adds request headers to exclude when WithHeaderTags is enabled.
func WithIgnoredHeaders(headers ...string) OptionFn {
	return func(cfg *config) {
		for _, header := range headers {
			cfg.ignoredHeaders[strings.ToLower(header)] = struct{}{}
		}
	}
}

// WithRequestTags enables protobuf request payload tags.
func WithRequestTags() OptionFn {
	return func(cfg *config) {
		cfg.withRequestTags = true
	}
}

// WithErrorDetailTags enables protobuf error detail tags.
func WithErrorDetailTags() OptionFn {
	return func(cfg *config) {
		cfg.withErrorDetailTags = true
	}
}

// WithCustomTag adds a tag to spans created by the interceptor.
func WithCustomTag(key string, value any) OptionFn {
	return func(cfg *config) {
		if cfg.tags == nil {
			cfg.tags = make(map[string]any)
		}
		cfg.tags[key] = value
	}
}

// WithSpanOptions adds options to spans created by the interceptor.
func WithSpanOptions(opts ...tracer.StartSpanOption) OptionFn {
	return func(cfg *config) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}
