// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package connect provides tracing for connectrpc.com/connect/v2 clients and servers.
//
// The interceptors created by NewClientInterceptor and NewServerInterceptor start a
// "connect.client" or "connect.server" span for every RPC and, for streaming RPCs, a
// "connect.message" span for every message sent or received (see WithStreamCalls and
// WithStreamMessages). The rpc.system tag is "grpc" for both gRPC and gRPC-Web, and "connectrpc"
// for the Connect protocol and for transports without a wire protocol, such as connectinprocess.
// A client call span starts before the transport opens the stream, so its rpc.system and peer tags
// are set after it starts. A call that no transport carried, because opening its stream failed or
// panicked before one did or because an interceptor returned a stream of its own, has none of the
// rpc.system, rpc.grpc.full_method, rpc.connect_rpc.error_code and rpc.grpc.status_code tags, so
// the tracer derives no peer.service from its rpc.service; its error is still recorded.
//
// A client interceptor injects the trace headers into the CallInfo's request header once the
// interceptors and transport that it wraps have opened the stream: those that send or copy the
// request header while opening it don't propagate the trace. A call that they make to the same
// procedure with the context they are given shares the traced call's CallInfo, and is part of its
// span rather than traced on its own. Through a CallInfo reused across calls (see
// connect.NewClientContext), a call removes the trace headers that an earlier call injected, before
// injecting its own if it is traced. The headers that the application set are kept, unless an
// injection writes the same keys.
//
// A server interceptor passed to connect.NewServer traces the calls routed to the unknown handler
// (see connect.Server.SetUnknownHandler) as bidi_streaming RPCs of an unrecognized method: the
// client chose their procedure, so their spans have the resource and rpc.method "_OTHER", as
// OpenTelemetry names such a method, and the requested method as rpc.method_original. So does a
// server interceptor that another one passed to connect.NewServer applies while SetUnknownHandler
// wraps the handler, but not one applied later, such as on the first call. Requests that the
// transport rejects before the interceptor chain runs, such as connecthttp requests with an
// unsupported HTTP method, content type or compression, or an invalid timeout, have no server
// span.
//
// A client call span finishes once the RPC has reached a terminal state and no other stream method
// is still running. The terminal states are Close; any Receive error (including io.EOF); the first
// Receive of a unary or client-streaming RPC, which reads its only response; any CloseSend error;
// a SendHeaders or Send error other than io.EOF (after which Receive reports the server's error),
// or any such error on a unary or server-streaming RPC, whose caller abandons the stream; a failure
// to open the stream; a panic in a stream method; and the end of the RPC's context, whose error is
// not recorded if a Receive that was running when it ended returns the end of the stream or the
// RPC's only response. Close or drain client streams, or cancel their context, as connect requires,
// or their spans are never finished. Close abandons a stream: the error of a Receive that returns
// once Close has been called is not recorded, unless that Receive was running when Close was called
// and returns the server's verdict: an error whose first *connect.Error, as errors.As finds it,
// reports IsRemote. So any other error that races Close is not recorded, even if the Receive read
// it before Close tore the stream down: for example, connecthttp's error for a response whose HTTP
// status rejects the call, as a proxy's or a load balancer's may. A caller that reads a client-streaming
// RPC past its only response, which connectinprocess doesn't fail, gets results that the call span
// doesn't record: the generated CloseAndReceive only reads the first.
//
// Finished message spans are held in memory until their call span finishes, and the tracer drops
// traces of more than 100,000 spans. For streams with many messages, use WithStreamMessages(false)
// or enable partial flushing (DD_TRACE_PARTIAL_FLUSH_ENABLED).
//
// With Orchestrion, every client built with connect.NewClient and every server built with
// connect.NewServer is instrumented automatically, including generated clients and handlers.
package connect

import (
	"context"
	"errors"
	"slices"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/connect/v2/connectproto"

	"github.com/DataDog/dd-trace-go/v2/contrib/connectrpc.com/connectshared"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(instrumentation.PackageConnectRPCV2)
}

// NewClientInterceptor returns an interceptor that traces clients.
func NewClientInterceptor(opts ...Option) connectrpc.ClientInterceptor {
	cfg := newInterceptorConfig(opts...)
	return func(next connectrpc.ClientFunc) connectrpc.ClientFunc {
		return func(ctx context.Context, spec connectrpc.Spec) (connectrpc.ClientStream, error) {
			return cfg.traceClient(ctx, spec, next)
		}
	}
}

// NewServerInterceptor returns an interceptor that traces servers.
func NewServerInterceptor(opts ...Option) connectrpc.ServerInterceptor {
	cfg := newInterceptorConfig(opts...)
	return func(next connectrpc.ServerFunc) connectrpc.ServerFunc {
		unrecognized := wrapsUnknownHandler()
		return func(ctx context.Context, spec connectrpc.Spec, stream connectrpc.ServerStream) error {
			return cfg.traceServer(ctx, spec, unrecognized, stream, next)
		}
	}
}

