// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"encoding/binary"
	"errors"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/log"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

var _ oteltrace.Span = (*span)(nil)

type spanFlags uint8

const (
	flagOTelSemantics spanFlags = 1 << iota
	flagHasHTTPMethod
	flagHasGraphQLOp
	flagHasErrorMsg
	flagHasErrorType
	flagHasErrorStack
	flagOpNameNonString
)

// opNameAttrs lazily holds only the subset of attributes inspected by
// createOperationName when no operation name is set explicitly.
type opNameAttrs struct {
	dbSystem            string
	messagingSystem     string
	messagingOperation  string
	rpcSystem           string
	rpcService          string
	faasInvokedProvider string
	faasInvokedName     string
	faasTrigger         string
	networkProtocolName string
}

type span struct {
	noop.Span              // https://pkg.go.dev/go.opentelemetry.io/otel/trace#hdr-API_Implementations
	mu        sync.RWMutex `msg:"-"` // all fields are protected by this RWMutex
	DD        *tracer.Span
	// ddCtx is the context of DD, saved when the span starts and never changed.
	// With the span pool on, DD can be reused for another span after End, but its
	// old context object is not, so SpanContext reads ddCtx, not DD. Reading it
	// needs no lock, so SpanContext can also run while End holds mu.
	ddCtx *tracer.SpanContext
	*oteltracer
	opAttrs    *opNameAttrs
	opName     string
	finishOpts []tracer.FinishOption
	events     []spanEvent
	statusInfo
	spanKind oteltrace.SpanKind
	finished bool
	flags    spanFlags
}

func (s *span) TracerProvider() oteltrace.TracerProvider {
	if s.oteltracer.provider.bridge {
		// The application can set its own TracerProvider after this span started.
		return otel.GetTracerProvider()
	}
	return s.oteltracer.provider
}

func (s *span) SetName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	if s.flags&flagOTelSemantics != 0 {
		// OTel semantics: the span name maps to the Datadog resource (the OTLP span-name
		// field), not the DD-only operation name. Changing the default would shift RED
		// metrics, so it is gated behind the flag.
		s.DD.SetTag(ext.ResourceName, name)
		return
	}
	s.opName = strings.ToLower(name)
	s.flags &^= flagOpNameNonString
}

// spanEvent holds information about span events
type spanEvent struct {
	name    string
	options []tracer.SpanEventOption
}

func (s *span) End(options ...oteltrace.SpanEndOption) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	s.finished = true
	otelSemantics := s.flags&flagOTelSemantics != 0
	if !otelSemantics {
		// Datadog operation-name handling. Under OTel semantics the span name maps to
		// the resource (the OTLP span-name field) and operation.name is suppressed on
		// export, so we skip this entirely and let the attributes pass through.
		if s.opName != "" {
			s.DD.SetOperationName(s.opName)
		} else if s.flags&flagOpNameNonString == 0 {
			s.DD.SetOperationName(s.createOperationName())
		}
	}
	for _, evt := range s.events {
		s.DD.AddEvent(evt.name, evt.options...)
	}
	var optsBuf [2]tracer.FinishOption
	opts := optsBuf[:0]
	if s.statusInfo.code == otelcodes.Error {
		hasErrorMsg := s.flags&flagHasErrorMsg != 0
		hasErrorType := s.flags&flagHasErrorType != 0
		hasErrorStack := s.flags&flagHasErrorStack != 0
		// With the DD_TRACE_OTEL_SEMANTICS_ENABLED config, error.message only feeds the
		// OTLP status message, so it always takes the status description. Otherwise it
		// is a regular tag, so a user-set value is kept.
		if otelSemantics || !hasErrorMsg {
			s.DD.SetTag(ext.ErrorMsg, s.statusInfo.description)
		}
		if otelSemantics || hasErrorMsg || hasErrorType || hasErrorStack {
			// Mark the span as errored directly. WithError would overwrite already
			// populated attributes, such as error.type. Applies to all export formats:
			// APM spans keep error=1 and error.message, without error.handling_stack.
			// See https://github.com/DataDog/dd-trace-go/issues/3708.
			s.DD.SetTag(ext.Error, true)
		} else {
			opts = append(opts, tracer.WithError(errors.New(s.statusInfo.description)))
		}
	}
	if len(s.finishOpts) != 0 {
		opts = append(opts, s.finishOpts...)
	}
	// If finishOpts has been appended, the following option will be a no-op.
	// This is because finishOpts may contain a FinishTime option, which will be
	// used to set the finish time of the span.
	if len(options) > 0 {
		finishCfg := oteltrace.NewSpanEndConfig(options...)
		if t := finishCfg.Timestamp(); !t.IsZero() {
			opts = append(opts, finishTime(t))
		}
	}
	s.DD.Finish(opts...)
}

