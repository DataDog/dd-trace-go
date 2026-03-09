// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"
)

func convertSpan(s *Span) otlptrace.Span {
	return otlptrace.Span{
		TraceId:           convertTraceID(s.traceID),
		SpanId:            convertSpanID(s.spanID),
		Name:              s.name,
		Kind:              convertSpanKind(getSpanKind(s)),
		StartTimeUnixNano: uint64(s.start),
		EndTimeUnixNano:   uint64(s.start + s.duration),
		Attributes:        buildAttributes(s),
	}
}

func buildAttributes(s *Span) []*otlpcommon.KeyValue {
	return []*otlpcommon.KeyValue{}
}

func convertTraceID(traceID uint64) []byte {
	return []byte{}
}

func convertSpanID(spanID uint64) []byte {
	return []byte{}
}

func convertSpanKind(spanKind string) otlptrace.Span_SpanKind {
	return otlptrace.Span_SpanKind(1)
}

func getSpanKind(s *Span) string {
	return s.meta[ext.SpanKind]
}
