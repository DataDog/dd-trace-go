// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
	_, im := ss.cfg.ignoredMethods[ss.method]
	_, um := ss.cfg.untracedMethods[ss.method]
	if ss.cfg.traceStreamMessages && !im && !um {
		span, _ := startSpanFromContext(
			ss.ctx,
			ss.method,
			"grpc.message",
			ss.cfg.serviceName,
			ss.cfg.startSpanOptions(tracer.Measured())...,
		)
		span.SetTag(ext.Component, componentName)
		defer func() {
			withMetadataTags(ss.ctx, ss.cfg, span)
			withRequestTags(ss.cfg, m, span)
			finishWithError(span, err, ss.method, ss.cfg)
		}()
	}
	err = ss.ServerStream.RecvMsg(m)
	return err
}

func (ss *serverStream) SendMsg(m interface{}) (err error) {
	_, im := ss.cfg.ignoredMethods[ss.method]
	_, um := ss.cfg.untracedMethods[ss.method]
	if ss.cfg.traceStreamMessages && !im && !um {
		span, _ := startSpanFromContext(
			ss.ctx,
			ss.method,
			"grpc.message",
			ss.cfg.serviceName,
			ss.cfg.startSpanOptions(tracer.Measured())...,
		)
		span.SetTag(ext.Component, componentName)
		defer func() { finishWithError(span, err, ss.method, ss.cfg) }()
	}
	err = ss.ServerStream.SendMsg(m)
	return err
}

// StreamServerInterceptor will trace streaming requests to the given gRPC server.
func StreamServerInterceptor(opts ...Option) grpc.StreamServerInterceptor {
	cfg := new(config)
	serverDefaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/google.golang.org/grpc: Configuring StreamServerInterceptor: %#v", cfg)
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		ctx := ss.Context()
		// if we've enabled call tracing, create a span
		_, im := cfg.ignoredMethods[info.FullMethod]
		_, um := cfg.untracedMethods[info.FullMethod]
		if cfg.traceStreamCalls && !im && !um {
			var span ddtrace.Span
			span, ctx = startSpanFromContext(
				ctx,
				info.FullMethod,
				cfg.spanName,
				cfg.serviceName,
				cfg.startSpanOptions(tracer.Measured(),
					tracer.Tag(ext.Component, componentName),
					tracer.Tag(ext.SpanKind, ext.SpanKindServer))...,
			)
			switch {
			case info.IsServerStream && info.IsClientStream:
				span.SetTag(tagMethodKind, methodKindBidiStream)
			case info.IsServerStream:
				span.SetTag(tagMethodKind, methodKindServerStream)
			case info.IsClientStream:
				span.SetTag(tagMethodKind, methodKindClientStream)
			}
			defer func() { finishWithError(span, err, info.FullMethod, cfg) }()
			if appsec.Enabled() {
				handler = appsecStreamHandlerMiddleware(info.FullMethod, span, handler)
			}
		}

		// call the original handler with a new stream, which traces each send
		// and recv if message tracing is enabled
		return handler(srv, &serverStream{
			ServerStream: ss,
			cfg:          cfg,
			method:       info.FullMethod,
			ctx:          ctx,
		})
	}
}

// UnaryServerInterceptor will trace requests to the given grpc server.
func UnaryServerInterceptor(opts ...Option) grpc.UnaryServerInterceptor {
	cfg := new(config)
	serverDefaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/google.golang.org/grpc: Configuring UnaryServerInterceptor: %#v", cfg)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		_, im := cfg.ignoredMethods[info.FullMethod]
		_, um := cfg.untracedMethods[info.FullMethod]
		if im || um {
			return handler(ctx, req)
		}
		span, ctx := startSpanFromContext(
			ctx,
			info.FullMethod,
			cfg.spanName,
			cfg.serviceName,
			cfg.startSpanOptions(tracer.Measured(),
				tracer.Tag(ext.Component, componentName),
				tracer.Tag(ext.SpanKind, ext.SpanKindServer))...,
		)
		span.SetTag(tagMethodKind, methodKindUnary)
		withMetadataTags(ctx, cfg, span)
		withRequestTags(cfg, req, span)
		if appsec.Enabled() {
			handler = appsecUnaryHandlerMiddleware(info.FullMethod, span, handler)
		}
		resp, err := handler(ctx, req)
		finishWithError(span, err, info.FullMethod, cfg)
		return resp, err
	}
}

func withMetadataTags(ctx context.Context, cfg *config, span ddtrace.Span) {
	if cfg.withMetadataTags {
		md, _ := metadata.FromIncomingContext(ctx) // nil is ok
		for k, v := range md {
			if _, ok := cfg.ignoredMetadata[k]; !ok {
				span.SetTag(tagMetadataPrefix+k, v)
			}
		}
	}
}

func withRequestTags(cfg *config, req interface{}, span ddtrace.Span) {
	if cfg.withRequestTags {
		if p, ok := req.(proto.Message); ok {
			if b, err := protojson.Marshal(p); err == nil {
				span.SetTag(tagRequest, string(b))
			}
		}
	}
}