func finishTime(t time.Time) tracer.FinishOption {
	return func(cfg *tracer.FinishConfig) {
		if !cfg.FinishTime.IsZero() {
			return
		}
		cfg.FinishTime = t
	}
}

// EndOptions sets tracer.FinishOption on a given span to be executed when span is finished.
func EndOptions(sp oteltrace.Span, options ...tracer.FinishOption) {
	s, ok := sp.(*span)
	if !ok || !s.IsRecording() {
		return
	}
	s.finishOpts = options
}

// SpanContext returns implementation of the oteltrace.SpanContext.
func (s *span) SpanContext() oteltrace.SpanContext {
	return spanContextFromDD(s.ddCtx, true)
}

// ddSpanContext converts the context of a Datadog span to an OpenTelemetry span context.
func ddSpanContext(ddSpan *tracer.Span) oteltrace.SpanContext {
	return spanContextFromDD(ddSpan.Context(), true)
}

// spanContextFromDD converts a Datadog span context to an OpenTelemetry span context.
func spanContextFromDD(ctx *tracer.SpanContext, remote bool) oteltrace.SpanContext {
	var traceID oteltrace.TraceID
	var spanID oteltrace.SpanID
	traceID = ctx.TraceIDBytes()
	uint64ToByte(ctx.SpanID(), spanID[:])
	config := oteltrace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}
	extractTraceData(ctx, &config, remote)
	return oteltrace.NewSpanContext(config)
}

const (
	// traceparentMinLen is the shortest trimmed traceparent that still carries the flags.
	traceparentMinLen = 3
	// otelFlagLen is the length of the flags in the traceparent: 2 hex-encoded digits.
	otelFlagLen = 2
	hexBase     = 16
	flagBits    = 8
)

// traceDataCarrier captures only the W3C traceparent and tracestate headers
// during tracer.Inject, avoiding a map allocation for all other propagated headers.
type traceDataCarrier struct {
	traceparent string
	tracestate  string
}

func (c *traceDataCarrier) Set(key, val string) {
	switch key {
	case "traceparent":
		c.traceparent = val
	case "tracestate":
		c.tracestate = val
	}
}

func extractTraceData(ctx *tracer.SpanContext, c *oteltrace.SpanContextConfig, remote bool) {
	var headers traceDataCarrier
	if err := tracer.Inject(ctx, &headers); err != nil {
		return
	}
	if headers.tracestate != "" {
		state, err := oteltrace.ParseTraceState(headers.tracestate)
		if err != nil {
			log.Debug("Couldn't parse tracestate: %s", err.Error())
			return
		}
		c.TraceState = state
	}
	parent := strings.Trim(headers.traceparent, " \t-")
	if len(parent) > traceparentMinLen {
		// checking the length to avoid panic when parsing
		// The format of the traceparent is `-` separated string,
		// where flags represents the propagated flags in the format of 2 hex-encoded digits at the end of the traceparent.
		hi, okHi := fromHexChar(parent[len(parent)-2])
		lo, okLo := fromHexChar(parent[len(parent)-1])
		if okHi && okLo {
			c.TraceFlags = oteltrace.TraceFlags(hi<<4 | lo)
		} else if f, err := strconv.ParseUint(parent[len(parent)-otelFlagLen:], hexBase, flagBits); err != nil {
			log.Debug("Couldn't parse traceparent: %s", err.Error())
		} else {
			c.TraceFlags = oteltrace.TraceFlags(f)
		}
	}
	// Remote indicates a remotely-created Span
	c.Remote = remote
}

