// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package kratos provides tracing middleware for Kratos v3 request/response
// HTTP and unary gRPC clients and servers.
package kratos // import "github.com/DataDog/dd-trace-go/contrib/go-kratos/kratos.v3/v2"

import (
	"context"
	"errors"
	"math"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
	appsechttpsec "github.com/DataDog/dd-trace-go/v2/instrumentation/appsec/httpsec"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/httptrace"

	kratoserrors "github.com/go-kratos/kratos/v3/errors"
	"github.com/go-kratos/kratos/v3/middleware"
	"github.com/go-kratos/kratos/v3/transport"
	kratoshttp "github.com/go-kratos/kratos/v3/transport/http"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/resolver"
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

			var inferredSpan *tracer.Span
			if tr.Kind() == transport.KindHTTP {
				if httpTr, ok := tr.(kratoshttp.Transporter); ok && httpTr.Request() != nil {
					inferredSpan, ctx = httptrace.StartInferredSpanFromRequest(ctx, httpTr.Request())
				}
			}

			spanOpts := startSpanOptions(cfg, tr, ext.SpanKindServer)
			if header := tr.RequestHeader(); header != nil {
				if spanctx, extractErr := tracer.Extract(headerCarrier{Header: header}); extractErr == nil {
					if spanctx != nil {
						items := make(map[string]string)
						spanctx.ForeachBaggageItem(func(key, value string) bool {
							items[key] = value
							return true
						})
						ctx = baggage.SetAll(ctx, items)
						if tr.Kind() == transport.KindHTTP {
							spanOpts = append(spanOpts, httptrace.BaggageTags(items))
						}
						if inferredSpan == nil {
							if links := spanctx.SpanLinks(); links != nil {
								spanOpts = append(spanOpts, tracer.WithSpanLinks(links))
							}
						}
					}
					if inferredSpan == nil {
						spanOpts = append(spanOpts, tracer.ChildOf(spanctx))
					}
				}
			}
			if inferredSpan != nil {
				spanOpts = append(spanOpts, tracer.ChildOf(inferredSpan.Context()))
			}

			span, spanCtx := tracer.StartSpanFromContext(ctx, operationName(tr, instrumentation.ComponentServer), spanOpts...)
			defer func() {
				finishSpan(span, tr, err, ext.SpanKindServer, cfg)
				if inferredSpan != nil {
					finishInferredHTTPSpan(inferredSpan, err, cfg)
				}
			}()
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
			defer func() { finishSpan(span, tr, err, ext.SpanKindClient, cfg) }()
			for key, value := range baggage.All(ctx) {
				span.SetBaggageItem(key, value)
			}
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
		instrumentation.ServiceNameWithSource(cfg.serviceName.String(), cfg.serviceSource),
		tracer.ResourceName(resourceName(tr)),
		tracer.Tag(ext.Component, component),
		tracer.Tag(ext.SpanKind, spanKind),
		tracer.Tag(ext.RPCSystem, tr.Kind().String()),
	)
	if !math.IsNaN(cfg.analyticsRate) {
		spanOpts = append(spanOpts, tracer.Tag(ext.EventSampleRate, cfg.analyticsRate))
	}
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
			httpURL := httptrace.URLFromClientRequest(req, cfg.queryString)
			if spanKind == ext.SpanKindServer {
				spanType = ext.SpanTypeWeb
				httpURL = httptrace.URLFromRequest(req, cfg.queryString)
			}
			spanOpts = append(spanOpts,
				tracer.SpanType(spanType),
				tracer.Tag(ext.HTTPMethod, req.Method),
				tracer.Tag(ext.HTTPURL, httpURL),
				httptrace.HeaderTagsFromRequest(req, cfg.headerTags),
			)
			if spanKind == ext.SpanKindServer {
				spanOpts = append(spanOpts,
					httptrace.ClientIPTagsFromRequest(req),
					tracer.Tag(ext.HTTPUserAgent, req.UserAgent()),
				)
				spanOpts = appsechttpsec.AppendSecurityTestingHeaderTags(spanOpts, req.Header)
				if req.Host != "" {
					spanOpts = append(spanOpts, tracer.Tag("http.host", req.Host))
				}
			} else {
				spanOpts = append(spanOpts, tracer.Tag(ext.NetworkDestinationName, req.URL.Hostname()))
				if port, err := strconv.Atoi(req.URL.Port()); err == nil {
					spanOpts = append(spanOpts, tracer.Tag(ext.NetworkDestinationPort, port))
				}
			}
			if route := httpTr.PathTemplate(); route != "" {
				spanOpts = append(spanOpts, tracer.Tag(ext.HTTPRoute, route))
			}
		}
	} else if tr.Kind() == transport.KindGRPC {
		spanOpts = append(spanOpts,
			tracer.SpanType(ext.AppTypeRPC),
			tracer.Tag(ext.GRPCFullMethod, tr.Operation()),
		)
		if spanKind == ext.SpanKindClient {
			host, port := endpointHostPort(tr.Endpoint())
			if host != "" {
				spanOpts = append(spanOpts,
					tracer.Tag(ext.PeerHostname, host),
					tracer.Tag(ext.TargetHost, host),
				)
			}
			if port != "" {
				spanOpts = append(spanOpts, tracer.Tag(ext.TargetPort, port))
			}
		}
	}
	return append(spanOpts, cfg.spanOpts...)
}

