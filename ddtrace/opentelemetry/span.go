// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

var _ oteltrace.Span = (*span)(nil)

type span struct {
	noop.Span               // https://pkg.go.dev/go.opentelemetry.io/otel/trace#hdr-API_Implementations
	mu         sync.RWMutex `msg:"-"` // all fields are protected by this RWMutex
	DD         tracer.Span
	finished   bool
	attributes map[string]interface{}
	spanKind   oteltrace.SpanKind
	finishOpts []tracer.FinishOption
	statusInfo
	*oteltracer
	events []spanEvent
}

func (s *span) TracerProvider() oteltrace.TracerProvider { return s.oteltracer.provider }

func (s *span) SetName(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attributes[ext.SpanName] = strings.ToLower(name)
}

// spanEvent holds information about span events
type spanEvent struct {
	Name         string                 `json:"name"`
	TimeUnixNano int64                  `json:"time_unix_nano"`
	Attributes   map[string]interface{} `json:"attributes,omitempty"`
}

func (s *span) End(options ...oteltrace.SpanEndOption) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finished {
		return
	}
	s.finished = true
	for k, v := range s.attributes {
		//	if we find operation.name,
		if k == "operation.name" || k == ext.SpanName {
			//	set it and keep track that it was set to ignore everything else
			if name, ok := v.(string); ok {
				s.attributes[ext.SpanName] = strings.ToLower(name)
			}
		}
	}

	// if no operation name was explicitly set,
	// operation name has to be calculated from the attributes
	if op, ok := s.attributes[ext.SpanName]; !ok || op == "" {
		s.DD.SetTag(ext.SpanName, strings.ToLower(s.createOperationName()))
	}
	for k, v := range s.attributes {
		s.DD.SetTag(k, v)
	}
	if s.events != nil {
		b, err := json.Marshal(s.events)
		if err == nil {
			s.DD.SetTag("events", string(b))
		} else {
			log.Debug(fmt.Sprintf("Issue marshaling span events; events dropped from span meta\n%v", err))
		}
	}
	var finishCfg = oteltrace.NewSpanEndConfig(options...)
	var opts []tracer.FinishOption
	if s.statusInfo.code == otelcodes.Error {
		s.DD.SetTag(ext.ErrorMsg, s.statusInfo.description)
		opts = append(opts, tracer.WithError(errors.New(s.statusInfo.description)))
	}
	if t := finishCfg.Timestamp(); !t.IsZero() {
		opts = append(opts, tracer.FinishTime(t))
	}
	if len(s.finishOpts) != 0 {
		opts = append(opts, s.finishOpts...)
	}
	s.DD.Finish(opts...)
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
	ctx := s.DD.Context()
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
	if err := tracer.Inject(s.DD.Context(), headers); err != nil {
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if code >= s.statusInfo.code {
		s.statusInfo = statusInfo{code, description}
	}
}

// AddEvent adds a span event onto the span with the provided name and EventOptions
func (s *span) AddEvent(name string, opts ...oteltrace.EventOption) {
	if !s.IsRecording() {
		return
	}
	c := oteltrace.NewEventConfig(opts...)
	attrs := make(map[string]interface{})
	for _, a := range c.Attributes() {
		attrs[string(a.Key)] = a.Value.AsInterface()
	}
	e := spanEvent{
		Name:         name,
		TimeUnixNano: c.Timestamp().UnixNano(),
		Attributes:   attrs,
	}
	s.events = append(s.events, e)
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
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, kv := range kv {
		if k, v := toReservedAttributes(string(kv.Key), kv.Value); k != "" {
			s.attributes[k] = v
		}
	}
}

// toReservedAttributes recognizes a set of span attributes that have a special meaning.
// These tags should supersede other values.
func toReservedAttributes(k string, v attribute.Value) (string, interface{}) {
	switch k {
	case "operation.name":
		if ops := strings.ToLower(v.AsString()); ops != "" {
			return ext.SpanName, strings.ToLower(v.AsString())
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
		return "http.status_code", strconv.FormatInt(v.AsInt64(), 10)
	default:
		return k, v.AsInterface()
	}
}

func (s *span) createOperationName() string {
	isClient := s.spanKind == oteltrace.SpanKindClient
	isServer := s.spanKind == oteltrace.SpanKindServer

	// http
	if _, ok := s.attributes["http.request.method"]; ok {
		switch s.spanKind {
		case oteltrace.SpanKindServer:
			return "http.server.request"
		case oteltrace.SpanKindClient:
			return "http.client.request"
		}
	}

	// database
	if v, ok := s.valueFromAttributes("db.system"); ok && isClient {
		return v + ".query"
	}

	// messaging

	system, systemOk := s.valueFromAttributes("messaging.system")
	op, opOk := s.valueFromAttributes("messaging.operation")
	if systemOk && opOk {
		switch s.spanKind {
		case oteltrace.SpanKindClient, oteltrace.SpanKindServer,
			oteltrace.SpanKindConsumer, oteltrace.SpanKindProducer:
			return system + "." + op
		}
	}

	// RPC & AWS
	rpcValue, isRPC := s.valueFromAttributes("rpc.system")
	isAws := isRPC && (rpcValue == "aws-api")
	// AWS client
	if isAws && isClient {
		if service, ok := s.valueFromAttributes("rpc.service"); ok {
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
	provider, pOk := s.valueFromAttributes("faas.invoked_provider")
	invokedName, inOk := s.valueFromAttributes("faas.invoked_name")
	if pOk && inOk && isClient {
		return provider + "." + invokedName + ".invoke"
	}

	//	FAAS server
	trigger, tOk := s.valueFromAttributes("faas.trigger")
	if tOk && isServer {
		return trigger + ".invoke"
	}

	//	Graphql
	if _, ok := s.valueFromAttributes("graphql.operation.type"); ok {
		return "graphql.server.request"
	}

	// if nothing matches, checking for generic http server/client
	protocol, pOk := s.valueFromAttributes("network.protocol.name")
	if isServer {
		if pOk {
			return protocol + ".server.request"
		}
		return "server.request"
	} else if isClient {
		if pOk {
			return protocol + ".client.request"
		}
		return "client.request"
	}

	if s.spanKind != 0 {
		return s.spanKind.String()
	}
	// no span kind was set/detected, so span kind will be set to Internal explicitly.
	s.attributes[ext.SpanKind] = oteltrace.SpanKindInternal
	return oteltrace.SpanKindInternal.String()
}

func (s *span) valueFromAttributes(key string) (string, bool) {
	v, ok := s.attributes[key]
	if !ok {
		return "", false
	}
	attr, ok := v.(attribute.Value)
	if ok {
		if s := strings.ToLower(attr.AsString()); s != "" {
			return s, true
		}
		return "", false
	}
	if s := v.(string); s != "" {
		return strings.ToLower(s), true
	}
	return "", false
}
