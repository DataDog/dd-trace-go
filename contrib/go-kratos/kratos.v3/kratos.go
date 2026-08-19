// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package kratos provides tracing middleware for Kratos v3 request/response
// HTTP and unary gRPC clients and servers.
package kratos // import "github.com/DataDog/dd-trace-go/contrib/go-kratos/kratos.v3/v2"

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/grpc/status"
)

const component = instrumentation.PackageGoKratosV3

var instr *instrumentation.Instrumentation

func init() {
	instr = instrumentation.Load(component)
}

// Server returns middleware that traces Kratos request/response HTTP and unary
// gRPC server calls.
func Server(opts ...Option) middleware.Middleware {
	cfg := new(config)
	serverDefaults(cfg)
	applyOptions(cfg, opts)

	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (reply any, err error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return handler(ctx, req)
			}

			spanOpts := startSpanOptions(cfg, tr, ext.SpanKindServer)
			if header := tr.RequestHeader(); header != nil {
				if spanctx, extractErr := tracer.Extract(headerCarrier{Header: header}); extractErr == nil {
					if spanctx != nil && spanctx.SpanLinks() != nil {
						spanOpts = append(spanOpts, tracer.WithSpanLinks(spanctx.SpanLinks()))
					}
					spanOpts = append(spanOpts, tracer.ChildOf(spanctx))
				}
			}

			span, spanCtx := tracer.StartSpanFromContext(ctx, operationName(tr, instrumentation.ComponentServer), spanOpts...)
			defer func() { finishSpan(span, tr, err, ext.SpanKindServer, cfg.noDebugStack) }()
			return handler(spanCtx, req)
		}
	}
}

// Client returns middleware that traces Kratos request/response HTTP and unary
// gRPC client calls and injects the trace context into outgoing headers.
func Client(opts ...Option) middleware.Middleware {
	cfg := new(config)
	clientDefaults(cfg)
	applyOptions(cfg, opts)

	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (reply any, err error) {
			tr, ok := transport.FromClientContext(ctx)
			if !ok {
				return handler(ctx, req)
			}

			span, spanCtx := tracer.StartSpanFromContext(ctx, operationName(tr, instrumentation.ComponentClient), startSpanOptions(cfg, tr, ext.SpanKindClient)...)
			defer func() { finishSpan(span, tr, err, ext.SpanKindClient, cfg.noDebugStack) }()
			if header := tr.RequestHeader(); header != nil {
				if injectErr := tracer.Inject(span.Context(), headerCarrier{Header: header}); injectErr != nil {
					instr.Logger().Warn("contrib/go-kratos/kratos.v3: failed to inject trace headers: %s", injectErr.Error())
				}
			}
			return handler(spanCtx, req)
		}
	}
}

func startSpanOptions(cfg *config, tr transport.Transporter, spanKind string) []tracer.StartSpanOption {
	spanOpts := make([]tracer.StartSpanOption, 0, 12+len(cfg.spanOpts))
	spanOpts = append(spanOpts,
		instrumentation.ServiceNameWithSource(cfg.serviceName, cfg.serviceSource),
		tracer.ResourceName(tr.Operation()),
		tracer.Tag(ext.Component, component),
		tracer.Tag(ext.SpanKind, spanKind),
		tracer.Tag(ext.RPCSystem, tr.Kind().String()),
	)
	if spanKind == ext.SpanKindServer {
		spanOpts = append(spanOpts, tracer.Measured())
	}

	service, method := splitOperation(tr.Operation())
	if service != "" {
		spanOpts = append(spanOpts, tracer.Tag(ext.RPCService, service))
	}
	if method != "" {
		spanOpts = append(spanOpts, tracer.Tag(ext.RPCMethod, method))
	}

	if tr.Kind() == transport.KindHTTP {
		if httpTr, ok := tr.(kratoshttp.Transporter); ok && httpTr.Request() != nil {
			req := httpTr.Request()
			spanType := ext.SpanTypeHTTP
			httpURL := httptrace.URLFromClientRequest(req, true)
			if spanKind == ext.SpanKindServer {
				spanType = ext.SpanTypeWeb
				httpURL = httptrace.URLFromRequest(req, true)
			}
			spanOpts = append(spanOpts,
				tracer.SpanType(spanType),
				tracer.Tag(ext.HTTPMethod, req.Method),
				tracer.Tag(ext.HTTPURL, httpURL),
				httptrace.HeaderTagsFromRequest(req, cfg.headerTags),
			)
			if route := httpTr.PathTemplate(); route != "" {
				spanOpts = append(spanOpts, tracer.Tag(ext.HTTPRoute, route))
			}
		}
	} else if tr.Kind() == transport.KindGRPC {
		spanOpts = append(spanOpts,
			tracer.SpanType(ext.AppTypeRPC),
			tracer.Tag(ext.GRPCFullMethod, tr.Operation()),
		)
	}
	return append(spanOpts, cfg.spanOpts...)
}

func finishSpan(span *tracer.Span, tr transport.Transporter, err error, spanKind string, noDebugStack bool) {
	if tr.Kind() == transport.KindGRPC {
		span.SetTag("grpc.code", status.Code(err).String())
	}
	if err == nil {
		if tr.Kind() == transport.KindHTTP {
			span.SetTag(ext.HTTPCode, strconv.Itoa(http.StatusOK))
		}
		span.Finish()
		return
	}

	kratosErr := kratoserrors.FromError(err)
	span.SetTag("kratos.status_code", kratosErr.Code)
	if kratosErr.Reason != "" {
		span.SetTag("kratos.error_reason", kratosErr.Reason)
	}
	if tr.Kind() == transport.KindHTTP {
		span.SetTag(ext.HTTPCode, strconv.Itoa(int(kratosErr.Code)))
	}
	var finishOpts []tracer.FinishOption
	// A 4xx returned by a server represents a client error and should not mark
	// the server span as erroneous. Client spans and non-HTTP spans retain the
	// original error because the remote call itself failed.
	if tr.Kind() != transport.KindHTTP || spanKind == ext.SpanKindClient || kratosErr.Code >= http.StatusInternalServerError {
		finishOpts = append(finishOpts, tracer.WithError(err))
		if noDebugStack {
			finishOpts = append(finishOpts, tracer.NoDebugStack())
		}
	}
	span.Finish(finishOpts...)
}

func operationName(tr transport.Transporter, componentType instrumentation.Component) string {
	return instr.OperationName(componentType, instrumentation.OperationContext{
		ext.RPCSystem: tr.Kind().String(),
	})
}

func splitOperation(operation string) (service, method string) {
	operation = strings.TrimPrefix(operation, "/")
	service, method, _ = strings.Cut(operation, "/")
	return service, method
}

type headerCarrier struct {
	transport.Header
}

func (c headerCarrier) ForeachKey(handler func(key, val string) error) error {
	for _, key := range c.Keys() {
		for _, value := range c.Values(key) {
			if err := handler(key, value); err != nil {
				return err
			}
		}
	}
	return nil
}
