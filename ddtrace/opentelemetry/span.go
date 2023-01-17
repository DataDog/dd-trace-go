// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package opentelemetry

import (
	"encoding/binary"

	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
)

var _ oteltrace.Span = (*span)(nil)

type span struct {
	ddtrace.Span
	finished bool
	*oteltracer
}

func (s *span) TracerProvider() oteltrace.TracerProvider                { return s.oteltracer.provider }
func (s *span) AddEvent(name string, options ...oteltrace.EventOption)  { /*	no-op */ }
func (s *span) RecordError(err error, options ...oteltrace.EventOption) { /*	no-op */ }

func (s *span) SetName(name string) { s.SetOperationName(name) }

func (s *span) End(options ...oteltrace.SpanEndOption) {
	var finishCfg = oteltrace.NewSpanEndConfig(options...)
	var localOpts []tracer.FinishOption
	if t := finishCfg.Timestamp(); !t.IsZero() {
		localOpts = append(localOpts, tracer.FinishTime(t))
	}
	s.Finish(localOpts...)
	s.finished = true
}

func (s *span) SpanContext() oteltrace.SpanContext {
	ctx := s.Span.Context()
	var traceId oteltrace.TraceID
	var spanId oteltrace.SpanID
	uint64ToByte(ctx.TraceID(), traceId[:])
	uint64ToByte(ctx.SpanID(), spanId[:])
	return oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID:    traceId,
		SpanID:     spanId,
		TraceFlags: 0,
		TraceState: oteltrace.TraceState{},
		Remote:     false,
	})
}

func uint64ToByte(n uint64, b []byte) {
	binary.LittleEndian.PutUint64(b, n)
}

func (s *span) IsRecording() bool {
	if s.finished {
		return true
	}
	return false
}

func (s *span) SetStatus(code otelcodes.Code, description string) {
	//// SetStatus sets the status of the Span in the form of a code and a
	//	// description, provided the status hasn't already been set to a higher
	//	// value before (OK > Error > Unset). The description is only included in a
	//	// status when the code is for an error.
	// TODO: implement me
}

// todo: check if there are specific keys that should be handled differently or map to our tags

func (s *span) SetAttributes(kv ...attribute.KeyValue) {
	for _, attr := range kv {
		s.SetTag(string(attr.Key), attr.Value.AsInterface())
	}
}
