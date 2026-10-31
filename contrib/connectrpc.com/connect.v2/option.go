// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	connectrpc "connectrpc.com/connect/v2"

	"github.com/DataDog/dd-trace-go/v2/contrib/connectrpc.com/connectshared"
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
	core *connectshared.Config
}

func newConfig(opts ...Option) *config {
	cfg := &config{core: connectshared.NewConfig(instr, instrumentation.PackageConnectRPCV2, &library)}
	for _, opt := range opts {
		opt.apply(cfg)
	}
	cfg.core.Init()
	return cfg
}

// WithService sets the service name for spans created by the interceptor.
func WithService(name string) OptionFn {
	return func(cfg *config) {
		cfg.core.SetService(name)
	}
}

// WithStreamCalls enables or disables call spans for streaming RPCs. Enabled by default.
func WithStreamCalls(enabled bool) OptionFn {
	return func(cfg *config) {
		cfg.core.SetStreamCalls(enabled)
	}
}

// WithStreamMessages enables or disables a span per streamed message. Enabled by default.
// Finished message spans are held in memory until their call span finishes, and the tracer drops
// traces of more than 100,000 spans: for streams with many messages, disable message spans or
// enable partial flushing (DD_TRACE_PARTIAL_FLUSH_ENABLED).
func WithStreamMessages(enabled bool) OptionFn {
	return func(cfg *config) {
		cfg.core.SetStreamMessages(enabled)
	}
}

// NoDebugStack disables stack traces for errors.
func NoDebugStack() OptionFn {
	return func(cfg *config) {
		cfg.core.SetNoDebugStack()
	}
}

// NonErrorCodes replaces the set of codes that are not recorded as errors; the default is
// CodeCanceled. Regardless of this set, an uncoded context.Canceled is never an error, nor, on
// stream messages, is connect's normal end of stream (io.EOF, which connecthttp wraps with
// CodeUnknown). Any other explicit code takes precedence over the error it wraps. On servers, a
// remote error that a handler returns, such as the error of a call it made, has the code the
// transports send for it, CodeInternal.
func NonErrorCodes(codes ...connectrpc.Code) OptionFn {
	return func(cfg *config) {
		connectshared.SetNonErrorCodes(cfg.core, codes)
	}
}

// WithErrorCheck sets fn to decide whether an RPC error that the NonErrorCodes rules did not
// already suppress is recorded on the span. If fn returns false, the status tags are kept but the
// error is not recorded. fn must be a pure classifier: it may be called concurrently and more than
// once per RPC, and must not call methods on the stream being traced.
func WithErrorCheck(fn func(procedure string, err error) (isError bool)) OptionFn {
	return func(cfg *config) {
		cfg.core.SetErrorCheck(fn)
	}
}

// WithUntracedMethods specifies procedures, such as "/acme.ping.v1.PingService/Ping", for which
// no spans are created.
func WithUntracedMethods(methods ...string) OptionFn {
	untraced := connectshared.MethodSet(methods...)
	return func(cfg *config) {
		cfg.core.SetUntracedMethods(untraced)
	}
}

// WithMetadataTags tags spans with the request metadata (headers) as
// "rpc.request.metadata.<key>". Unary RPCs tag their call span. On streams, the call span is tagged
// or, with WithStreamCalls(false), a single message span: on servers the first one, on clients the
// one of the first Send or, if it has none, the next one. Clients report the metadata set on the
// CallInfo's request header as of their first SendHeaders, Send or CloseSend, when connect sends
// it; servers report every request header the transport received. Binary ("-bin") keys and common
// propagation and credential keys are excluded; use WithIgnoredMetadata for application-specific
// sensitive keys.
func WithMetadataTags() OptionFn {
	return func(cfg *config) {
		cfg.core.EnableMetadataTags()
	}
}

// WithIgnoredMetadata adds case-insensitive metadata keys to exclude from WithMetadataTags.
func WithIgnoredMetadata(keys ...string) OptionFn {
	return func(cfg *config) {
		cfg.core.IgnoreMetadata(keys...)
	}
}

// WithRequestTags tags spans with the JSON encoding of protobuf request messages: the call span of
// unary RPCs, and the message spans of streams.
func WithRequestTags() OptionFn {
	return func(cfg *config) {
		cfg.core.EnableRequestTags()
	}
}

// WithErrorDetailTags tags spans with the protobuf details of recorded errors.
func WithErrorDetailTags() OptionFn {
	return func(cfg *config) {
		cfg.core.EnableErrorDetailTags()
	}
}

// WithCustomTag adds a tag to spans created by the interceptor. Custom tags take precedence over
// the tags the interceptor sets when a span starts, including those from WithSpanOptions, but not
// over the tags it sets afterwards: network.destination.*, rpc.system and rpc.grpc.full_method on
// client call spans, network.destination.* on client message spans, span.kind on message spans
// that are local roots, the request and metadata tags, and the status and error tags set when a
// span finishes. Set the service with WithService; an ext.ServiceName tag set here takes
// precedence over it.
func WithCustomTag(key string, value any) OptionFn {
	return func(cfg *config) {
		cfg.core.AddCustomTag(key, value)
	}
}

// WithSpanOptions adds options to spans created by the interceptor. They are applied to every
// span as it starts and take precedence over the interceptor's own start options, but not over the
// tags it sets afterwards (see WithCustomTag). Set the service with WithService; a service set
// here takes precedence over it.
func WithSpanOptions(opts ...tracer.StartSpanOption) OptionFn {
	return func(cfg *config) {
		cfg.core.AddSpanOptions(opts...)
	}
}