func finishSpan(span *tracer.Span, tr transport.Transporter, err error, spanKind string, cfg *config) {
	if tr.Kind() == transport.KindGRPC {
		code := status.Code(err)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			code = status.FromContextError(err).Code()
		}
		span.SetTag("grpc.code", code.String())
		if errors.Is(err, context.Canceled) || code == codes.Canceled {
			span.Finish()
			return
		}
	}
	if err == nil {
		span.Finish()
		return
	}

	kratosErr := kratoserrors.FromError(err)
	markError := true
	hasProtocolStatus := tr.Kind() != transport.KindHTTP || spanKind == ext.SpanKindServer
	if tr.Kind() == transport.KindHTTP {
		// A client-side transport failure has no response status to classify and
		// must remain an error. Kratos HTTP response errors use *kratoserrors.Error.
		var responseErr *kratoserrors.Error
		if spanKind == ext.SpanKindClient {
			hasProtocolStatus = errors.As(err, &responseErr)
		}
		if hasProtocolStatus {
			statusCode := int(kratosErr.Code)
			span.SetTag(ext.HTTPCode, strconv.Itoa(statusCode))
			markError = cfg.isStatusError(statusCode)
		}
	}
	if hasProtocolStatus {
		span.SetTag("kratos.status_code", kratosErr.Code)
		if kratosErr.Reason != "" {
			span.SetTag("kratos.error_reason", kratosErr.Reason)
		}
	}
	var finishOpts []tracer.FinishOption
	if markError {
		finishOpts = append(finishOpts, tracer.WithError(err))
		if cfg.noDebugStack {
			finishOpts = append(finishOpts, tracer.NoDebugStack())
		}
	}
	span.Finish(finishOpts...)
}

func finishInferredHTTPSpan(span *tracer.Span, err error, cfg *config) {
	if err == nil {
		span.Finish()
		return
	}
	statusCode := int(kratoserrors.FromError(err).Code)
	span.SetTag(ext.HTTPCode, strconv.Itoa(statusCode))
	if !cfg.isStatusError(statusCode) {
		span.Finish()
		return
	}
	finishOpts := []tracer.FinishOption{tracer.WithError(err)}
	if cfg.noDebugStack {
		finishOpts = append(finishOpts, tracer.NoDebugStack())
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

func resourceName(tr transport.Transporter) string {
	if operation := tr.Operation(); operation != "" {
		return operation
	}
	if tr.Kind() == transport.KindHTTP {
		if httpTr, ok := tr.(kratoshttp.Transporter); ok && httpTr.Request() != nil && httpTr.PathTemplate() != "" {
			return httpTr.Request().Method + " " + httpTr.PathTemplate()
		}
	}
	return "unknown"
}

func endpointHostPort(endpoint string) (host, port string) {
	lowerEndpoint := strings.ToLower(endpoint)
	if strings.HasPrefix(lowerEndpoint, "unix:") || strings.HasPrefix(lowerEndpoint, "unix-abstract:") {
		return "", ""
	}
	target := endpoint
	scheme, _, _ := strings.Cut(lowerEndpoint, ":")
	if strings.Contains(endpoint, "://") || resolver.Get(scheme) != nil {
		parsed, err := url.Parse(endpoint)
		if err != nil {
			return "", ""
		}
		switch {
		case parsed.Opaque != "":
			target = parsed.Opaque
		case parsed.Path != "":
			target = strings.TrimPrefix(parsed.Path, "/")
		default:
			target = parsed.Host
		}
	}
	if target == "" {
		return "", ""
	}
	host, port, err := net.SplitHostPort(target)
	if err == nil {
		return host, port
	}
	return strings.Trim(target, "[]"), ""
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