func fromHexChar(c byte) (byte, bool) {
	switch {
	case '0' <= c && c <= '9':
		return c - '0', true
	case 'a' <= c && c <= 'f':
		return c - 'a' + 10, true
	case 'A' <= c && c <= 'F':
		return c - 'A' + 10, true
	default:
		return 0, false
	}
}

func uint64ToByte(n uint64, b []byte) {
	binary.BigEndian.PutUint64(b, n)
}

// IsRecording returns the recording state of the Span. It will return
// true if the Span is active and events can be recorded.
func (s *span) IsRecording() bool {
	return !s.finished
}

type statusInfo struct {
	code        otelcodes.Code
	description string
}

// SetStatus saves state of code and description indicating
// whether the span has recorded errors. This will be done by setting
// `error.message` tag on the span. If the code has been set to a higher
// value before (OK > Error > Unset), the code will not be changed.
// The code and description are set once when the span is finished.
func (s *span) SetStatus(code otelcodes.Code, description string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if code >= s.statusInfo.code {
		s.statusInfo = statusInfo{code, description}
	}
}

// AddEvent adds a span event onto the span with the provided name and EventOptions
func (s *span) AddEvent(name string, opts ...oteltrace.EventOption) {
	s.mu.Lock()
	defer s.mu.Unlock()
	// s.events is read under s.mu by End; guard the append so a concurrent
	// AddEvent/RecordError can't race the flush.
	if s.finished {
		return
	}
	s.events = append(s.events, newSpanEvent(name, opts...))
}

// newSpanEvent converts an OpenTelemetry event to the Datadog span event options.
func newSpanEvent(name string, opts ...oteltrace.EventOption) spanEvent {
	c := oteltrace.NewEventConfig(opts...)
	cAttrs := c.Attributes()
	if len(cAttrs) == 0 {
		return spanEvent{
			name: name,
			options: []tracer.SpanEventOption{
				tracer.WithSpanEventTimestamp(c.Timestamp()),
			},
		}
	}
	attrs := make(map[string]any, len(cAttrs))
	for _, a := range cAttrs {
		attrs[string(a.Key)] = a.Value.AsInterface()
	}
	return spanEvent{
		name: name,
		options: []tracer.SpanEventOption{
			tracer.WithSpanEventTimestamp(c.Timestamp()),
			tracer.WithSpanEventAttributes(attrs),
		},
	}
}

// RecordError records err as an OpenTelemetry "exception" span event (with a stack
// trace when WithStackTrace is set). Like the OTel SDK, it does not set the status.
// See https://github.com/open-telemetry/opentelemetry-go/blob/v1.44.0/sdk/trace/span.go#L631
func (s *span) RecordError(err error, opts ...oteltrace.EventOption) {
	if err == nil || !s.IsRecording() {
		return
	}
	s.AddEvent("exception", exceptionEventOptions(err, opts)...)
}

// exceptionEventOptions returns the options of the OpenTelemetry "exception" event for err.
func exceptionEventOptions(err error, opts []oteltrace.EventOption) []oteltrace.EventOption {
	cfg := oteltrace.NewEventConfig(opts...)
	attrs := []attribute.KeyValue{
		attribute.String("exception.type", errorTypeName(err)),
		attribute.String("exception.message", err.Error()),
	}
	if cfg.StackTrace() {
		attrs = append(attrs, attribute.String("exception.stacktrace", recordStackTrace()))
	}
	return append(opts, oteltrace.WithAttributes(attrs...))
}

