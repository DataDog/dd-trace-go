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
	"reflect"
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
//
// Root-ness is checked on the span itself, via Span.Root(), after the single real call to
// tracer.StartSpanFromContext below — not by a separate pre-evaluation of opts or cfg.spanOpts.
// Both can include arbitrary, potentially stateful StartSpanOptions (e.g. from
// WithSpanOptions), and there's no way to know whether one sets a parent without invoking it;
// invoking it a second time just to check isn't safe unless it's known to be idempotent.
// Callers must not reuse the same opts (in particular, an extracted-header ChildOf) across
// multiple message spans: dd-trace-go backfills a trace onto an extracted SpanContext the
// first time it parents a real span, which would make Root() disagree between sibling spans
// sharing that same SpanContext value. streamingHandlerConn re-extracts fresh per message
// instead of caching a shared parent option for this reason.
func (cfg *config) startMessageSpan(ctx context.Context, spec connectrpc.Spec, protocol string, component instrumentation.Component, opts ...tracer.StartSpanOption) *tracer.Span {
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
	if component == instrumentation.ComponentServer {
		// Matches contrib/google.golang.org/grpc's server message spans: even as children of
		// the call span (so not top-level), their per-message latency should still count
		// toward trace stats.
		opts = append(opts, tracer.Measured())
	}
	span, _ := tracer.StartSpanFromContext(ctx, "connect.message", cfg.startSpanOptions(opts...)...)
	if span.Root() == span {
		span.SetTag(ext.SpanKind, spanKind(component))
	}
	return span
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
		// A matched-but-nil *connectrpc.Error (e.g. a handler normally returning
		// (*connectrpc.Error)(nil)) would panic on Code(), and every one of its other methods
		// dereferences the same nil receiver, so err isn't safe to pass to errors.Is below either
		// — it can reach *connectrpc.Error.Unwrap via the chain walk. Return early instead.
		if connectErr == nil {
			return connectrpc.CodeUnknown
		}
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

// isExpectedStreamEOF reports whether err is nothing more than the sentinel signal that a
// stream ended normally. A *connectrpc.Error is always a deliberate RPC-level error, even when
// its explicit code is CodeUnknown or it happens to wrap io.EOF as its cause, so only a bare
// (uncoded) error chain containing io.EOF counts as expected termination.
func isExpectedStreamEOF(err error) bool {
	if _, ok := errors.AsType[*connectrpc.Error](err); ok {
		return false
	}
	return errors.Is(err, io.EOF)
}

// isSuppressedTerminalError reports whether err's code is one finishSpan would drop as a
// non-error, so a stream's terminal-error bookkeeping can let a later, more meaningful error
// replace an earlier one that would end up suppressed anyway. This deliberately checks only the
// code-based rules finishSpan applies (the default uncoded-context.Canceled rule and
// cfg.nonErrorCodes; the stream-EOF and unary-GET-not-modified cases don't apply here: callers
// already filter EOF before storing a terminal error, and streams have no not-modified
// responses). cfg.errCheck is intentionally excluded: it's documented as running once, when the
// span actually finishes, and calling it speculatively here to compare candidates could invoke a
// side-effecting user callback extra times per RPC.
func isSuppressedTerminalError(err error, cfg *config) bool {
	if err == nil {
		return true
	}
	code := codeOf(err)
	_, hasConnectError := errors.AsType[*connectrpc.Error](err)
	if code == connectrpc.CodeCanceled && !hasConnectError && errors.Is(err, context.Canceled) {
		return true
	}
	return cfg.nonErrorCodes[code]
}

// shouldReplaceTerminalError decides whether candidate should replace a stream's currently
// stored terminal error. A panic always wins, and a stored panic is never displaced by a later
// non-panic. Otherwise this relies only on isSuppressedTerminalError's code-based rules (never
// cfg.errCheck, which is meant to run at most once per RPC — see isSuppressedTerminalError):
// a code-suppressed stored error is always replaced (even by another code-suppressed one, which
// is harmless since finishSpan drops both the same way), but a code-non-suppressed ("real")
// stored error is only replaced by another candidate that's also real by code. Concurrent
// Send/Receive can each finish with a different, equally "real" by-code error; since
// cfg.errCheck might accept one and reject the other, always keeping whichever arrived first
// would let a callback-rejected error permanently hide a later genuine failure, so the newer
// candidate wins that case instead.
func shouldReplaceTerminalError(storedErr error, storedIsPanic bool, candidate error, isPanic bool, cfg *config) bool {
	switch {
	case storedErr == nil, isPanic:
		return true
	case storedIsPanic:
		return false
	case isSuppressedTerminalError(storedErr, cfg):
		return true
	default:
		return !isSuppressedTerminalError(candidate, cfg)
	}
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
	if isNilError(err) || hasNilConnectError(err) {
		// Covers two variants of the same trap: err itself is a nil-valued error interface (e.g.
		// a normally returned (*connectrpc.Error)(nil)), or err is a legitimate non-nil wrapper
		// whose Unwrap chain contains a nil *connectrpc.Error (e.g. a custom error type storing
		// one as its cause). Either way, codeOf, connectrpc.IsNotModifiedError,
		// setErrorDetailTags, and eventually span.Finish's own error tagging (via
		// tracer.WithError) can call a method on that nil value (Unwrap, Details, Error, Code)
		// and panic. Replace err with a safe stand-in before any of that runs.
		err = fmt.Errorf("connect: error value (%T) is unsafe to inspect: a nil error, or one with a nil *connectrpc.Error in its chain", err)
	}

	code := codeOf(err)
	_, hasConnectError := errors.AsType[*connectrpc.Error](err)
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
		if expectedEOF || (code == connectrpc.CodeCanceled && !hasConnectError && errors.Is(err, context.Canceled)) || notModified || cfg.nonErrorCodes[code] {
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
	if !errors.As(err, &connectErr) || connectErr == nil {
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
	if err, ok := value.(error); ok && !isNilError(err) {
		return err
	}
	return fmt.Errorf("panic: %v", value)
}

// isNilError reports whether err is a non-nil error interface wrapping a nil value, e.g. a
// value recovered from panic((*connectrpc.Error)(nil)). Such an error compares != nil and
// passes a type assertion to error, but calling any of its methods (as codeOf and
// isExpectedStreamEOF do) dereferences the nil receiver and panics again, replacing the
// original panic and leaving the span unfinished.
func isNilError(err error) bool {
	v := reflect.ValueOf(err)
	switch v.Kind() {
	case reflect.Ptr, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func, reflect.Interface:
		return v.IsNil()
	default:
		return false
	}
}

// hasNilConnectError reports whether err's chain contains a *connectrpc.Error whose value is
// nil, e.g. a custom wrapper error whose Unwrap returns (*connectrpc.Error)(nil) as its cause.
// Unlike isNilError, which only catches err itself being nil-valued, this catches the same trap
// one level deeper: errors.AsType happily matches the nil pointer without dereferencing it, but
// codeOf, setErrorDetailTags, and connectrpc.IsNotModifiedError's Unwrap chain-walk all call one
// of its methods and panic.
func hasNilConnectError(err error) bool {
	connectErr, ok := errors.AsType[*connectrpc.Error](err)
	return ok && connectErr == nil
}
