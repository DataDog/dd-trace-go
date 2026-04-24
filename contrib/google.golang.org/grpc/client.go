// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"
	"net"
	"sync"

	"github.com/DataDog/dd-trace-go/contrib/google.golang.org/grpc/v2/internal/grpcutil"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

type clientStream struct {
	grpc.ClientStream
	cfg    *config
	method string
	// span is the stream-level span, nil when call-level tracing is disabled.
	// readyOnce guards one-time peer tagging and finish-goroutine launch that
	// depend on span being non-nil and ClientStream.Context() being safe.
	span      *tracer.Span
	readyOnce sync.Once
}

// onStreamReady performs work that depends on ClientStream.Context() being
// safe to call (i.e. after Header or RecvMsg has returned): peer tagging and
// launching the goroutine that finishes the stream-level span on completion.
func (cs *clientStream) onStreamReady() {
	if cs.span == nil {
		return
	}
	cs.readyOnce.Do(func() {
		ctx := cs.ClientStream.Context()
		if p, ok := peer.FromContext(ctx); ok {
			setSpanTargetFromPeer(cs.span, *p)
		}
		go func() {
			<-ctx.Done()
			finishWithError(cs.span, ctx.Err(), cs.cfg)
		}()
	})
}

func (cs *clientStream) Context() context.Context {
	ctx := cs.ClientStream.Context()
	cs.onStreamReady()
	return ctx
}

func (cs *clientStream) Header() (metadata.MD, error) {
	md, err := cs.ClientStream.Header()
	cs.onStreamReady()
	return md, err
}

func (cs *clientStream) CloseSend() error {
	err := cs.ClientStream.CloseSend()
	cs.onStreamReady()
	return err
}

func (cs *clientStream) RecvMsg(m interface{}) (err error) {
	err = cs.ClientStream.RecvMsg(m)
	cs.onStreamReady()
	if !cs.cfg.traceStreamMessages {
		return err
	}
	if _, ok := cs.cfg.untracedMethods[cs.method]; ok {
		return err
	}
	span, _ := startSpanFromContext(
		cs.ClientStream.Context(),
		cs.method,
		"grpc.message",
		cs.cfg.serviceName.String(),
		cs.cfg.serviceSource,
		cs.cfg.startSpanOptions()...,
	)
	span.SetTag(ext.Component, componentName)
	finishWithError(span, err, cs.cfg)
	return err
}

func (cs *clientStream) SendMsg(m interface{}) (err error) {
	if !cs.cfg.traceStreamMessages {
		return cs.ClientStream.SendMsg(m)
	}
	if _, ok := cs.cfg.untracedMethods[cs.method]; ok {
		return cs.ClientStream.SendMsg(m)
	}
	span, _ := startSpanFromContext(
		cs.ClientStream.Context(),
		cs.method,
		"grpc.message",
		cs.cfg.serviceName.String(),
		cs.cfg.serviceSource,
		cs.cfg.startSpanOptions()...,
	)
	span.SetTag(ext.Component, componentName)
	err = cs.ClientStream.SendMsg(m)
	finishWithError(span, err, cs.cfg)
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
		var (
			stream grpc.ClientStream
			span   *tracer.Span
		)
		if _, ok := cfg.untracedMethods[method]; cfg.traceStreamCalls && !ok {
			var err error
			span, ctx, err = doClientRequest(ctx, cfg, method, methodKind, cc, opts,
				func(ctx context.Context, opts []grpc.CallOption) error {
					var err error
					stream, err = streamer(ctx, desc, cc, method, opts...)
					return err
				})
			if err != nil {
				finishWithError(span, err, cfg)
				return nil, err
			}
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
		return &clientStream{
			ClientStream: stream,
			cfg:          cfg,
			method:       method,
			span:         span,
		}, nil
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
		finishWithError(span, err, cfg)
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
