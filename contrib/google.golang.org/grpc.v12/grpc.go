//go:generate protoc -I . fixtures_test.proto --go_out=plugins=grpc:.

// Package grpc provides functions to trace the google.golang.org/grpc package v1.2.
package grpc // import "gopkg.in/DataDog/dd-trace-go.v0/contrib/google.golang.org/grpc.v12"

import (
	"net"

	"gopkg.in/DataDog/dd-trace-go.v0/contrib/google.golang.org/internal/grpcutil"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v0/ddtrace/tracer"

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
	}
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		span, ctx := startSpanFromContext(ctx, info.FullMethod, cfg.serviceName)
		resp, err := handler(ctx, req)
		span.Finish(tracer.WithError(err))
		return resp, err
	}
}

func startSpanFromContext(ctx context.Context, method, service string) (ddtrace.Span, context.Context) {
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName(service),
		tracer.ResourceName(method),
		tracer.Tag("grpc.method", method),
		tracer.SpanType(ext.AppTypeRPC),
	}
	md, _ := metadata.FromContext(ctx) // nil is ok
	if sctx, err := tracer.Extract(grpcutil.MDCarrier(md)); err == nil {
		opts = append(opts, tracer.ChildOf(sctx))
	}
	return tracer.StartSpanFromContext(ctx, "grpc.server", opts...)
}

// UnaryClientInterceptor will add tracing to a gprc client.
func UnaryClientInterceptor(opts ...InterceptorOption) grpc.UnaryClientInterceptor {
	cfg := new(interceptorConfig)
	defaults(cfg)
	for _, fn := range opts {
		fn(cfg)
	}
	if cfg.serviceName == "" {
		cfg.serviceName = "grpc.client"
	}
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var (
			span ddtrace.Span
			p    peer.Peer
		)
		span, ctx = tracer.StartSpanFromContext(ctx, "grpc.client",
			tracer.Tag("grpc.method", method),
			tracer.SpanType(ext.AppTypeRPC),
		)
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
		span.SetTag("grpc.code", grpc.Code(err).String())
		span.Finish(tracer.WithError(err))
		return err
	}
}