func newInterceptorConfig(opts ...Option) *config {
	cfg := newConfig(opts...)
	instr.Logger().Debug("contrib/connectrpc.com/connect.v2: configuring interceptor: %#v", cfg.core)
	return cfg
}

// sharedSpec returns the part of spec that spans are tagged with.
func sharedSpec(spec connectrpc.Spec) connectshared.Spec {
	return connectshared.Spec{Procedure: spec.Procedure, StreamType: connectshared.StreamType(spec.StreamType)}
}

var messageFinishMode = connectshared.FinishMode{AllowEOF: true}

// transportProtocol returns the protocol of the call that info describes. A transport fills in the
// Spec of a call that it carries, and its Protocol unless it has no wire protocol, like
// connectinprocess. Any other call, such as one whose stream an interceptor returned itself, has an
// unknown protocol.
func transportProtocol(info *connectrpc.CallInfo) *connectshared.Protocol {
	if info.Protocol == "" && info.Spec.Procedure != "" {
		return connectshared.InProcessProtocol()
	}
	return connectshared.ProtocolFor(info.Protocol)
}

// library describes connect's errors. Code and IsRemote report a nil *connectrpc.Error as
// CodeUnknown and not remote.
var library = connectshared.Library{
	InspectError: func(err error) connectshared.ErrorInfo {
		connectErr, ok := errors.AsType[*connectrpc.Error](err)
		if !ok {
			return connectshared.ErrorInfo{}
		}
		return connectshared.ErrorInfo{Coded: true, Nil: connectErr == nil, Code: connectshared.Code(connectErr.Code()), Remote: connectErr.IsRemote()}
	},
	IsNotModified: connecthttp.IsNotModifiedError,
	ErrorDetails: func(err error, tags connectshared.ErrorDetailTags) {
		if connectErr, ok := errors.AsType[*connectrpc.Error](err); ok && connectErr != nil {
			connectshared.TagErrorDetails(tags, connectErr.Details(), connectproto.UnmarshalErrorDetail)
		}
	},
}

// cloneHeader copies header, keys and value slices: connect.Header has no Clone method.
func cloneHeader(header *connectrpc.Header) *connectrpc.Header {
	clone := new(connectrpc.Header)
	for key, values := range header.All() {
		clone.SetValues(key, slices.Clone(values))
	}
	return clone
}

// headerSnapshot copies header's keys and value slices into a map shaped like an http.Header.
func headerSnapshot(header *connectrpc.Header) map[string][]string {
	snapshot := make(map[string][]string, header.Len())
	for key, values := range header.All() {
		snapshot[key] = slices.Clone(values)
	}
	return snapshot
}

// headerCarrier adapts a connect header to tracer.TextMapWriter and tracer.TextMapReader.
type headerCarrier struct {
	header *connectrpc.Header
}

// Set replaces the values of key with value, marked as injected (see clearInjected), so that
// injecting into the header of a reused CallInfo doesn't duplicate the trace headers.
func (c headerCarrier) Set(key, value string) {
	values := make([]string, 1, 2)
	values[0] = value
	values[:2][1] = injectedMark
	c.header.SetValues(key, values)
}

// injectedMark follows, past their length, the values that headerCarrier.Set writes. connect keeps
// the request header of a reused CallInfo, and connect.Header keeps the slices that SetValues is
// given and yields them from All: the mark tells the trace headers that an earlier call injected
// from those of the application. The application's Set replaces a marked slice, and its Add
// overwrites the mark.
const injectedMark = "\x00dd-trace-go:injected"

// isInjected reports whether values are marked as written by headerCarrier.Set.
func isInjected(values []string) bool {
	return len(values) == 1 && cap(values) == 2 && values[:2][1] == injectedMark
}

// clearInjected removes the trace headers that an earlier call injected into header and that the
// application hasn't changed since. An injection only replaces the keys that it writes: without
// this, a call through a reused CallInfo would send those that an earlier call wrote and it
// doesn't, such as x-datadog-origin or baggage, or all of them if it injects nothing.
func clearInjected(header *connectrpc.Header) {
	if header.Len() == 0 {
		return
	}
	for key, values := range header.All() {
		if isInjected(values) {
			header.Delete(key)
		}
	}
}

// ForeachKey calls handler with every value of every key, like tracer.HTTPHeadersCarrier.
func (c headerCarrier) ForeachKey(handler func(key, value string) error) error {
	for key, values := range c.header.All() {
		for _, value := range values {
			if err := handler(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}

// inject replaces the trace headers that an earlier call injected into header with those of the
// span of ctx, if any.
func (cfg *config) inject(ctx context.Context, header *connectrpc.Header) {
	clearInjected(header)
	cfg.core.Inject(ctx, headerCarrier{header: header})
}
