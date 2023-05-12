// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:generate protoc -I . fixtures_test.proto --go_out=plugins=grpc:.

// Package grpc provides functions to trace the google.golang.org/grpc package v1.2.
package grpc // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc.v12"

import (
	"net"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/internal/grpcutil"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/telemetry"

	context "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

const componentName = "google.golang.org/grpc.v12"

func init() {
	telemetry.LoadIntegration(componentName)
}

// UnaryServerInterceptor will trace requests to the given grpc server.
func UnaryServerInterceptor(opts ...InterceptorOption) grpc.UnaryServerInterceptor {
	cfg := new(interceptorConfig)
	serverDefaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	if cfg.serviceName == "" {
		cfg.serviceName = "grpc.server"
		if svc := globalconfig.ServiceName(); svc != "" {
			cfg.serviceName = svc
		}
	}

	log.Debug("contrib/google.golang.org/grpc.v12: Configuring UnaryServerInterceptor: %#v", cfg)
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		span, ctx := startServerSpanFromContext(ctx, info.FullMethod, cfg)
		resp, err := handler(ctx, req)
		span.Finish(tracer.WithError(err))
		return resp, err
	}
}

func startServerSpanFromContext(ctx context.Context, method string, cfg *interceptorConfig) (ddtrace.Span, context.Context) {
	extraOpts := []tracer.StartSpanOption{
		tracer.ServiceName(cfg.serviceName),
		tracer.ResourceName(method),
		tracer.Tag(tagMethod, method),
		tracer.SpanType(ext.AppTypeRPC),
		tracer.Measured(),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
		tracer.Tag(ext.RPCSystem, ext.RPCSystemGRPC),
		tracer.Tag(ext.GRPCFullMethod, method),
	}
	// copy opts in case the caller reuses the slice in parallel
	// we will add the items in extraOpts
	optsLocal := make([]tracer.StartSpanOption, len(cfg.spanOpts), len(cfg.spanOpts)+len(extraOpts))
	copy(optsLocal, cfg.spanOpts)
	optsLocal = append(optsLocal, extraOpts...)
	md, _ := metadata.FromContext(ctx) // nil is ok
	if sctx, err := tracer.Extract(grpcutil.MDCarrier(md)); err == nil {
		optsLocal = append(optsLocal, tracer.ChildOf(sctx))
	}
	return tracer.StartSpanFromContext(ctx, cfg.spanName, optsLocal...)
}

// UnaryClientInterceptor will add tracing to a grpc client.
func UnaryClientInterceptor(opts ...InterceptorOption) grpc.UnaryClientInterceptor {
	cfg := new(interceptorConfig)
	clientDefaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	log.Debug("contrib/google.golang.org/grpc.v12: Configuring UnaryClientInterceptor: %#v", cfg)
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var (
			span ddtrace.Span
			p    peer.Peer
		)
		spanopts := cfg.spanOpts
		spanopts = append(spanopts,
			tracer.ServiceName(cfg.serviceName),
			tracer.Tag(tagMethod, method),
			tracer.SpanType(ext.AppTypeRPC),
			tracer.Tag(ext.Component, componentName),
			tracer.Tag(ext.SpanKind, ext.SpanKindClient),
			tracer.Tag(ext.RPCSystem, ext.RPCSystemGRPC),
			tracer.Tag(ext.GRPCFullMethod, method),
		)
		span, ctx = tracer.StartSpanFromContext(ctx, cfg.spanName, spanopts...)
		md, ok := metadata.FromContext(ctx)
		if !ok {
			md = metadata.MD{}
		}
		_ = tracer.Inject(span.Context(), grpcutil.MDCarrier(md))
		ctx = metadata.NewContext(ctx, md)
		opts = append(opts, grpc.Peer(&p))
		err := invoker(ctx, method, req, reply, cc, opts...)
		if p.Addr != nil {
			addr := p.Addr.String()
			host, port, err := net.SplitHostPort(addr)
			if err == nil {
				if host != "" {
					span.SetTag(ext.TargetHost, host)
				}
				span.SetTag(ext.TargetPort, port)
			}
		}
		span.SetTag(tagCode, grpc.Code(err).String())
		span.Finish(tracer.WithError(err))
		return err
	}
}
