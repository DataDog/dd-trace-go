// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"encoding/binary"
	"errors"
	"strconv"
	"strings"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
)

var _ oteltrace.Span = (*span)(nil)

type span struct {
	tracer.Span
	finished bool
	// nameSet signifies if the span operation name was set with specifically `operation.name` tag
	// it is persisted here since it's not possible to see how the name was set,
	// nor access the span attributes set before
	nameSet    bool
	spanKind   oteltrace.SpanKind
	finishOpts []tracer.FinishOption
	statusInfo
	*oteltracer
}

func (s *span) TracerProvider() oteltrace.TracerProvider        { return s.oteltracer.provider }
func (s *span) AddEvent(_ string, _ ...oteltrace.EventOption)   { /*	no-op */ }
func (s *span) RecordError(_ error, _ ...oteltrace.EventOption) { /*	no-op */ }

func (s *span) SetName(name string) { s.SetOperationName(name) }

func (s *span) End(options ...oteltrace.SpanEndOption) {
	if s.finished {
		return
	}
	s.finished = true
	var finishCfg = oteltrace.NewSpanEndConfig(options...)
	var opts []tracer.FinishOption
	if s.statusInfo.code == otelcodes.Error {
		s.SetTag(ext.ErrorMsg, s.statusInfo.description)
		opts = append(opts, tracer.WithError(errors.New(s.statusInfo.description)))
	}
	if t := finishCfg.Timestamp(); !t.IsZero() {
		opts = append(opts, tracer.FinishTime(t))
	}
	if len(s.finishOpts) != 0 {
		opts = append(opts, s.finishOpts...)
	}
	s.Finish(opts...)
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
	ctx := s.Span.Context()
	var traceID oteltrace.TraceID
	var spanID oteltrace.SpanID
	if w3cCtx, ok := ctx.(ddtrace.SpanContextW3C); ok {
		traceID = w3cCtx.TraceID128Bytes()
	} else {
		log.Debug("Non-W3C context found in span, unable to get full 128 bit trace id")
		uint64ToByte(ctx.TraceID(), traceID[:])
	}
	uint64ToByte(ctx.SpanID(), spanID[:])
	config := oteltrace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}
	s.extractTraceData(&config)
	return oteltrace.NewSpanContext(config)
}

func (s *span) extractTraceData(c *oteltrace.SpanContextConfig) {
	headers := tracer.TextMapCarrier{}
	if err := tracer.Inject(s.Context(), headers); err != nil {
		return
	}
	state, err := oteltrace.ParseTraceState(headers["tracestate"])
	if err != nil {
		log.Debug("Couldn't parse tracestate: %v", err)
		return
	}
	c.TraceState = state
	parent := strings.Trim(headers["traceparent"], " \t-")
	if len(parent) > 3 {
		// checking the length to avoid panic when parsing
		// The format of the traceparent is `-` separated string,
		// where flags represents the propagated flags in the format of 2 hex-encoded digits at the end of the traceparent.
		otelFlagLen := 2
		if f, err := strconv.ParseUint(parent[len(parent)-otelFlagLen:], 16, 8); err != nil {
			log.Debug("Couldn't parse traceparent: %v", err)
		} else {
			c.TraceFlags = oteltrace.TraceFlags(f)
		}
	}
	// Remote indicates a remotely-created Span
	c.Remote = true
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
	if code >= s.statusInfo.code {
		s.statusInfo = statusInfo{code, description}
	}
}

// SetAttributes sets the key-value pairs as tags on the span.
// Every value is propagated as an interface.
func (s *span) SetAttributes(kv ...attribute.KeyValue) {
	for _, keyValue := range kv {
		// remapping OTel attributes to Datadog semantics
		k, v := toSpecialAttributes(string(keyValue.Key), keyValue.Value)
		if k == ext.SpanName && v != "" {
			s.nameSet = true
		}
		s.SetTag(k, v)
	}
	// if 'operation.name' was provided explicitly before or with new attributes,
	// there is no need to recalculate the operation name
	if s.nameSet {
		return
	}

	// setting new attributes may affect span operation name,
	// so we have to recalculate it
	atttributesSet := attribute.NewSet(kv...)

	// remapping operation name relies on span.kind, which we can't get from the span once it was created,
	// so we have to persist it locally
	if t, ok := atttributesSet.Value("span.kind"); ok {
		spanKind := oteltrace.SpanKind(t.AsInt64())
		s.spanKind = spanKind
		s.Span.SetTag(ext.SpanKind, spanKind.String())
	}
	if ops := remapOperationName(s.spanKind, atttributesSet, s.nameSet); ops != "otel_unknown" {
		s.SetOperationName(ops)
	}
}

