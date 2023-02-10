// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package opentelemetry

import (
	"encoding/binary"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var _ oteltrace.Span = (*span)(nil)

type span struct {
	ddtrace.Span
	finished bool
	statusInfo
	*oteltracer
}

func (s *span) TracerProvider() oteltrace.TracerProvider                { return s.oteltracer.provider }
func (s *span) AddEvent(name string, options ...oteltrace.EventOption)  { /*	no-op */ }
func (s *span) RecordError(err error, options ...oteltrace.EventOption) { /*	no-op */ }

func (s *span) SetName(name string) { s.SetOperationName(name) }

func (s *span) End(options ...oteltrace.SpanEndOption) {
	s.finished = true
	var finishCfg = oteltrace.NewSpanEndConfig(options...)
	var opts []tracer.FinishOption
	if s.statusInfo.code == otelcodes.Error {
		s.SetTag(ext.ErrorMsg, s.statusInfo.description)
	}
	if t := finishCfg.Timestamp(); !t.IsZero() {
		opts = append(opts, tracer.FinishTime(t))
	}
	s.Finish(opts...)
}

// SpanContext returns implementation of the oteltrace.SpanContext.
func (s *span) SpanContext() oteltrace.SpanContext {
	ctx := s.Span.Context()
	var traceID oteltrace.TraceID
	var spanID oteltrace.SpanID
	// todo(dianashevchenko): change ctx.TraceID() to extract 128 traceID from W3C interface
	uint64ToByte(ctx.TraceID(), traceID[:])
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
	}
	c.TraceState = state
	parent := strings.Trim(headers["traceparent"], " \t-")
	if len(parent) > 3 {
		// checking the length to avoid panic when parsing
		if f, err := strconv.ParseUint(parent[len(parent)-3:], 16, 8); err != nil {
			c.TraceFlags = oteltrace.TraceFlags(f)
		} else {
			log.Debug("Couldn't parse traceparent: %v", err)
		}
	}
	// Remote indicates a remotely-created Span
	c.Remote = true
}

// todo: verify whether we need
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
// `error.msg` tag on the span. If the code has been set to a higher
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
	for _, attr := range kv {
		s.SetTag(string(attr.Key), attr.Value.AsInterface())
	}
}
