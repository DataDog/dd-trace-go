// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

// -----------------------------------------------------------------------------
// Span conversion (DD Span → OTLP Span and related types)
// -----------------------------------------------------------------------------

// +checklocksignore — Post-finish: reads finished span fields during payload encoding.
func convertSpan(s *Span) *otlptrace.Span {
	return &otlptrace.Span{
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

// +checklocksignore — Post-finish: reads finished span fields during payload encoding.
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

// +checklocksignore — Post-finish: reads finished span fields during payload encoding.
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

// +checklocksignore — Post-finish: reads finished span fields during payload encoding.
func getSpanKind(s *Span) string {
	return s.meta[ext.SpanKind]
}

// -----------------------------------------------------------------------------
// Attribute conversion (DD → OTLP KeyValue / AnyValue)
// -----------------------------------------------------------------------------

// +checklocksignore — Post-finish: reads finished span fields during payload encoding.
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

func convertEventAttributes(ddAttributes map[string]*spanEventAttribute) []*otlpcommon.KeyValue {
	out := make([]*otlpcommon.KeyValue, 0, len(ddAttributes))
	for key, value := range ddAttributes {
		switch value.Type {
		case spanEventAttributeTypeString:
			out = append(out, otlpKeyValue(key, otlpStringValue(value.StringValue)))
		case spanEventAttributeTypeBool:
			out = append(out, otlpKeyValue(key, otlpBoolValue(value.BoolValue)))
		case spanEventAttributeTypeDouble:
			out = append(out, otlpKeyValue(key, otlpDoubleValue(value.DoubleValue)))
		case spanEventAttributeTypeInt:
			out = append(out, otlpKeyValue(key, otlpIntValue(value.IntValue)))
		case spanEventAttributeTypeArray:
			out = append(out, otlpKeyValue(key, otlpArrayValue(value.ArrayValue)))
		}
	}
	return out
}

// --- AnyValue helpers ---

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

func otlpBoolValue(b bool) *otlpcommon.AnyValue {
	return &otlpcommon.AnyValue{Value: &otlpcommon.AnyValue_BoolValue{BoolValue: b}}
}

func otlpIntValue(i int64) *otlpcommon.AnyValue {
	return &otlpcommon.AnyValue{Value: &otlpcommon.AnyValue_IntValue{IntValue: i}}
}

func otlpArrayValue(arr *spanEventArrayAttribute) *otlpcommon.AnyValue {
	if arr == nil || len(arr.Values) == 0 {
		return &otlpcommon.AnyValue{}
	}
	values := make([]*otlpcommon.AnyValue, 0, len(arr.Values))
	for _, v := range arr.Values {
		if av := spanEventArrayAttributeValueToAnyValue(v); av != nil {
			values = append(values, av)
		}
	}
	return &otlpcommon.AnyValue{Value: &otlpcommon.AnyValue_ArrayValue{ArrayValue: &otlpcommon.ArrayValue{Values: values}}}
}

func spanEventArrayAttributeValueToAnyValue(v *spanEventArrayAttributeValue) *otlpcommon.AnyValue {
	if v == nil {
		return nil
	}
	switch v.Type {
	case spanEventArrayAttributeValueTypeString:
		return otlpStringValue(v.StringValue)
	case spanEventArrayAttributeValueTypeBool:
		return otlpBoolValue(v.BoolValue)
	case spanEventArrayAttributeValueTypeInt:
		return otlpIntValue(v.IntValue)
	case spanEventArrayAttributeValueTypeDouble:
		return otlpDoubleValue(v.DoubleValue)
	default:
		return nil
	}
}