// toSpecialAttributes recognizes a set of span attributes that have a special meaning.
// These tags should supersede other values.
func toSpecialAttributes(k string, v attribute.Value) (string, interface{}) {
	switch k {
	case "operation.name":
		if ops := strings.ToLower(v.AsString()); ops != "" {
			return ext.SpanName, strings.ToLower(v.AsString())
		}
		// ignoring non-string values
		return "", nil
	case "service.name":
		return ext.ServiceName, v.AsString()
	case "resource.name":
		return ext.ResourceName, v.AsString()
	case "span.type":
		return ext.SpanType, v.AsString()
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
	default:
		return k, v.AsInterface()
	}
}

const (
	httpServerKey    = "http.server.request"
	httpClientKey    = "http.client.request"
	operationNameKey = "operation.name"
)

func remapOperationName(spanKind oteltrace.SpanKind, attrs attribute.Set, opsNameSet bool) string {
	isClient := spanKind == oteltrace.SpanKindClient
	isServer := spanKind == oteltrace.SpanKindServer

	// we can't check if and how the span name was set, so we have to persist it locally.
	// opsNameSet signifies if the span name was set with specifically `operation.name` tag
	// which has priority over the remapped operation name
	if opsNameSet {
		return ""
	}

	// http
	if _, ok := attrs.Value("http.request.method"); ok {
		switch spanKind {
		case oteltrace.SpanKindServer:
			return httpServerKey
		case oteltrace.SpanKindClient:
			return httpClientKey
		}
	}

	// database
	if v := valueFromAttributes(attrs, "db.system"); v != "" && isClient {
		return v + ".query"
	}

	// messaging
	system := valueFromAttributes(attrs, "messaging.system")
	op := valueFromAttributes(attrs, "messaging.operation")
	if system != "" && op != "" {
		switch spanKind {
		case oteltrace.SpanKindClient, oteltrace.SpanKindServer,
			oteltrace.SpanKindConsumer, oteltrace.SpanKindProducer:
			return system + "." + op
		}
	}

	// RPC & AWS
	rpcValue := valueFromAttributes(attrs, "rpc.system")
	isRPC := rpcValue != ""
	isAws := isRPC && (rpcValue == "aws-api")
	// AWS client
	if isAws && isClient {
		if service := valueFromAttributes(attrs, "rpc.service"); service != "" {
			return "aws." + service + ".request"
		}
		return "aws.client.request"
	}
	// RPC client
	if isRPC && isClient {
		return rpcValue + ".client.request"
	}
	// RPC server
	if isRPC && isServer {
		return rpcValue + ".server.request"
	}

	// FAAS client
	provider := valueFromAttributes(attrs, "faas.invoked_provider")
	faasName := valueFromAttributes(attrs, "faas.invoked_name")
	if provider != "" && faasName != "" && isClient {
		return provider + "." + faasName + ".invoke"
	}

	//	FAAS server
	trigger := valueFromAttributes(attrs, "faas.trigger")
	if trigger != "" && isServer {
		return trigger + ".invoke"
	}

	//	Graphql
	if valueFromAttributes(attrs, "graphql.operation.type") != "" {
		return "graphql.server.request"
	}

	// if nothing matches, checking for generic http server/client
	protocol := valueFromAttributes(attrs, "network.protocol.name")
	if isServer {
		if protocol != "" {
			return protocol + ".server.request"
		}
		return "server.request"
	}

	if isClient {
		if protocol != "" {
			return protocol + ".client.request"
		}
		return "client.request"
	}

	if spanKind != oteltrace.SpanKindUnspecified {
		return spanKind.String()
	}

	return "otel_unknown"
}

func valueFromAttributes(attrs attribute.Set, key string) string {
	v, ok := attrs.Value(attribute.Key(key))
	if !ok {
		return ""
	}
	return strings.ToLower(v.AsString())
}