// errorTypeName formats err's type like the OTel SDK's exception.type.
func errorTypeName(err error) string {
	t := reflect.TypeOf(err)
	if t.PkgPath() == "" && t.Name() == "" {
		return t.String() // builtin or pointer type, e.g. *errors.errorString
	}
	return t.PkgPath() + "." + t.Name()
}

func recordStackTrace() string {
	buf := make([]byte, 2048)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}

// SetAttributes sets the key-value pairs as tags on the span.
// Every value is propagated as an interface.
// Some attribute keys are reserved and will be remapped to Datadog reserved tags.
// The reserved tags list is as follows:
//   - "operation.name" (remapped to "span.name")
//   - "analytics.event" (remapped to "_dd1.sr.eausr")
//   - "service.name"
//   - "resource.name"
//   - "span.type"
//
// The list of reserved tags might be extended in the future.
// Any other non-reserved tags will be set as provided.
func (s *span) SetAttributes(kv ...attribute.KeyValue) {
	if len(kv) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	otelSemantics := s.flags&flagOTelSemantics != 0
	for _, attr := range kv {
		k, v := toReservedAttributes(string(attr.Key), attr.Value, otelSemantics)
		if k == "" {
			continue
		}
		if !otelSemantics && k == ext.SpanName {
			if name, ok := v.(string); ok {
				s.opName = strings.ToLower(name)
				s.flags &^= flagOpNameNonString
				continue
			}
			s.opName = ""
			s.flags |= flagOpNameNonString
		}
		s.recordAttrMeta(k, attr.Value)
		s.DD.SetTag(k, v)
	}
}

// recordAttrMeta records the small set of attribute flags/values inspected at End
// so we do not need to buffer all attributes in a map.
func (s *span) recordAttrMeta(k string, v attribute.Value) {
	switch k {
	case ext.ErrorMsg:
		s.flags |= flagHasErrorMsg
		return
	case ext.ErrorType:
		s.flags |= flagHasErrorType
		return
	case ext.ErrorStack:
		s.flags |= flagHasErrorStack
		return
	}
	if s.flags&flagOTelSemantics != 0 {
		return
	}
	switch k {
	case "http.request.method":
		s.flags |= flagHasHTTPMethod
	case "graphql.operation.type":
		if v.Type() == attribute.STRING && v.AsString() != "" {
			s.flags |= flagHasGraphQLOp
		}
	case "db.system":
		if str := attrLowerString(v); str != "" {
			s.ensureOpAttrs().dbSystem = str
		}
	case "messaging.system":
		if str := attrLowerString(v); str != "" {
			s.ensureOpAttrs().messagingSystem = str
		}
	case "messaging.operation":
		if str := attrLowerString(v); str != "" {
			s.ensureOpAttrs().messagingOperation = str
		}
	case "rpc.system":
		if str := attrLowerString(v); str != "" {
			s.ensureOpAttrs().rpcSystem = str
		}
	case "rpc.service":
		if str := attrLowerString(v); str != "" {
			s.ensureOpAttrs().rpcService = str
		}
	case "faas.invoked_provider":
		if str := attrLowerString(v); str != "" {
			s.ensureOpAttrs().faasInvokedProvider = str
		}
	case "faas.invoked_name":
		if str := attrLowerString(v); str != "" {
			s.ensureOpAttrs().faasInvokedName = str
		}
	case "faas.trigger":
		if str := attrLowerString(v); str != "" {
			s.ensureOpAttrs().faasTrigger = str
		}
	case "network.protocol.name":
		if str := attrLowerString(v); str != "" {
			s.ensureOpAttrs().networkProtocolName = str
		}
	}
}

