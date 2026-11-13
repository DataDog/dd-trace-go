// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package grpc

import (
	"context"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type serverStream struct {
	grpc.ServerStream
	cfg    *config
	method string
	// resource is the span resource name; it differs from method for
	// unknown-service calls.
	resource string
	ctx      context.Context
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
	_, um := ss.cfg.untracedMethods[ss.method]
	if ss.cfg.traceStreamMessages && !um {
		span, _ := startSpanFromContext(
			ss.ctx,
			ss.method,
			"grpc.message",
			ss.cfg.serviceName.String(),
			ss.cfg.serviceSource,
			ss.cfg.startSpanOptions(tracer.Measured())...,
		)
		span.SetTag(ext.Component, componentName)
		if ss.resource != ss.method {
			span.SetTag(ext.ResourceName, ss.resource)
		}
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
	_, um := ss.cfg.untracedMethods[ss.method]
	if ss.cfg.traceStreamMessages && !um {
		span, _ := startSpanFromContext(
			ss.ctx,
			ss.method,
			"grpc.message",
			ss.cfg.serviceName.String(),
			ss.cfg.serviceSource,
			ss.cfg.startSpanOptions(tracer.Measured())...,
		)
		span.SetTag(ext.Component, componentName)
		if ss.resource != ss.method {
			span.SetTag(ext.ResourceName, ss.resource)
		}
		defer func() { finishWithError(span, err, ss.method, ss.cfg) }()
	}
	err = ss.ServerStream.SendMsg(m)
	return err
}

const (
	// resourceUnknownMethod is the resource name for calls served by
	// grpc.UnknownServiceHandler, following OTel RPC conventions.
	resourceUnknownMethod = "_OTHER"
	tagMethodOriginal     = "rpc.method_original"
)

// isUnknownServiceCall reports whether the call is served by
// grpc.UnknownServiceHandler. grpc invokes that handler with a nil server and
// a bidi stream, but a registered method with a nil implementation looks the
// same, so the registered services are checked when they are known.
func isUnknownServiceCall(cfg *config, srv interface{}, info *grpc.StreamServerInfo) bool {
	if srv != nil || !info.IsClientStream || !info.IsServerStream {
		return false
	}
	if cfg.serviceInfo == nil {
		return true
	}
	svc, name, ok := splitFullMethod(info.FullMethod)
	if !ok {
		return true
	}
	si, ok := cfg.serviceInfo()[svc]
	if !ok {
		return true
	}
	for _, m := range si.Methods {
		if m.Name == name {
			return false
		}
	}
	return true
}

// splitFullMethod splits "/pkg.Service/Method" into service and method.
func splitFullMethod(fm string) (service, method string, ok bool) {
	fm, ok = strings.CutPrefix(fm, "/")
	if !ok {
		return "", "", false
	}
	return strings.Cut(fm, "/")
}

// StreamServerInterceptor will trace streaming requests to the given gRPC server.
func StreamServerInterceptor(opts ...Option) grpc.StreamServerInterceptor {
	cfg := new(config)
	serverDefaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	instr.Logger().Debug("contrib/google.golang.org/grpc: Configuring StreamServerInterceptor: %#v", cfg)
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) (err error) {
		ctx := ss.Context()
		// grpc invokes the UnknownServiceHandler with a nil server and a
		// bidi stream. The method there is chosen by the client, so it must
		// not become the resource name (#5813).
		resource := info.FullMethod
		unknown := isUnknownServiceCall(cfg, srv, info)
		if unknown {
			resource = resourceUnknownMethod
		}
		// if we've enabled call tracing, create a span
		_, um := cfg.untracedMethods[info.FullMethod]
		if cfg.traceStreamCalls && !um {
			var span *tracer.Span
			span, ctx = startSpanFromContext(
				ctx,
				info.FullMethod,
				cfg.spanName,
				cfg.serviceName.String(),
				cfg.serviceSource,
				cfg.startSpanOptions(tracer.Measured(),
					tracer.Tag(ext.Component, componentName),
					tracer.Tag(ext.SpanKind, ext.SpanKindServer))...,
			)
			if unknown {
				span.SetTag(ext.ResourceName, resource)
				span.SetTag(tagMethodOriginal, info.FullMethod)
			}
			switch {
			case info.IsServerStream && info.IsClientStream:
				span.SetTag(tagMethodKind, methodKindBidiStream)
			case info.IsServerStream:
				span.SetTag(tagMethodKind, methodKindServerStream)
			case info.IsClientStream:
				span.SetTag(tagMethodKind, methodKindClientStream)
			}
			defer func() { finishWithError(span, err, info.FullMethod, cfg) }()
			if instr.AppSecEnabled() {
				handler = appsecStreamHandlerMiddleware(info.FullMethod, span, handler)
			}
		}

		// call the original handler with a new stream, which traces each send
		// and recv if message tracing is enabled
		return handler(srv, &serverStream{
			ServerStream: ss,
			cfg:          cfg,
			method:       info.FullMethod,
			resource:     resource,
			ctx:          ctx,
		})
	}
}

// UnaryServerInterceptor will trace requests to the given grpc server.
func UnaryServerInterceptor(opts ...Option) grpc.UnaryServerInterceptor {
	cfg := new(config)
	serverDefaults(cfg)
	for _, fn := range opts {
		fn.apply(cfg)
	}
	instr.Logger().Debug("contrib/google.golang.org/grpc: Configuring UnaryServerInterceptor: %#v", cfg)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		_, um := cfg.untracedMethods[info.FullMethod]
		if um {
			return handler(ctx, req)
		}
		span, ctx := startSpanFromContext(
			ctx,
			info.FullMethod,
			cfg.spanName,
			cfg.serviceName.String(),
			cfg.serviceSource,
			cfg.startSpanOptions(tracer.Measured(),
				tracer.Tag(ext.Component, componentName),
				tracer.Tag(ext.SpanKind, ext.SpanKindServer))...,
		)
		span.SetTag(tagMethodKind, methodKindUnary)
		withMetadataTags(ctx, cfg, span)
		withRequestTags(cfg, req, span)
		if instr.AppSecEnabled() {
			handler = appsecUnaryHandlerMiddleware(info.FullMethod, span, handler)
		}
		resp, err := handler(ctx, req)
		finishWithError(span, err, info.FullMethod, cfg)
		return resp, err
	}
}

func withMetadataTags(ctx context.Context, cfg *config, span *tracer.Span) {
	if cfg.withMetadataTags {
		md, _ := metadata.FromIncomingContext(ctx) // nil is ok
		for k, v := range md {
			if _, ok := cfg.ignoredMetadata[k]; ok {
				continue
			}

			// gRPC binary metadata keys end in "-bin"; their values are
			// arbitrary bytes and must not be stored as string span tags.
			if strings.HasSuffix(k, "-bin") {
				continue
			}
			span.SetTag(tagMetadataPrefix+k, v)
		}
	}
}

func withRequestTags(cfg *config, req interface{}, span *tracer.Span) {
	if cfg.withRequestTags {
		if p, ok := req.(proto.Message); ok {
			if b, err := protojson.Marshal(p); err == nil {
				span.SetTag(tagRequest, string(b))
			}
		}
	}
}
