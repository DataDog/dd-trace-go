// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package connect

import (
	"context"
	"reflect"
	"runtime"

	connectrpc "connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/DataDog/dd-trace-go/v2/contrib/connectrpc.com/connectshared"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

// traceServer traces a call that the handler next serves. unrecognized is set if next is the
// server's unknown handler (see wrapsUnknownHandler).
func (cfg *config) traceServer(ctx context.Context, spec connectrpc.Spec, unrecognized bool, stream connectrpc.ServerStream, next connectrpc.ServerFunc) (err error) {
	// call is nil when the ServerFunc is not called through connect.Server.Call, for every
	// interceptor of the chain.
	call, _ := connectrpc.CallInfoForServerContext(ctx)
	ctx, traced := cfg.core.ClaimCall(ctx, instrumentation.ComponentServer, spec.Procedure, call)
	if !traced {
		return next(ctx, spec, stream)
	}
	info := call
	if info == nil {
		info = new(connectrpc.CallInfo)
	}
	protocol, header := transportProtocol(info), info.RequestHeader()
	tagged := sharedSpec(spec)
	tagged.Unrecognized = unrecognized
	unary := spec.StreamType == connectrpc.StreamTypeUnary
	if !unary && !cfg.core.StreamCalls() {
		ctx = cfg.core.MarkHandled(ctx, instrumentation.ComponentServer, spec.Procedure, call)
		return next(ctx, spec, cfg.messageStream(ctx, tagged, stream, protocol, header))
	}
	finish := connectshared.SpanFinish{Procedure: spec.Procedure, Protocol: protocol, Mode: handlerFinishMode(ctx, protocol, unary)}
	span, ctx := cfg.core.StartServerCall(ctx, tagged, protocol, call, headerCarrier{header: header})
	var guard connectshared.Guard
	defer cfg.core.FinishOnReturn(&guard, &span, &err, &finish, nil)
	guard.Run(func() {
		cfg.core.SetMetadataTags(span, header.All())
		switch {
		case !unary:
			stream = cfg.messageStream(ctx, tagged, stream, protocol, header)
		case cfg.core.RequestTags():
			stream = &unaryServerStream{ServerStream: stream, cfg: cfg, span: span, protocol: protocol}
		}
		err = next(ctx, spec, stream)
	})
	return err
}

// handlerFinishMode returns the finish mode of a handler's call span. Only connecthttp reports an
// HTTP method: it answers a Connect GET of a unary RPC with a 304, and sends an uncoded error that
// wraps os.ErrDeadlineExceeded as CodeDeadlineExceeded.
func handlerFinishMode(ctx context.Context, protocol *connectshared.Protocol, unary bool) connectshared.FinishMode {
	httpInfo, overHTTP := connecthttp.ServerInfoForContext(ctx)
	if !unary {
		return connectshared.FinishMode{Wire: true, HTTP: overHTTP}
	}
	return connectshared.UnaryFinishMode(protocol, httpInfo.HTTPMethod(), true)
}

// messageStream returns the stream that the handler gets: stream, wrapped if its messages are
// traced.
func (cfg *config) messageStream(ctx context.Context, spec connectshared.Spec, stream connectrpc.ServerStream, protocol *connectshared.Protocol, header *connectrpc.Header) connectrpc.ServerStream {
	if !cfg.core.StreamMessages() {
		return stream
	}
	return newServerStream(ctx, cfg, stream, spec, protocol, header)
}

// unaryServerStream tags a unary handler's call span with its request. Unary RPCs have no message
// spans, so it only wraps the stream with WithRequestTags.
type unaryServerStream struct {
	connectrpc.ServerStream
	cfg      *config
	span     *tracer.Span
	protocol *connectshared.Protocol
}

func (s *unaryServerStream) Receive(message any) error {
	err := s.ServerStream.Receive(message)
	if err == nil {
		s.cfg.core.SetRequestTags(s.span, message, s.protocol)
	}
	return err
}

// serverStream traces the messages of a streaming handler. SendHeaders passes through: it starts
// no span.
type serverStream struct {
	connectrpc.ServerStream
	cfg      *config
	messages connectshared.ServerMessages
	// header is a copy of the request headers when there is no call span, taken before the handler
	// runs: message spans extract their parent from it, and the first one is tagged with its
	// metadata. Send and Receive don't read the CallInfo, as the handler may modify its headers
	// concurrently.
	header *connectrpc.Header
}

func newServerStream(ctx context.Context, cfg *config, stream connectrpc.ServerStream, spec connectshared.Spec, protocol *connectshared.Protocol, header *connectrpc.Header) *serverStream {
	s := &serverStream{ServerStream: stream, cfg: cfg}
	if !cfg.core.StreamCalls() {
		s.header = cloneHeader(header)
	}
	s.messages.Init(ctx, cfg.core, spec, protocol, headerCarrier{header: s.header})
	return s
}

func (s *serverStream) Receive(message any) (err error) {
	var span *tracer.Span
	var guard connectshared.Guard
	defer s.cfg.core.FinishOnReturn(&guard, &span, &err, s.messages.SpanFinish(), nil)
	guard.Run(func() {
		span = s.startMessageSpan()
		if err = s.ServerStream.Receive(message); err == nil {
			s.messages.TagRequest(span, message)
		}
	})
	return err
}

func (s *serverStream) Send(message any) (err error) {
	// Send(nil) only flushes the response headers.
	if message == nil {
		return s.ServerStream.Send(message)
	}
	var span *tracer.Span
	var guard connectshared.Guard
	defer s.cfg.core.FinishOnReturn(&guard, &span, &err, s.messages.SpanFinish(), nil)
	guard.Run(func() {
		span = s.startMessageSpan()
		err = s.ServerStream.Send(message)
	})
	return err
}

// startMessageSpan starts a message span, and tags the first one with the metadata when there is
// no call span to carry it.
func (s *serverStream) startMessageSpan() *tracer.Span {
	span, tagMetadata := s.messages.Start()
	if tagMetadata {
		s.cfg.core.SetMetadataTags(span, s.header.All())
	}
	return span
}

// setUnknownHandler is the name of connect.Server.SetUnknownHandler in stack frames.
var setUnknownHandler = runtime.FuncForPC(reflect.ValueOf((*connectrpc.Server).SetUnknownHandler).Pointer()).Name()

// wrapsUnknownHandler reports whether the server interceptor chain is wrapping the handler of
// connect.Server.SetUnknownHandler, which serves the procedures that the server doesn't register.
// connect passes it a Spec that names the requested procedure and is otherwise shaped like that of
// a registered bidi method without a Schema, such as one registered by hand, so only where the
// chain wraps it tells them apart. It reads the whole stack, however deep the interceptors that
// wrap this one nest it, and allocates only for a stack deeper than 32 frames.
func wrapsUnknownHandler() bool {
	var buf [32]uintptr
	pcs := buf[:]
	n := runtime.Callers(3, pcs)
	for n == len(pcs) {
		pcs = make([]uintptr, 2*len(pcs))
		n = runtime.Callers(3, pcs)
	}
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if frame.Function == setUnknownHandler {
			return true
		}
		if !more {
			return false
		}
	}
}
