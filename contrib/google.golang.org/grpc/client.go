// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"
	"net"
	"sync/atomic"

	"github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2/internal/grpcutil"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

type clientStream struct {
	grpc.ClientStream
	ctx    context.Context
	cfg    *config
	method string
	// committed records that the stream attempt is committed. Reading the
	// transport context is safe only after Header or RecvMsg has returned,
	// because an earlier read commits the attempt and disables transparent
	// retries. Context, Header and RecvMsg set it after the underlying call
	// returned, and the interceptor sets it at construction when call tracing
	// already read the transport context at stream creation, which commits
	// the attempt as well.
	committed atomic.Bool
}

// Context returns the transport stream context with the stream call span
// attached to it. gRPC attaches values such as the transport peer to the
// transport context only, so callers must receive the transport context
// and not the interceptor context. The span stays on the returned context
// so that code which wraps this stream can parent new spans to the stream
// call span. The merge happens lazily on each call. Calling
// cs.ClientStream.Context() at stream creation would commit the stream
// attempt and disable transparent retries (issue #4757).
func (cs *clientStream) Context() context.Context {
	sctx := cs.ClientStream.Context()
	// The call above committed the stream attempt.
	cs.committed.Store(true)
	if span, ok := tracer.SpanFromContext(cs.ctx); ok {
		return tracer.ContextWithSpan(sctx, span)
	}
	return sctx
}

// Header returns the headers of the stream. The underlying call commits the
// stream attempt, so record the commit the same way Context does.
func (cs *clientStream) Header() (metadata.MD, error) {
	md, err := cs.ClientStream.Header()
	cs.committed.Store(true)
	return md, err
}

func (cs *clientStream) RecvMsg(m interface{}) (err error) {
	var span *tracer.Span
	if _, ok := cs.cfg.untracedMethods[cs.method]; cs.cfg.traceStreamMessages && !ok {
		span, _ = startSpanFromContext(
			cs.ctx,
			cs.method,
			"grpc.message",
			cs.cfg.serviceName.String(),
			cs.cfg.serviceSource,
			cs.cfg.startSpanOptions()...,
		)
		span.SetTag(ext.Component, componentName)
		defer func() { finishWithError(span, err, cs.method, cs.cfg) }()
	}
	err = cs.ClientStream.RecvMsg(m)
	if span != nil {
		// RecvMsg has returned, so the retry window is closed and reading the
		// transport context no longer disables retries.
		cs.committed.Store(true)
		if p, ok := peer.FromContext(cs.ClientStream.Context()); ok {
			setSpanTargetFromPeer(span, *p)
		}
	}
	return err
}

func (cs *clientStream) SendMsg(m interface{}) (err error) {
	var span *tracer.Span
	if _, ok := cs.cfg.untracedMethods[cs.method]; cs.cfg.traceStreamMessages && !ok {
		span, _ = startSpanFromContext(
			cs.ctx,
			cs.method,
			"grpc.message",
			cs.cfg.serviceName.String(),
			cs.cfg.serviceSource,
			cs.cfg.startSpanOptions()...,
		)
		span.SetTag(ext.Component, componentName)
		defer func() { finishWithError(span, err, cs.method, cs.cfg) }()
	}
	err = cs.ClientStream.SendMsg(m)
	// SendMsg does not close the retry window, so read the transport context
	// only once the attempt is already committed.
	if span != nil && cs.committed.Load() {
		if p, ok := peer.FromContext(cs.ClientStream.Context()); ok {
			setSpanTargetFromPeer(span, *p)
		}
	}
	return err
}

