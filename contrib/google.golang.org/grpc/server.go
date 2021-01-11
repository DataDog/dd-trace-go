// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/proto"
	context "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type serverStream struct {
	grpc.ServerStream
	cfg    *config
	method string
	ctx    context.Context
}

// Context returns the ServerStream Context.
//
// One subtle difference between the server stream and the client stream is the
// order the contexts are created. In the client stream we pass the context to
// the streamer function, which means the ClientStream.Context() derives from
// the span context, so we want to return that. However with the ServerStream
// the span context derives from the ServerStream.Context, so we want to return
// the span context instead.
func (ss *serverStream) Context() context.Context {
	return ss.ctx
}

func (ss *serverStream) RecvMsg(m interface{}) (err error) {
	if _, ok := ss.cfg.ignoredMethods[ss.method]; ss.cfg.traceStreamMessages && !ok {
		span, _ := startSpanFromContext(
			ss.ctx,
			ss.method,
			"grpc.message",
			ss.cfg.serverServiceName(),
			tracer.AnalyticsRate(ss.cfg.analyticsRate),
			tracer.Measured(),
		)
		defer func() { finishWithError(span, err, ss.cfg) }()
	}
	err = ss.ServerStream.RecvMsg(m)
	return err
}

func (ss *serverStream) SendMsg(m interface{}) (err error) {
	if _, ok := ss.cfg.ignoredMethods[ss.method]; ss.cfg.traceStreamMessages && !ok {
		span, _ := startSpanFromContext(
			ss.ctx,
			ss.method,
			"grpc.message",
			ss.cfg.serverServiceName(),
			tracer.AnalyticsRate(ss.cfg.analyticsRate),
			tracer.Measured(),
		)
		defer func() { finishWithError(span, err, ss.cfg) }()
	}
	err = ss.ServerStream.SendMsg(m)
	return err
}

// StreamServerInterceptor will trace streaming requests to the given gRPC server.
func StreamServerInterceptor(opts ...Option) grpc.StreamServerInterceptor {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	if cfg.serviceName == "" {
		cfg.serviceName = "grpc.server"
	}
	log.Debug("contrib/google.golang.org/grpc: Configuring StreamServerInterceptor: %#v", cfg)
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		ctx := ss.Context()
		// if we've enabled call tracing, create a span
		if _, ok := cfg.ignoredMethods[info.FullMethod]; cfg.traceStreamCalls && !ok {
			var span ddtrace.Span
			span, ctx = startSpanFromContext(
				ctx,
				info.FullMethod,
				"grpc.server",
				cfg.serverServiceName(),
				tracer.AnalyticsRate(cfg.analyticsRate),
				tracer.Measured(),
			)
			switch {
			case info.IsServerStream && info.IsClientStream:
				span.SetTag(tagMethodKind, methodKindBidiStream)
			case info.IsServerStream:
				span.SetTag(tagMethodKind, methodKindServerStream)
			case info.IsClientStream:
				span.SetTag(tagMethodKind, methodKindClientStream)
			}
			defer func() { finishWithError(span, err, cfg) }()
		}

		// call the original handler with a new stream, which traces each send
		// and recv if message tracing is enabled
		err = handler(srv, &serverStream{
			ServerStream: ss,
			cfg:          cfg,
			method:       info.FullMethod,
			ctx:          ctx,
		})

		return err
	}
}

// UnaryServerInterceptor will trace requests to the given grpc server.
func UnaryServerInterceptor(opts ...Option) grpc.UnaryServerInterceptor {
	cfg := new(config)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/google.golang.org/grpc: Configuring UnaryServerInterceptor: %#v", cfg)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		if _, ok := cfg.ignoredMethods[info.FullMethod]; ok {
			return handler(ctx, req)
		}
		span, ctx := startSpanFromContext(
			ctx,
			info.FullMethod,
			"grpc.server",
			cfg.serverServiceName(),
			tracer.AnalyticsRate(cfg.analyticsRate),
			tracer.Measured(),
		)
		span.SetTag(tagMethodKind, methodKindUnary)

		if cfg.withMetadataTags {
			md, _ := metadata.FromIncomingContext(ctx) // nil is ok
			for k, v := range md {
				if _, ok := cfg.ignoredMetadata[k]; !ok {
					span.SetTag(tagMetadataPrefix+k, v)
				}
			}
		}
		if cfg.withRequestTags {
			var m jsonpb.Marshaler
			if p, ok := req.(proto.Message); ok {
				if s, err := m.MarshalToString(p); err == nil {
					span.SetTag(tagRequest, s)
				}
			}
		}
		resp, err := handler(ctx, req)
		finishWithError(span, err, cfg)
		return resp, err
	}
}
