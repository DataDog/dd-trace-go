// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

// Package connect provides tracing for connectrpc.com/connect clients and handlers.
package connect // import "github.com/DataDog/dd-trace-go/contrib/connectrpc.com/connect/v2"

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"

	connectrpc "connectrpc.com/connect"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/runtime/protoimpl"
)

const (
	componentName = "connectrpc.com/connect"

	tagMethodKind          = "rpc.method.kind"
	tagConnectErrorCode    = "rpc.connect_rpc.error_code"
	tagGRPCStatusCode      = "rpc.grpc.status_code"
	tagConnectRequest      = "connect.request"
	tagGRPCRequest         = "grpc.request"
	tagStatusDetailsPrefix = "status_details."

	methodKindUnary        = "unary"
	methodKindClientStream = "client_streaming"
	methodKindServerStream = "server_streaming"
	methodKindBidiStream   = "bidi_streaming"
)

var (
	instr       *instrumentation.Instrumentation
	spanTypeRPC = tracer.SpanType(ext.AppTypeRPC)
)

func init() {
	instr = instrumentation.Load(instrumentation.PackageConnectRPC)
}

func (cfg *config) startCallSpan(ctx context.Context, spec connectrpc.Spec, component instrumentation.Component, opts ...tracer.StartSpanOption) (*tracer.Span, context.Context) {
	service, method := parseProcedure(spec.Procedure)
	serviceName, serviceSource := cfg.service(component)
	opts = append(opts,
		instrumentation.ServiceNameWithSource(serviceName, serviceSource),
		tracer.ResourceName(spec.Procedure),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.SpanKind, spanKind(component)),
		tracer.Tag(ext.RPCService, service),
		tracer.Tag(ext.RPCMethod, method),
		tracer.Tag(tagMethodKind, methodKind(spec.StreamType)),
		spanTypeRPC,
		tracer.Measured(),
	)
	return tracer.StartSpanFromContext(ctx, instr.OperationName(component, nil), cfg.startSpanOptions(opts...)...)
}

// startMessageSpan starts a span for a single streaming message. When WithStreamCalls(false)
// leaves no call span to parent them (e.g. no caller span on the client, or no extracted
// parent on the server), these spans become trace roots and must carry span.kind like any
// other root RPC span would. Otherwise they're plain internal children of the call span, so
// span.kind is omitted, matching contrib/google.golang.org/grpc's message spans.
func (cfg *config) startMessageSpan(ctx context.Context, spec connectrpc.Spec, protocol string, component instrumentation.Component, opts ...tracer.StartSpanOption) *tracer.Span {
	root := isRootSpan(ctx, opts)
	service, method := parseProcedure(spec.Procedure)
	serviceName, serviceSource := cfg.service(component)
	opts = append(opts,
		instrumentation.ServiceNameWithSource(serviceName, serviceSource),
		tracer.ResourceName(spec.Procedure),
		tracer.Tag(ext.Component, componentName),
		tracer.Tag(ext.RPCSystem, rpcSystem(protocol)),
		tracer.Tag(ext.RPCService, service),
		tracer.Tag(ext.RPCMethod, method),
		tracer.Tag(tagMethodKind, methodKind(spec.StreamType)),
		spanTypeRPC,
	)
	if root {
		opts = append(opts, tracer.Tag(ext.SpanKind, spanKind(component)))
	}
	span, _ := tracer.StartSpanFromContext(ctx, "connect.message", cfg.startSpanOptions(opts...)...)
	return span
}

// isRootSpan reports whether a span started now, with the given extra options, would have no
// parent: no ambient span in ctx (e.g. no call span, no caller-supplied span), and no
// caller-supplied parent option (e.g. a ChildOf extracted from propagated headers).
func isRootSpan(ctx context.Context, opts []tracer.StartSpanOption) bool {
	if len(opts) > 0 {
		return false
	}
	_, ok := tracer.SpanFromContext(ctx)
	return !ok
}

func spanKind(component instrumentation.Component) string {
	if component == instrumentation.ComponentClient {
		return ext.SpanKindClient
	}
	return ext.SpanKindServer
}