// recordAnyAttrMeta records metadata for tags coming from ContextWithStartOptions
// (where values are untyped any rather than attribute.Value).
func (s *span) recordAnyAttrMeta(k string, v any) {
	switch k {
	case ext.ErrorMsg:
		s.flags |= flagHasErrorMsg
		return
	case ext.ErrorType:
		s.flags |= flagHasErrorType
		return
	case ext.ErrorStack:
		s.flags |= flagHasErrorStack
		return
	}
	if s.flags&flagOTelSemantics != 0 {
		return
	}
	if k == "operation.name" || k == ext.SpanName {
		if name, ok := v.(string); ok {
			s.opName = strings.ToLower(name)
			s.flags &^= flagOpNameNonString
		} else if k == ext.SpanName {
			s.opName = ""
			s.flags |= flagOpNameNonString
		}
		return
	}
	switch k {
	case "http.request.method":
		s.flags |= flagHasHTTPMethod
	case "graphql.operation.type":
		if str := anyLowerString(v); str != "" {
			s.flags |= flagHasGraphQLOp
		}
	case "db.system":
		if str := anyLowerString(v); str != "" {
			s.ensureOpAttrs().dbSystem = str
		}
	case "messaging.system":
		if str := anyLowerString(v); str != "" {
			s.ensureOpAttrs().messagingSystem = str
		}
	case "messaging.operation":
		if str := anyLowerString(v); str != "" {
			s.ensureOpAttrs().messagingOperation = str
		}
	case "rpc.system":
		if str := anyLowerString(v); str != "" {
			s.ensureOpAttrs().rpcSystem = str
		}
	case "rpc.service":
		if str := anyLowerString(v); str != "" {
			s.ensureOpAttrs().rpcService = str
		}
	case "faas.invoked_provider":
		if str := anyLowerString(v); str != "" {
			s.ensureOpAttrs().faasInvokedProvider = str
		}
	case "faas.invoked_name":
		if str := anyLowerString(v); str != "" {
			s.ensureOpAttrs().faasInvokedName = str
		}
	case "faas.trigger":
		if str := anyLowerString(v); str != "" {
			s.ensureOpAttrs().faasTrigger = str
		}
	case "network.protocol.name":
		if str := anyLowerString(v); str != "" {
			s.ensureOpAttrs().networkProtocolName = str
		}
	}
}

func (s *span) ensureOpAttrs() *opNameAttrs {
	if s.opAttrs == nil {
		s.opAttrs = &opNameAttrs{}
	}
	return s.opAttrs
}

func attrLowerString(v attribute.Value) string {
	if v.Type() != attribute.STRING {
		return ""
	}
	str := v.AsString()
	if str == "" {
		return ""
	}
	return strings.ToLower(str)
}

func anyLowerString(v any) string {
	switch val := v.(type) {
	case string:
		if val != "" {
			return strings.ToLower(val)
		}
	case attribute.Value:
		return attrLowerString(val)
	}
	return ""
}

// toReservedAttributes recognizes a set of span attributes that have a special meaning.
// These tags should supersede other values.
func toReservedAttributes(k string, v attribute.Value, otelSemantics bool) (string, any) {
	if otelSemantics {
		// Under OTel semantics, set every attribute as-is (no DD reserved-tag remapping).
		return k, v.AsInterface()
	}
	switch k {
	case "operation.name":
		if ops := strings.ToLower(v.AsString()); ops != "" {
			return ext.SpanName, ops
		}
		// ignoring non-string values
		return "", nil
	case "analytics.event":
		var rate int
		if b, err := strconv.ParseBool(v.AsString()); err == nil && b {
			rate = 1
		} else if v.AsBool() {
			rate = 1
		} else {
			rate = 0
		}
		return ext.EventSampleRate, rate
	case "http.response.status_code":
		return "http.status_code", formatHTTPStatusCode(v.AsInt64())
	default:
		return k, v.AsInterface()
	}
}