// StreamClientInterceptor returns a grpc.StreamClientInterceptor which will trace client
// streams using the given set of options.
func StreamClientInterceptor(opts ...Option) grpc.StreamClientInterceptor {
	cfg := new(config)
	clientDefaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	instr.Logger().Debug("contrib/google.golang.org/grpc: Configuring StreamClientInterceptor: %#v", cfg)
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		var methodKind string
		if desc != nil {
			switch {
			case desc.ServerStreams && desc.ClientStreams:
				methodKind = methodKindBidiStream
			case desc.ServerStreams:
				methodKind = methodKindServerStream
			case desc.ClientStreams:
				methodKind = methodKindClientStream
			}
		}
		var stream grpc.ClientStream
		var committed bool
		if _, ok := cfg.untracedMethods[method]; cfg.traceStreamCalls && !ok {
			var (
				span *tracer.Span
				err  error
			)
			span, ctx, err = doClientRequest(ctx, cfg, method, methodKind, cc, opts,
				func(ctx context.Context, opts []grpc.CallOption) error {
					var err error
					stream, err = streamer(ctx, desc, cc, method, opts...)
					return err
				})
			if err != nil {
				finishWithError(span, err, method, cfg)
				return nil, err
			}

			// the Peer call option only works with unary calls, so for streams
			// we need to set it via FromContext
			if p, ok := peer.FromContext(stream.Context()); ok {
				setSpanTargetFromPeer(span, *p)
			}
			// The read above committed the stream attempt (#4757), so message
			// spans may read the transport context from now on.
			committed = true

			go func() {
				<-stream.Context().Done()
				finishWithError(span, stream.Context().Err(), method, cfg)
			}()
		} else {
			// if call tracing is disabled, just call streamer, but still return
			// a clientStream so that messages can be traced if enabled

			// it's possible there's already a span on the context even though
			// we're not tracing calls, so inject it if it's there
			ctx = injectSpanIntoContext(ctx)

			var err error
			stream, err = streamer(ctx, desc, cc, method, opts...)
			if err != nil {
				return nil, err
			}
		}
		sc := &clientStream{
			ClientStream: stream,
			cfg:          cfg,
			method:       method,
			ctx:          ctx,
		}
		if committed {
			sc.committed.Store(true)
		}
		return sc, nil
	}
}

// UnaryClientInterceptor returns a grpc.UnaryClientInterceptor which will trace requests using
// the given set of options.
func UnaryClientInterceptor(opts ...Option) grpc.UnaryClientInterceptor {
	cfg := new(config)
	clientDefaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	instr.Logger().Debug("contrib/google.golang.org/grpc: Configuring UnaryClientInterceptor: %#v", cfg)
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		if _, ok := cfg.untracedMethods[method]; ok {
			return invoker(ctx, method, req, reply, cc, opts...)
		}
		span, _, err := doClientRequest(ctx, cfg, method, methodKindUnary, cc, opts,
			func(ctx context.Context, opts []grpc.CallOption) error {
				return invoker(ctx, method, req, reply, cc, opts...)
			})
		finishWithError(span, err, method, cfg)
		return err
	}
}

// doClientRequest starts a new span and invokes the handler with the new context
// and options. The span should be finished by the caller.
func doClientRequest(
	ctx context.Context, cfg *config, method string, methodKind string, cc *grpc.ClientConn, opts []grpc.CallOption,
	handler func(ctx context.Context, opts []grpc.CallOption) error,
) (*tracer.Span, context.Context, error) {
	// inject the trace id into the metadata
	span, ctx := startSpanFromContext(
		ctx,
		method,
		cfg.spanName,
		cfg.serviceName.String(),
		cfg.serviceSource,
		cfg.startSpanOptions(
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.SpanKind, ext.SpanKindClient))...,
	)
	if methodKind != "" {
		span.SetTag(tagMethodKind, methodKind)
	}
	if cc != nil {
		if host, _, err := net.SplitHostPort(cc.Target()); err == nil {
			span.SetTag(ext.PeerHostname, host)
		}
	}
	// fill in the peer so we can add it to the tags
	var p peer.Peer
	opts = append(opts, grpc.Peer(&p))

	handlerCtx := injectSpanIntoContext(ctx)
	err := handler(handlerCtx, opts)

	setSpanTargetFromPeer(span, p)

	return span, ctx, err
}

// setSpanTargetFromPeer sets the target tags in a span based on the gRPC peer.
func setSpanTargetFromPeer(span *tracer.Span, p peer.Peer) {
	// if the peer was set, set the tags
	if p.Addr != nil {
		ip, port, err := net.SplitHostPort(p.Addr.String())
		if err == nil {
			if ip != "" {
				span.SetTag(ext.TargetHost, ip)
			}
			span.SetTag(ext.TargetPort, port)
		}
	}
}

// injectSpanIntoContext injects the span associated with a context as gRPC metadata
// if no span is associated with the context, just return the original context.
func injectSpanIntoContext(ctx context.Context) context.Context {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return ctx
	}
	md, ok := metadata.FromOutgoingContext(ctx)
	if ok {
		// we have to copy the metadata because its not safe to modify
		md = md.Copy()
	} else {
		md = metadata.MD{}
	}
	if err := tracer.Inject(span.Context(), grpcutil.MDCarrier(md)); err != nil {
		instr.Logger().Warn("ddtrace: failed to inject the span context into the gRPC metadata: %s", err.Error())
	}
	return metadata.NewOutgoingContext(ctx, md)
}