func (cfg *config) startSpanOptions(opts ...tracer.StartSpanOption) []tracer.StartSpanOption {
	if len(cfg.spanOpts) == 0 && len(cfg.tags) == 0 && math.IsNaN(cfg.analyticsRate) {
		return opts
	}

	count := len(opts) + len(cfg.spanOpts) + len(cfg.tags)
	if !math.IsNaN(cfg.analyticsRate) {
		count++
	}
	spanOpts := make([]tracer.StartSpanOption, 0, count)
	spanOpts = append(spanOpts, opts...)
	if !math.IsNaN(cfg.analyticsRate) {
		spanOpts = append(spanOpts, tracer.AnalyticsRate(cfg.analyticsRate))
	}
	spanOpts = append(spanOpts, cfg.spanOpts...)
	for key, value := range cfg.tags {
		spanOpts = append(spanOpts, tracer.Tag(key, value))
	}
	return spanOpts
}

func parseProcedure(procedure string) (service, method string) {
	parts := strings.SplitN(strings.TrimPrefix(procedure, "/"), "/", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return parts[0], ""
}

func methodKind(streamType connectrpc.StreamType) string {
	switch streamType {
	case connectrpc.StreamTypeUnary:
		return methodKindUnary
	case connectrpc.StreamTypeClient:
		return methodKindClientStream
	case connectrpc.StreamTypeServer:
		return methodKindServerStream
	case connectrpc.StreamTypeBidi:
		return methodKindBidiStream
	default:
		return "unknown"
	}
}

func rpcSystem(protocol string) string {
	if protocol == connectrpc.ProtocolGRPC || protocol == connectrpc.ProtocolGRPCWeb {
		return ext.RPCSystemGRPC
	}
	return ext.RPCSystemConnectRPC
}

func setProtocolTag(span *tracer.Span, protocol string) {
	span.SetTag(ext.RPCSystem, rpcSystem(protocol))
}

func codeOf(err error) connectrpc.Code {
	if connectErr, ok := errors.AsType[*connectrpc.Error](err); ok {
		return connectErr.Code()
	}
	switch {
	case errors.Is(err, context.Canceled):
		return connectrpc.CodeCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return connectrpc.CodeDeadlineExceeded
	default:
		return connectrpc.CodeOf(err)
	}
}

func isExpectedStreamEOF(err error) bool {
	return errors.Is(err, io.EOF) && codeOf(err) == connectrpc.CodeUnknown
}

func finishCall(span *tracer.Span, err error, procedure, protocol string, cfg *config) {
	finishSpan(span, err, procedure, protocol, false, false, false, cfg)
}

func finishCallOnPanic(span *tracer.Span, err error, procedure, protocol string, cfg *config) {
	finishSpan(span, err, procedure, protocol, false, false, true, cfg)
}

func finishMessage(span *tracer.Span, err error, procedure, protocol string, cfg *config) {
	finishSpan(span, err, procedure, protocol, true, false, false, cfg)
}

func finishMessageOnPanic(span *tracer.Span, err error, procedure, protocol string, cfg *config) {
	finishSpan(span, err, procedure, protocol, true, false, true, cfg)
}

func finishUnary(span *tracer.Span, err error, procedure, protocol, httpMethod string, cfg *config) {
	allowNotModified := rpcSystem(protocol) == ext.RPCSystemConnectRPC && httpMethod == http.MethodGet
	finishSpan(span, err, procedure, protocol, false, allowNotModified, false, cfg)
}

func finishUnaryOnPanic(span *tracer.Span, err error, procedure, protocol, httpMethod string, cfg *config) {
	allowNotModified := rpcSystem(protocol) == ext.RPCSystemConnectRPC && httpMethod == http.MethodGet
	finishSpan(span, err, procedure, protocol, false, allowNotModified, true, cfg)
}

// finishSpan finishes span, tagging it based on err. allowEOF and allowNotModified permit
// treating a stream EOF or Connect's unary-GET "not modified" response as non-errors; isPanic
// forces the opposite: a recovered panic must always be recorded as an error, even if the
// panic value happens to be one that would otherwise be filtered out (e.g. context.Canceled,
// a non-error code, or a value errCheck classifies as not an error), since the panic is always
// re-raised by the caller after this returns and must stay visible in the trace.
func finishSpan(span *tracer.Span, err error, procedure, protocol string, allowEOF, allowNotModified, isPanic bool, cfg *config) {
	if span == nil {
		return
	}

	code := codeOf(err)
	expectedEOF := !isPanic && allowEOF && isExpectedStreamEOF(err)
	notModified := !isPanic && allowNotModified && connectrpc.IsNotModifiedError(err)
	if rpcSystem(protocol) == ext.RPCSystemGRPC {
		statusCode := uint32(0)
		if err != nil && !expectedEOF {
			statusCode = uint32(code)
		}
		span.SetTag(tagGRPCStatusCode, statusCode)
	} else if err != nil && !expectedEOF && !notModified {
		span.SetTag(tagConnectErrorCode, code.String())
	}

	if !isPanic {
		if expectedEOF || (code == connectrpc.CodeCanceled && errors.Is(err, context.Canceled)) || notModified || cfg.nonErrorCodes[code] {
			err = nil
		} else if err != nil && cfg.errCheck != nil && !cfg.errCheck(procedure, err) {
			err = nil
		}
	}
	if notModified {
		span.SetTag(ext.HTTPCode, 304)
	}
	if err != nil && cfg.withErrorDetailTags {
		setErrorDetailTags(span, err, protocol)
	}

	var finishOpts []tracer.FinishOption
	if err != nil {
		finishOpts = append(finishOpts, tracer.WithError(err))
		if cfg.noDebugStack {
			finishOpts = append(finishOpts, tracer.NoDebugStack())
		}
	}
	span.Finish(finishOpts...)
}

func setErrorDetailTags(span *tracer.Span, err error, protocol string) {
	var connectErr *connectrpc.Error
	if !errors.As(err, &connectErr) {
		return
	}
	prefix := "connect." + tagStatusDetailsPrefix
	if rpcSystem(protocol) == ext.RPCSystemGRPC {
		prefix = "grpc." + tagStatusDetailsPrefix
	}
	for i, detail := range connectErr.Details() {
		message, detailErr := detail.Value()
		if detailErr != nil {
			continue
		}
		span.SetTag(prefix+"_"+strconv.Itoa(i), protoimpl.X.MessageStringOf(message))
	}
}

func withHeaderTags(cfg *config, headers http.Header, protocol string, span *tracer.Span) {
	if !cfg.withHeaderTags || span == nil {
		return
	}
	prefix := "rpc.connect_rpc.request.metadata."
	if rpcSystem(protocol) == ext.RPCSystemGRPC {
		prefix = "rpc.grpc.request.metadata."
	}
	for key, values := range headers {
		key = strings.ToLower(key)
		if _, ignored := cfg.ignoredHeaders[key]; ignored || strings.HasSuffix(key, "-bin") || strings.HasPrefix(key, tracer.DefaultBaggageHeaderPrefix) {
			continue
		}
		span.SetTag(prefix+key, values)
	}
}

func withRequestTags(cfg *config, request any, protocol string, span *tracer.Span) {
	if !cfg.withRequestTags || span == nil {
		return
	}
	message, ok := request.(proto.Message)
	if !ok {
		return
	}
	encoded, err := protojson.Marshal(message)
	if err != nil {
		return
	}
	tag := tagConnectRequest
	if rpcSystem(protocol) == ext.RPCSystemGRPC {
		tag = tagGRPCRequest
	}
	span.SetTag(tag, string(encoded))
}

func propagationOptions(headers http.Header) []tracer.StartSpanOption {
	spanContext, err := tracer.Extract(tracer.HTTPHeadersCarrier(headers))
	if err != nil || spanContext == nil {
		return nil
	}
	opts := []tracer.StartSpanOption{tracer.ChildOf(spanContext)}
	if links := spanContext.SpanLinks(); links != nil {
		opts = append(opts, tracer.WithSpanLinks(links))
	}
	return opts
}

func injectSpan(ctx context.Context, headers http.Header) {
	span, ok := tracer.SpanFromContext(ctx)
	if !ok {
		return
	}
	if err := tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(headers)); err != nil {
		instr.Logger().Warn("contrib/connectrpc.com/connect: failed to inject trace context: %s", err.Error())
	}
}

func setPeerTags(span *tracer.Span, peer connectrpc.Peer) {
	host, port, err := net.SplitHostPort(peer.Addr)
	if err != nil {
		host = peer.Addr
		port = ""
	}
	host = strings.Trim(host, "[]")
	if ip := net.ParseIP(host); ip != nil {
		span.SetTag(ext.NetworkDestinationIP, ip.String())
	} else if host != "" {
		span.SetTag(ext.NetworkDestinationName, host)
	}
	if port != "" {
		if portNumber, err := strconv.Atoi(port); err == nil {
			span.SetTag(ext.NetworkDestinationPort, portNumber)
		}
	}
}

func panicError(value any) error {
	if err, ok := value.(error); ok {
		return err
	}
	return fmt.Errorf("panic: %v", value)
}