// Pre-boxed common HTTP status code strings to avoid both strconv.FormatInt
// and string-to-any boxing allocations on the HTTP span hot path.
var (
	httpStatus200 any = "200"
	httpStatus201 any = "201"
	httpStatus202 any = "202"
	httpStatus204 any = "204"
	httpStatus301 any = "301"
	httpStatus302 any = "302"
	httpStatus304 any = "304"
	httpStatus307 any = "307"
	httpStatus308 any = "308"
	httpStatus400 any = "400"
	httpStatus401 any = "401"
	httpStatus403 any = "403"
	httpStatus404 any = "404"
	httpStatus405 any = "405"
	httpStatus408 any = "408"
	httpStatus409 any = "409"
	httpStatus422 any = "422"
	httpStatus429 any = "429"
	httpStatus500 any = "500"
	httpStatus502 any = "502"
	httpStatus503 any = "503"
	httpStatus504 any = "504"
)

func formatHTTPStatusCode(code int64) any {
	switch code {
	case 200:
		return httpStatus200
	case 201:
		return httpStatus201
	case 202:
		return httpStatus202
	case 204:
		return httpStatus204
	case 301:
		return httpStatus301
	case 302:
		return httpStatus302
	case 304:
		return httpStatus304
	case 307:
		return httpStatus307
	case 308:
		return httpStatus308
	case 400:
		return httpStatus400
	case 401:
		return httpStatus401
	case 403:
		return httpStatus403
	case 404:
		return httpStatus404
	case 405:
		return httpStatus405
	case 408:
		return httpStatus408
	case 409:
		return httpStatus409
	case 422:
		return httpStatus422
	case 429:
		return httpStatus429
	case 500:
		return httpStatus500
	case 502:
		return httpStatus502
	case 503:
		return httpStatus503
	case 504:
		return httpStatus504
	default:
		return strconv.FormatInt(code, 10)
	}
}

func (s *span) createOperationName() string {
	isClient := s.spanKind == oteltrace.SpanKindClient
	isServer := s.spanKind == oteltrace.SpanKindServer

	// http
	if s.flags&flagHasHTTPMethod != 0 {
		switch s.spanKind {
		case oteltrace.SpanKindServer:
			return "http.server.request"
		case oteltrace.SpanKindClient:
			return "http.client.request"
		}
	}

	if a := s.opAttrs; a != nil {
		// database
		if a.dbSystem != "" && isClient {
			return a.dbSystem + ".query"
		}

		// messaging
		if a.messagingSystem != "" && a.messagingOperation != "" {
			switch s.spanKind {
			case oteltrace.SpanKindClient, oteltrace.SpanKindServer,
				oteltrace.SpanKindConsumer, oteltrace.SpanKindProducer:
				return a.messagingSystem + "." + a.messagingOperation
			}
		}

		// RPC & AWS
		if a.rpcSystem != "" {
			isAws := a.rpcSystem == "aws-api"
			// AWS client
			if isAws && isClient {
				if a.rpcService != "" {
					return "aws." + a.rpcService + ".request"
				}
				return "aws.client.request"
			}
			// RPC client
			if isClient {
				return a.rpcSystem + ".client.request"
			}
			// RPC server
			if isServer {
				return a.rpcSystem + ".server.request"
			}
		}

		// FAAS client
		if a.faasInvokedProvider != "" && a.faasInvokedName != "" && isClient {
			return a.faasInvokedProvider + "." + a.faasInvokedName + ".invoke"
		}

		// FAAS server
		if a.faasTrigger != "" && isServer {
			return a.faasTrigger + ".invoke"
		}
	}

	// Graphql
	if s.flags&flagHasGraphQLOp != 0 {
		return "graphql.server.request"
	}

	// if nothing matches, checking for generic http server/client
	var protocol string
	if s.opAttrs != nil {
		protocol = s.opAttrs.networkProtocolName
	}
	if isServer {
		if protocol != "" {
			return protocol + ".server.request"
		}
		return "server.request"
	} else if isClient {
		if protocol != "" {
			return protocol + ".client.request"
		}
		return "client.request"
	}

	if s.spanKind != 0 {
		return s.spanKind.String()
	}
	// no span kind was set/detected, so span kind will be set to Internal explicitly.
	s.DD.SetTag(ext.SpanKind, spanKindTagValues[oteltrace.SpanKindInternal])
	return oteltrace.SpanKindInternal.String()
}
