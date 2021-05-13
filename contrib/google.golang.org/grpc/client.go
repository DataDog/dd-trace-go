// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"net"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/internal/grpcutil"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	context "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/grpclog"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

type clientStream struct {
	grpc.ClientStream
	cfg    *config
	method string
}

func (cs *clientStream) RecvMsg(m interface{}) (err error) {
	if cs.cfg.traceStreamMessages {
		span, _ := startSpanFromContext(
			cs.Context(),
			cs.method,
			"grpc.message",
			cs.cfg.clientServiceName(),
			tracer.AnalyticsRate(cs.cfg.analyticsRate),
		)
		if p, ok := peer.FromContext(cs.Context()); ok {
			setSpanTargetFromPeer(span, *p)
		}
		defer func() { finishWithError(span, err, cs.cfg) }()
	}
	err = cs.ClientStream.RecvMsg(m)
	return err
}

func (cs *clientStream) SendMsg(m interface{}) (err error) {
	if cs.cfg.traceStreamMessages {
		span, _ := startSpanFromContext(
			cs.Context(),
			cs.method,
			"grpc.message",
			cs.cfg.clientServiceName(),
			tracer.AnalyticsRate(cs.cfg.analyticsRate),
		)
		if p, ok := peer.FromContext(cs.Context()); ok {
			setSpanTargetFromPeer(span, *p)
		}
		defer func() { finishWithError(span, err, cs.cfg) }()
	}
	err = cs.ClientStream.SendMsg(m)
	return err
}

// StreamClientInterceptor returns a grpc.StreamClientInterceptor which will trace client
// streams using the given set of options.
func StreamClientInterceptor(opts ...Option) grpc.StreamClientInterceptor {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/google.golang.org/grpc: Configuring StreamClientInterceptor: %#v", cfg)
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
		if cfg.traceStreamCalls {
			span, err := doClientRequest(ctx, cfg, method, methodKind, opts,
				func(ctx context.Context, opts []grpc.CallOption) error {
					var err error
					stream, err = streamer(ctx, desc, cc, method, opts...)
					return err
				})
			if err != nil {
				finishWithError(span, err, cfg)
				return nil, err
			}

			// the Peer call option only works with unary calls, so for streams
			// we need to set it via FromContext
			if p, ok := peer.FromContext(stream.Context()); ok {
				setSpanTargetFromPeer(span, *p)
			}

			go func() {
				<-stream.Context().Done()
				finishWithError(span, stream.Context().Err(), cfg)
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
		return &clientStream{
			ClientStream: stream,
			cfg:          cfg,
			method:       method,
		}, nil
	}
}

// UnaryClientInterceptor returns a grpc.UnaryClientInterceptor which will trace requests using
// the given set of options.
func UnaryClientInterceptor(opts ...Option) grpc.UnaryClientInterceptor {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/google.golang.org/grpc: Configuring UnaryClientInterceptor: %#v", cfg)
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		span, err := doClientRequest(ctx, cfg, method, methodKindUnary, opts,
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
	ctx context.Context, cfg *config, method string, methodKind string, opts []grpc.CallOption,
	handler func(ctx context.Context, opts []grpc.CallOption) error,
) (ddtrace.Span, error) {
	// inject the trace id into the metadata
	span, ctx := startSpanFromContext(
		ctx,
		method,
		"grpc.client",
		cfg.clientServiceName(),
		tracer.AnalyticsRate(cfg.analyticsRate),
	)
	if methodKind != "" {
		span.SetTag(tagMethodKind, methodKind)
	}
	ctx = injectSpanIntoContext(ctx)

	// fill in the peer so we can add it to the tags
	var p peer.Peer
	opts = append(opts, grpc.Peer(&p))

	err := handler(ctx, opts)

	setSpanTargetFromPeer(span, p)

	return span, err
}

// setSpanTargetFromPeer sets the target tags in a span based on the gRPC peer.
func setSpanTargetFromPeer(span ddtrace.Span, p peer.Peer) {
	// if the peer was set, set the tags
	if p.Addr != nil {
		host, port, err := net.SplitHostPort(p.Addr.String())
		if err == nil {
			if host != "" {
				span.SetTag(ext.TargetHost, host)
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
		// in practice this error should never really happen
		grpclog.Warningf("ddtrace: failed to inject the span context into the gRPC metadata: %v", err)
	}
	return metadata.NewOutgoingContext(ctx, md)
}
