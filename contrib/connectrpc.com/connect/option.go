// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"strings"
	"sync/atomic"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
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
	// service is set by WithService, or lazily by resolveService.
	service             atomic.Pointer[resolvedService]
	nonErrorCodes       map[connectrpc.Code]bool
	errCheck            func(procedure string, err error) bool
	traceStreamCalls    bool
	traceStreamMessages bool
	noDebugStack        bool
	untracedMethods     map[string]struct{}
	withMetadataTags    bool
	ignoredMetadata     map[string]struct{}
	withRequestTags     bool
	withErrorDetailTags bool
	analyticsRate       float64
	spanOpts            []tracer.StartSpanOption
	tags                map[string]any

	// Built by newConfig once every Option has been applied; read-only afterwards.
	client, server spanBases
	customTags     tracer.StartSpanOption
}

func defaults(cfg *config) {
	cfg.nonErrorCodes = map[connectrpc.Code]bool{connectrpc.CodeCanceled: true}
	cfg.traceStreamCalls = true
	cfg.traceStreamMessages = true
	cfg.analyticsRate = instr.AnalyticsRate(false)
	cfg.ignoredMetadata = map[string]struct{}{
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
	}
}

func newConfig(opts ...Option) *config {
	cfg := new(config)
	defaults(cfg)
	for _, opt := range opts {
		opt.apply(cfg)
	}
	cfg.client = newSpanBases(instrumentation.ComponentClient, cfg.analyticsRate)
	cfg.server = newSpanBases(instrumentation.ComponentServer, cfg.analyticsRate)
	if len(cfg.tags) > 0 {
		cfg.customTags = tracer.WithTags(cfg.tags)
	}
	return cfg
}

// resolvedService is an immutable service name paired with its prebuilt start option.
type resolvedService struct {
	name string
	opt  tracer.StartSpanOption
}

// newResolvedService builds the same option as instrumentation.ServiceNameWithSource, without
// allocating on every span.
func newResolvedService(name, source string) *resolvedService {
	override := instrumentation.ServiceOverride{Name: name, Source: source}
	return &resolvedService{name: name, opt: tracer.Tag(ext.KeyServiceSource, override)}
}

// resolveService returns the WithService value or the default (DD_SERVICE). The default is only
// cached once the tracer is initialized: before tracer.Start, which Orchestrion runs after
// package-level clients and handlers are built, it is still "".
func (cfg *config) resolveService() *resolvedService {
	if svc := cfg.service.Load(); svc != nil {
		return svc
	}
	name, source := instr.ServiceName(instrumentation.ComponentDefault, nil), string(instrumentation.PackageConnectRPC)
	if name == "" {
		// The tracer's own default service is not an override.
		source = ""
	}
	svc := newResolvedService(name, source)
	if instr.TracerInitialized() {
		cfg.service.Store(svc)
	}
	return svc
}

// WithService sets the service name for spans created by the interceptor.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.service.Store(newResolvedService(name, instrumentation.ServiceSourceWithServiceOption))
	}
}

// WithStreamCalls enables or disables call spans for streaming RPCs. Enabled by default.
func WithStreamCalls(enabled bool) OptionFn {
	return func(cfg *config) {
		cfg.traceStreamCalls = enabled
	}
}

// WithStreamMessages enables or disables a span per streamed message. Enabled by default.
// Finished message spans are held in memory until their call span finishes, and the tracer drops
// traces of more than 100,000 spans: for streams with many messages, disable message spans or
// enable partial flushing (DD_TRACE_PARTIAL_FLUSH_ENABLED).
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

// NonErrorCodes replaces the set of codes that are not recorded as errors; the default is
// CodeCanceled. Regardless of this set, an uncoded context.Canceled is never an error, nor, on
// stream messages, is connect's normal end of stream (io.EOF, which connect wraps with
// CodeUnknown). Any other explicit code takes precedence over the error it wraps.
func NonErrorCodes(codes ...connectrpc.Code) OptionFn {
	return func(cfg *config) {
		cfg.nonErrorCodes = make(map[connectrpc.Code]bool, len(codes))
		for _, code := range codes {
			cfg.nonErrorCodes[code] = true
		}
	}
}

// WithErrorCheck sets fn to decide whether an RPC error that the NonErrorCodes rules did not
// already suppress is recorded on the span. If fn returns false, the status tags are kept but the
// error is not recorded. fn must be a pure classifier: it may be called concurrently and more than
// once per RPC, and must not call methods on the stream or connection being traced.
func WithErrorCheck(fn func(procedure string, err error) (isError bool)) OptionFn {
	return func(cfg *config) {
		cfg.errCheck = fn
	}
}

// WithUntracedMethods specifies procedures, such as "/acme.ping.v1.PingService/Ping", for which
// no spans are created.
func WithUntracedMethods(methods ...string) OptionFn {
	untraced := make(map[string]struct{}, len(methods))
	for _, method := range methods {
		untraced[method] = struct{}{}
	}
	return func(cfg *config) {
		cfg.untracedMethods = untraced
	}
}

// WithMetadataTags tags spans with the request metadata (headers) as
// "rpc.request.metadata.<key>". On streams, the call span is tagged or, with WithStreamCalls(false),
// a single message span: on handlers the first one, on clients the one of the first Send or, if it
// has none, the next one. Client streams report the metadata as of their first Send or
// CloseRequest, when connect sends it. Binary ("-bin") keys and common propagation and credential
// keys are excluded; use WithIgnoredMetadata for application-specific sensitive keys.
func WithMetadataTags() OptionFn {
	return func(cfg *config) {
		cfg.withMetadataTags = true
	}
}

// WithIgnoredMetadata adds case-insensitive metadata keys to exclude from WithMetadataTags.
func WithIgnoredMetadata(keys ...string) OptionFn {
	return func(cfg *config) {
		for _, key := range keys {
			cfg.ignoredMetadata[strings.ToLower(key)] = struct{}{}
		}
	}
}

// WithRequestTags tags spans with the JSON encoding of protobuf request messages.
func WithRequestTags() OptionFn {
	return func(cfg *config) {
		cfg.withRequestTags = true
	}
}

// WithErrorDetailTags tags spans with the protobuf details of recorded errors.
func WithErrorDetailTags() OptionFn {
	return func(cfg *config) {
		cfg.withErrorDetailTags = true
	}
}

// WithCustomTag adds a tag to spans created by the interceptor. Custom tags take precedence over
// the tags the interceptor sets when a span starts, including those from WithSpanOptions, but not
// over the tags it sets afterwards: network.destination.* on client spans, rpc.system and
// rpc.grpc.full_method on clients' streaming call spans, span.kind on message spans that are local
// roots, the request and metadata tags, and the status and error tags set when a span finishes.
// Set the service with WithService; an ext.ServiceName tag set here takes precedence over it.
func WithCustomTag(key string, value any) OptionFn {
	return func(cfg *config) {
		if cfg.tags == nil {
			cfg.tags = make(map[string]any)
		}
		cfg.tags[key] = value
	}
}

// WithSpanOptions adds options to spans created by the interceptor. They are applied to every
// span as it starts and take precedence over the interceptor's own start options, but not over the
// tags it sets afterwards (see WithCustomTag). Set the service with WithService; a service set
// here takes precedence over it.
func WithSpanOptions(opts ...tracer.StartSpanOption) OptionFn {
	return func(cfg *config) {
		cfg.spanOpts = append(cfg.spanOpts, opts...)
	}
}
