// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:generate protoc -I . fixtures_test.proto --go_out=plugins=grpc:.

// Package grpc provides functions to trace the google.golang.org/grpc package v1.2.
package grpc // import "gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/grpc.v12"

import (
	"math"
	"net"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/contrib/google.golang.org/internal/grpcutil"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/globalconfig"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	context "golang.org/x/net/context"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// UnaryServerInterceptor will trace requests to the given grpc server.
func UnaryServerInterceptor(opts ...InterceptorOption) grpc.UnaryServerInterceptor {
	cfg := new(interceptorConfig)
	defaults(cfg)
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
		span, ctx := startSpanFromContext(ctx, info.FullMethod, cfg.serviceName, cfg.analyticsRate)
		resp, err := handler(ctx, req)
		span.SetTag(ext.GRPCStatus, grpc.Code(err).String())
		span.Finish(tracer.WithError(err))
		return resp, err
	}
}

func startSpanFromContext(ctx context.Context, method, service string, rate float64) (ddtrace.Span, context.Context) {
	rpcTags := extractRPCTags(method)
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(service),
		tracer.ResourceName(method),
		tracer.Tag(tagMethod, method),
		tracer.SpanType(ext.AppTypeRPC),
		tracer.Measured(),
		tracer.Tag(ext.Component, "google.golang.org/grpc.v12"),
		tracer.Tag(ext.SpanKind, ext.SpanKindServer),
		tracer.Tag(ext.RPCSystem, "grpc"),
		tracer.Tag(ext.RPCService, rpcTags[ext.RPCService]),
		tracer.Tag(ext.RPCMethod, rpcTags[ext.RPCMethod]),
		tracer.Tag(ext.GRPCPackage, rpcTags[ext.GRPCPackage]),
		tracer.Tag(ext.GRPCPath, method),
		tracer.Tag(ext.GRPCKind, "unary"),
	}
	if !math.IsNaN(rate) {
		opts = append(opts, tracer.Tag(ext.EventSampleRate, rate))
	}
	md, _ := metadata.FromContext(ctx) // nil is ok
	if sctx, err := tracer.Extract(grpcutil.MDCarrier(md)); err == nil {
		opts = append(opts, tracer.ChildOf(sctx))
	}
	return tracer.StartSpanFromContext(ctx, "grpc.server", opts...)
}

// UnaryClientInterceptor will add tracing to a grpc client.
func UnaryClientInterceptor(opts ...InterceptorOption) grpc.UnaryClientInterceptor {
	cfg := new(interceptorConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	if cfg.serviceName == "" {
		cfg.serviceName = "grpc.client"
	}
	log.Debug("contrib/google.golang.org/grpc.v12: Configuring UnaryClientInterceptor: %#v", cfg)
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var (
			span ddtrace.Span
			p    peer.Peer
		)
		rpcTags := extractRPCTags(method)
		spanopts := []ddtrace.StartSpanOption{
			tracer.Tag(tagMethod, method),
			tracer.SpanType(ext.AppTypeRPC),
			tracer.Tag(ext.RPCSystem, "grpc"),
			tracer.Tag(ext.RPCService, rpcTags[ext.RPCService]),
			tracer.Tag(ext.RPCMethod, rpcTags[ext.RPCMethod]),
			tracer.Tag(ext.GRPCPackage, rpcTags[ext.GRPCPackage]),
			tracer.Tag(ext.GRPCPath, method),
			tracer.Tag(ext.GRPCKind, "unary"),
		}
		if !math.IsNaN(cfg.analyticsRate) {
			spanopts = append(spanopts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
		}
		spanopts = append(spanopts, tracer.Tag(ext.Component, "google.golang.org/grpc.v12"))
		spanopts = append(spanopts, tracer.Tag(ext.SpanKind, ext.SpanKindClient))
		span, ctx = tracer.StartSpanFromContext(ctx, "grpc.client", spanopts...)

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
		span.SetTag(ext.GRPCStatus, grpc.Code(err).String())
		span.Finish(tracer.WithError(err))
		return err
	}
}

// extractRPCTags will assign the proper tag values for method, service, package according to otel given a full method
func extractRPCTags(fullMethod string) map[string]string {

	//Otel definition: https://opentelemetry.io/docs/reference/specification/trace/semantic_conventions/rpc/#span-name

	tags := map[string]string{
		ext.RPCMethod:   "",
		ext.RPCService:  "",
		ext.GRPCPackage: "",
	}

	//Always remove leading slash
	fullMethod = strings.TrimPrefix(fullMethod, "/")

	//Split by slash and get everything after last slash as method
	slashSplit := strings.SplitAfter(fullMethod, "/")
	tags[ext.RPCMethod] = slashSplit[len(slashSplit)-1]

	//Join everything before last slash and remove last slash as service
	tags[ext.RPCService] = strings.TrimSuffix(strings.Join(slashSplit[:len(slashSplit)-1], ""), "/")

	//Split by period and see if package exists if period is found
	if strings.Contains(tags[ext.RPCService], ".") {
		dotSplit := strings.SplitAfter(tags[ext.RPCService], ".")
		tags[ext.GRPCPackage] = strings.TrimSuffix(strings.Join(dotSplit[:len(dotSplit)-1], ""), ".")
	}

	return tags
}