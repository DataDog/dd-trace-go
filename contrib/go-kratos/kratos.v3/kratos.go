// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package kratos provides tracing middleware for Kratos v3 request/response
// HTTP and unary gRPC clients and servers. Transport metadata and extracted
// context are applied before caller-supplied span options, so explicit options
// retain precedence.
package kratos // import "github.com/DataDog/dd-trace-go/contrib/go-kratos/kratos.v3/v2"

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/status"
)

// The named package key also coordinates unary span deduplication with the
// generic gRPC interceptors. They consume the direction/method marker once.
const component = instrumentation.PackageGoKratosV3

// A structural interface avoids importing the HTTP transport, allowing
// Orchestrion to inject this middleware into transport constructors.
type httpTransporter interface {
	transport.Transporter
	Request() *http.Request
	PathTemplate() string
}

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
	prepareSpanConfig(cfg, ext.SpanKindServer)

	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (reply any, err error) {
			tr, ok := transport.FromServerContext(ctx)
			if !ok {
				return handler(ctx, req)
			}

			var inferredSpan *tracer.Span
			if tr.Kind() == transport.KindHTTP {
				if httpTr, ok := tr.(httpTransporter); ok && httpTr.Request() != nil {
					inferredSpan, ctx = httptrace.StartInferredSpanFromRequest(ctx, httpTr.Request())
				}
			}

			tags := startSpanTags(cfg, tr, ext.SpanKindServer)
			var parentCtx *tracer.SpanContext
			if header := tr.RequestHeader(); header != nil {
				if spanctx, extractErr := tracer.Extract(headerCarrier{Header: header}); extractErr == nil {
					parentCtx = spanctx
					if spanctx != nil {
						var items map[string]string
						spanctx.ForeachBaggageItem(func(key, value string) bool {
							if items == nil {
								items = make(map[string]string)
							}
							items[key] = value
							return true
						})
						if len(items) > 0 {
							ctx = baggage.SetAll(ctx, items)
							if tr.Kind() == transport.KindHTTP {
								httptrace.SetBaggageTags(tags, items)
							}
						}
					}
				}
			}

			spanOpts := startSpanOptions(cfg, tags)
			if inferredSpan != nil {
				spanOpts = append(spanOpts, tracer.ChildOf(inferredSpan.Context()))
			} else if parentCtx != nil {
				if links := parentCtx.SpanLinks(); links != nil {
					spanOpts = append(spanOpts, tracer.WithSpanLinks(links))
				}
				spanOpts = append(spanOpts, tracer.ChildOf(parentCtx))
			}
			spanOpts = append(spanOpts, cfg.spanOpts...)

			span, spanCtx := tracer.StartSpanFromContext(ctx, operationName(tr, instrumentation.ComponentServer), spanOpts...)
			if tr.Kind() == transport.KindGRPC {
				spanCtx = context.WithValue(spanCtx, component, ext.SpanKindServer+":"+tr.Operation())
			}
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
	prepareSpanConfig(cfg, ext.SpanKindClient)

	return func(handler middleware.Handler) middleware.Handler {
		return func(ctx context.Context, req any) (reply any, err error) {
			tr, ok := transport.FromClientContext(ctx)
			if !ok {
				return handler(ctx, req)
			}

			spanOpts := startSpanOptions(cfg, startSpanTags(cfg, tr, ext.SpanKindClient))
			spanOpts = append(spanOpts, cfg.spanOpts...)
			span, spanCtx := tracer.StartSpanFromContext(ctx, operationName(tr, instrumentation.ComponentClient), spanOpts...)
			if tr.Kind() == transport.KindGRPC {
				spanCtx = context.WithValue(spanCtx, component, ext.SpanKindClient+":"+tr.Operation())
			}
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

func startSpanTags(cfg *config, tr transport.Transporter, spanKind string) map[string]any {
	tags := make(map[string]any, 12)
	tags[ext.ResourceName] = resourceName(tr)
	tags[ext.RPCSystem] = "kratos"

	service, method := splitOperation(tr.Operation())
	if service != "" {
		tags[ext.RPCService] = service
	}
	if method != "" {
		tags[ext.RPCMethod] = method
	}

	if tr.Kind() == transport.KindHTTP {
		if httpTr, ok := tr.(httpTransporter); ok && httpTr.Request() != nil {
			req := httpTr.Request()
			spanType := ext.SpanTypeHTTP
			httpURL := httptrace.URLFromClientRequest(req, cfg.queryString)
			if spanKind == ext.SpanKindServer {
				spanType = ext.SpanTypeWeb
				httpURL = httptrace.URLFromRequest(req, cfg.queryString)
			}
			tags[ext.SpanType] = spanType
			tags[ext.HTTPMethod] = req.Method
			tags[ext.HTTPURL] = httpURL
			httptrace.SetHeaderTagsFromRequest(tags, req, cfg.headerTags)
			if spanKind == ext.SpanKindServer {
				tags[ext.HTTPUserAgent] = req.UserAgent()
				appsechttpsec.SetSecurityTestingHeaderTags(tags, req.Header)
				httptrace.SetClientIPTagsFromRequest(tags, req)
				if req.Host != "" {
					tags["http.host"] = req.Host
				}
			} else {
				tags[ext.NetworkDestinationName] = req.URL.Hostname()
				if port, err := strconv.Atoi(req.URL.Port()); err == nil {
					tags[ext.NetworkDestinationPort] = port
				}
			}
			if route := httpTr.PathTemplate(); route != "" {
				tags[ext.HTTPRoute] = route
			}
			if cfg.otelEnabled {
				setOTelHTTPTags(tags, req, httpTr.PathTemplate(), spanKind, cfg.queryString)
			}
		}
	} else if tr.Kind() == transport.KindGRPC {
		tags[ext.SpanType] = ext.AppTypeRPC
		tags[ext.GRPCFullMethod] = tr.Operation()
		if spanKind == ext.SpanKindClient {
			host, port := endpointHostPort(tr.Endpoint())
			if host != "" {
				tags[ext.PeerHostname] = host
				tags[ext.TargetHost] = host
			}
			if port != "" {
				tags[ext.TargetPort] = port
			}
		}
	}

	return tags
}

func setOTelHTTPTags(tags map[string]any, req *http.Request, route, spanKind string, queryString bool) {
	method, token, original := httptrace.NormalizeHTTPMethod(req.Method)
	tags[ext.HTTPRequestMethod] = method
	if original != "" {
		tags[ext.HTTPRequestMethodOriginal] = original
	}
	delete(tags, ext.HTTPMethod)
	delete(tags, ext.HTTPURL)
	delete(tags, "http.host")
	delete(tags, ext.HTTPUserAgent)
	if spanKind == ext.SpanKindClient {
		tags[ext.ResourceName] = token
		tags[ext.URLFull] = httptrace.URLFullFromClientRequest(req, queryString)
		address, port := httptrace.ServerAddressPortFromClientRequest(req)
		if address != "" {
			tags[ext.ServerAddress] = address
		}
		if port > 0 {
			tags[ext.ServerPort] = port
		}
		delete(tags, ext.NetworkDestinationName)
		delete(tags, ext.NetworkDestinationPort)
		return
	}
	tags[ext.ResourceName] = httptrace.ServerSpanName(req.Method, route)
	scheme := "http"
	if req.TLS != nil {
		scheme = "https"
	}
	tags[ext.URLScheme] = scheme
	if req.URL != nil {
		tags[ext.URLPath] = req.URL.Path
		if fullURL, err := url.Parse(httptrace.URLFromRequest(req, queryString)); err == nil && fullURL.RawQuery != "" {
			tags[ext.URLQuery] = fullURL.RawQuery
		}
	}
	if req.UserAgent() != "" {
		tags[ext.UserAgentOriginal] = req.UserAgent()
	}
	host := req.Host
	if req.URL != nil && req.URL.Host != "" {
		host = req.URL.Host
	}
	authority := &http.Request{URL: &url.URL{Scheme: scheme, Host: host}}
	address, port := httptrace.ServerAddressPortFromClientRequest(authority)
	if address != "" {
		tags[ext.ServerAddress] = address
	}
	if port > 0 && !((port == 80 && scheme == "http") || (port == 443 && scheme == "https")) {
		tags[ext.ServerPort] = port
	}
	if ip, ok := tags[ext.HTTPClientIP]; ok {
		tags[ext.ClientAddress] = ip
	}
	if ip, ok := tags[ext.NetworkClientIP]; ok {
		tags[ext.NetworkPeerAddress] = ip
	}
	delete(tags, ext.HTTPClientIP)
	delete(tags, ext.NetworkClientIP)
}

func startSpanOptions(cfg *config, tags map[string]any) []tracer.StartSpanOption {
	spanOpts := make([]tracer.StartSpanOption, 0, 3)
	spanOpts = append(spanOpts, tracer.WithTags(tags))
	spanOpts = append(spanOpts, tracer.WithStartSpanConfig(cfg.spanConfig))
	if cfg.serviceNameOption != nil {
		spanOpts = append(spanOpts, cfg.serviceNameOption)
	}
	return spanOpts
}

func prepareSpanConfig(cfg *config, spanKind string) {
	staticOpts := []tracer.StartSpanOption{
		tracer.Tag(ext.Component, string(component)),
		tracer.Tag(ext.SpanKind, spanKind),
	}
	if spanKind == ext.SpanKindServer {
		staticOpts = append(staticOpts, tracer.Measured())
	}
	if cfg.serviceNameStatic {
		staticOpts = append(staticOpts, instrumentation.ServiceNameWithSource(cfg.serviceName.String(), cfg.serviceSource))
	} else {
		// The default service can be configured when the tracer starts, after
		// applications have constructed their middleware. Cache the option but
		// keep resolving the value lazily until the tracer is initialized.
		serviceName := cfg.serviceName
		serviceSource := cfg.serviceSource
		cfg.serviceNameOption = func(spanCfg *tracer.StartSpanConfig) {
			spanCfg.Tags[ext.KeyServiceSource] = instrumentation.ServiceOverride{
				Name:   serviceName.String(),
				Source: serviceSource,
			}
		}
	}
	cfg.spanConfig = tracer.NewStartSpanConfig(staticOpts...)
}

func finishSpan(span *tracer.Span, tr transport.Transporter, err error, spanKind string, cfg *config) {
	if tr.Kind() == transport.KindGRPC {
		if errors.Is(err, io.EOF) {
			err = nil
		}
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
		if tr.Kind() == transport.KindHTTP && cfg.otelEnabled {
			setHTTPStatus(span, http.StatusOK, cfg)
		}
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
			setHTTPStatus(span, statusCode, cfg)
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
		if tr.Kind() == transport.KindHTTP && cfg.otelEnabled && hasProtocolStatus {
			span.SetTag(ext.Error, err)
			span.SetTag(ext.ErrorType, strconv.Itoa(int(kratosErr.Code)))
			if cfg.noDebugStack {
				span.SetTag("error.stack", "")
			}
			span.Finish()
			return
		}
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
	setHTTPStatus(span, statusCode, cfg)
	if !cfg.isStatusError(statusCode) {
		span.Finish()
		return
	}
	if cfg.otelEnabled {
		span.SetTag(ext.Error, err)
		span.SetTag(ext.ErrorType, strconv.Itoa(statusCode))
		if cfg.noDebugStack {
			span.SetTag("error.stack", "")
		}
		span.Finish()
		return
	}
	finishOpts := []tracer.FinishOption{tracer.WithError(err)}
	if cfg.noDebugStack {
		finishOpts = append(finishOpts, tracer.NoDebugStack())
	}
	span.Finish(finishOpts...)
}

func setHTTPStatus(span *tracer.Span, statusCode int, cfg *config) {
	key := ext.HTTPCode
	if cfg.otelEnabled {
		key = ext.HTTPResponseStatusCode
		if cfg.isStatusError(statusCode) {
			span.SetTag(ext.ErrorType, strconv.Itoa(statusCode))
		}
	}
	span.SetTag(key, strconv.Itoa(statusCode))
}

func operationName(_ transport.Transporter, componentType instrumentation.Component) string {
	if componentType == instrumentation.ComponentClient {
		return "kratos.client.request"
	}
	return "kratos.server.request"
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
		if httpTr, ok := tr.(httpTransporter); ok && httpTr.Request() != nil && httpTr.PathTemplate() != "" {
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
