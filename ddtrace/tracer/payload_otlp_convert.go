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
		Name:              s.resource,
		Kind:              convertSpanKind(getSpanKind(s)),
		StartTimeUnixNano: uint64(s.start),
		EndTimeUnixNano:   uint64(s.start + s.duration),
		Attributes:        convertSpanAttributes(s),
		Events:            convertEvents(s),
		Links:             convertSpanLinks(s.spanLinks),
		Status:            convertSpanStatus(s),
	}
}

func convertSpanStatus(s *Span) *otlptrace.Status {
	status := &otlptrace.Status{
		Code:    otlptrace.Status_STATUS_CODE_UNSET,
		Message: s.meta[ext.ErrorMsg],
	}
	if s.error == 1 {
		status.Code = otlptrace.Status_STATUS_CODE_ERROR
	}
	return status
}

func convertSpanLinks(links []SpanLink) []*otlptrace.Span_Link {
	otlpLinks := make([]*otlptrace.Span_Link, 0)
	for _, link := range links {
		otlpLinks = append(otlpLinks, &otlptrace.Span_Link{
			TraceId:    convertTraceID(link.TraceID),
			SpanId:     convertSpanID(link.SpanID),
			Attributes: convertMapToOTLPAttributesString(link.Attributes),
			TraceState: link.Tracestate,
			Flags:      link.Flags,
		})
	}
	return otlpLinks
}

func convertEvents(s *Span) []*otlptrace.Span_Event {
	events := make([]*otlptrace.Span_Event, 0)
	for _, event := range s.spanEvents {
		events = append(events, &otlptrace.Span_Event{
			Name:         event.Name,
			TimeUnixNano: uint64(event.TimeUnixNano),
			Attributes:   convertEventAttributes(event.Attributes),
		})
	}
	return events
}

func convertTraceID(traceID uint64) []byte {
	return []byte{}
}

func convertSpanID(spanID uint64) []byte {
	return []byte{}
}

func convertSpanKind(spanKind string) otlptrace.Span_SpanKind {
	switch spanKind {
	case ext.SpanKindInternal:
		return otlptrace.Span_SPAN_KIND_INTERNAL
	case ext.SpanKindServer:
		return otlptrace.Span_SPAN_KIND_SERVER
	case ext.SpanKindClient:
		return otlptrace.Span_SPAN_KIND_CLIENT
	case ext.SpanKindProducer:
		return otlptrace.Span_SPAN_KIND_PRODUCER
	case ext.SpanKindConsumer:
		return otlptrace.Span_SPAN_KIND_CONSUMER
	default:
		return otlptrace.Span_SPAN_KIND_INTERNAL
	}
}

func getSpanKind(s *Span) string {
	return s.meta[ext.SpanKind]
}

// -----------------------------------------------------------------------------
// Attribute conversion (DD → OTLP KeyValue / AnyValue)
// -----------------------------------------------------------------------------

// otlpKeyValue returns a KeyValue with the given key and AnyValue.
func otlpKeyValue(key string, value *otlpcommon.AnyValue) *otlpcommon.KeyValue {
	if value == nil {
		return nil
	}
	return &otlpcommon.KeyValue{Key: key, Value: value}
}

func otlpStringValue(s string) *otlpcommon.AnyValue {
	return &otlpcommon.AnyValue{Value: &otlpcommon.AnyValue_StringValue{StringValue: s}}
}

func otlpDoubleValue(d float64) *otlpcommon.AnyValue {
	return &otlpcommon.AnyValue{Value: &otlpcommon.AnyValue_DoubleValue{DoubleValue: d}}
}

// TODO: Support other attribute types (bool, int, array)
func convertEventAttributes(ddAttributes map[string]*spanEventAttribute) []*otlpcommon.KeyValue {
	out := make([]*otlpcommon.KeyValue, 0, len(ddAttributes))
	for key, value := range ddAttributes {
		out = append(out, otlpKeyValue(key, otlpStringValue(value.StringValue)))
	}
	return out
}

func convertSpanAttributes(s *Span) []*otlpcommon.KeyValue {
	attributes := convertMapToOTLPAttributesString(s.meta)
	for key, value := range s.metrics {
		attributes = append(attributes, otlpKeyValue(key, otlpDoubleValue(value)))
	}
	return attributes
}

func convertMapToOTLPAttributesString(ddAttributes map[string]string) []*otlpcommon.KeyValue {
	out := make([]*otlpcommon.KeyValue, 0, len(ddAttributes))
	for key, value := range ddAttributes {
		out = append(out, otlpKeyValue(key, otlpStringValue(value)))
	}
	return out
}
