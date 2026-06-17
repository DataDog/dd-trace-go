// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/binary"
	"encoding/json"
	"fmt"

	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlpresource "go.opentelemetry.io/proto/otlp/resource/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

// Derived from the default max attributes count for OTLP spans.
// See https://opentelemetry.io/docs/specs/otel/configuration/sdk-environment-variables/#attribute-limits
const maxAttributesCount = 128

// -----------------------------------------------------------------------------
// Resource construction
// -----------------------------------------------------------------------------

// buildResource constructs the OTLP Resource from resolved tracer configuration.
// If cfg is nil, an empty resource is returned.
func buildResource(cfg *internalconfig.Config) *otlpresource.Resource {
	if cfg == nil {
		return &otlpresource.Resource{}
	}
	attrs := []*otlpcommon.KeyValue{
		otlpKeyValue("service.name", otlpStringValue(cfg.ServiceName())),
		otlpKeyValue("telemetry.sdk.language", otlpStringValue("go")),
		otlpKeyValue("telemetry.sdk.name", otlpStringValue("datadog")),
		otlpKeyValue("telemetry.sdk.version", otlpStringValue(version.Tag)),
	}
	if v := cfg.Env(); v != "" {
		attrs = append(attrs, otlpKeyValue("deployment.environment.name", otlpStringValue(v)))
	}
	if v := cfg.Version(); v != "" {
		attrs = append(attrs, otlpKeyValue("service.version", otlpStringValue(v)))
	}
	return &otlpresource.Resource{Attributes: attrs}
}

// -----------------------------------------------------------------------------
// Span conversion (DD Span → OTLP Span and related types)
// -----------------------------------------------------------------------------

// +checklocksignore — Post-finish: reads finished span fields during payload encoding.
func convertSpan(s *Span, defaultServiceName string) *otlptrace.Span {
	if p, ok := s.context.SamplingPriority(); ok && p < ext.PriorityAutoKeep {
		return nil
	}
	return &otlptrace.Span{
		TraceId:           convertTraceID(s.context.traceID.Upper(), s.context.traceID.Lower()),
		SpanId:            convertSpanID(s.spanID),
		ParentSpanId:      convertParentSpanID(s.parentID),
		Name:              s.resource,
		Kind:              convertSpanKind(getSpanKind(s)),
		StartTimeUnixNano: uint64(s.start),
		EndTimeUnixNano:   uint64(s.start + s.duration),
		Attributes:        convertSpanAttributes(s, defaultServiceName),
		Events:            convertEvents(s),
		Links:             convertSpanLinks(s.spanLinks),
		Status:            convertSpanStatus(s),
		TraceState:        convertTraceState(s.context),
	}
}

// +checklocksignore — Post-finish: reads finished span fields during payload encoding.
func convertSpanStatus(s *Span) *otlptrace.Status {
	message, _ := s.meta.Get(ext.ErrorMsg)
	status := &otlptrace.Status{
		Code:    otlptrace.Status_STATUS_CODE_UNSET,
		Message: message,
	}
	if s.error == 1 {
		status.Code = otlptrace.Status_STATUS_CODE_ERROR
	}
	return status
}

func convertSpanLinks(links []SpanLink) []*otlptrace.Span_Link {
	if len(links) == 0 {
		return nil
	}
	otlpLinks := make([]*otlptrace.Span_Link, 0, len(links))
	for _, link := range links {
		otlpLinks = append(otlpLinks, &otlptrace.Span_Link{
			TraceId:    convertTraceID(link.TraceIDHigh, link.TraceID),
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
	if len(s.spanEvents) == 0 {
		return nil
	}
	events := make([]*otlptrace.Span_Event, 0, len(s.spanEvents))
	for _, event := range s.spanEvents {
		events = append(events, &otlptrace.Span_Event{
			Name:         event.Name,
			TimeUnixNano: uint64(event.TimeUnixNano),
			Attributes:   convertEventAttributes(event.Attributes),
		})
	}
	return events
}

func convertTraceID(high, low uint64) []byte {
	b := make([]byte, 16)
	binary.BigEndian.PutUint64(b[:8], high)
	binary.BigEndian.PutUint64(b[8:], low)
	return b
}

func convertSpanID(spanID uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, spanID)
	return b
}

func convertParentSpanID(parentID uint64) []byte {
	if parentID == 0 {
		return nil
	}
	return convertSpanID(parentID)
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
		return otlptrace.Span_SPAN_KIND_UNSPECIFIED
	}
}

// +checklocksignore — Post-finish: reads finished span fields during payload encoding.
func getSpanKind(s *Span) string { v, _ := s.meta.Get(ext.SpanKind); return v }

// -----------------------------------------------------------------------------
// Attribute conversion (DD → OTLP KeyValue / AnyValue)
// -----------------------------------------------------------------------------

// addAttribute appends a key-value pair to attrs and returns true if there is
// still room for more attributes.
func addAttribute(attrs *[]*otlpcommon.KeyValue, key string, val *otlpcommon.AnyValue) bool {
	if val != nil {
		*attrs = append(*attrs, &otlpcommon.KeyValue{Key: key, Value: val})
	}
	return len(*attrs) < maxAttributesCount
}

// +checklocksignore — Post-finish: reads finished span fields during payload encoding.
func convertSpanAttributes(s *Span, defaultServiceName string) []*otlpcommon.KeyValue {
	n := s.meta.Count() + len(s.metrics) + len(s.metaStruct) + 3
	if s.service != defaultServiceName {
		n++
	}
	attrs := make([]*otlpcommon.KeyValue, 0, min(n, maxAttributesCount))

	if !addAttribute(&attrs, "operation.name", otlpStringValue(s.name)) {
		return attrs
	}
	if !addAttribute(&attrs, "resource.name", otlpStringValue(s.resource)) {
		return attrs
	}
	if !addAttribute(&attrs, "span.type", otlpStringValue(s.spanType)) {
		return attrs
	}
	if s.service != defaultServiceName {
		if !addAttribute(&attrs, "service.name", otlpStringValue(s.service)) {
			return attrs
		}
	}
	for key, value := range s.meta.All() {
		if !addAttribute(&attrs, key, otlpStringValue(value)) {
			return attrs
		}
	}
	for key, value := range s.metrics {
		if !addAttribute(&attrs, key, otlpDoubleValue(value)) {
			return attrs
		}
	}
	for key, value := range s.metaStruct {
		if !addAttribute(&attrs, key, anyToOTLPValue(value)) {
			return attrs
		}
	}
	return attrs
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
			if kv := otlpKeyValue(key, otlpArrayValue(value.ArrayValue)); kv != nil {
				out = append(out, kv)
			}
		}
	}
	return out
}

func convertTraceState(ctx *SpanContext) string {
	if ctx.trace == nil {
		return ""
	}
	return ctx.trace.propagatingTag(tracestateHeader)
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
	if arr == nil {
		return nil
	}
	values := make([]*otlpcommon.AnyValue, 0, len(arr.Values))
	for _, v := range arr.Values {
		if av := spanEventArrayAttributeValueToAnyValue(v); av != nil {
			values = append(values, av)
		}
	}
	return &otlpcommon.AnyValue{Value: &otlpcommon.AnyValue_ArrayValue{ArrayValue: &otlpcommon.ArrayValue{Values: values}}}
}

// anyToOTLPValue converts an arbitrary Go value to an OTLP AnyValue.
// Handles primitives, maps, and slices recursively. For types that don't map
// directly to OTLP, falls back to JSON-encoding as a string.
func anyToOTLPValue(v any) *otlpcommon.AnyValue {
	switch val := v.(type) {
	case string:
		return otlpStringValue(val)
	case bool:
		return otlpBoolValue(val)
	case int64:
		return otlpIntValue(val)
	case uint64:
		return otlpIntValue(int64(val))
	case float32:
		return otlpDoubleValue(float64(val))
	case float64:
		return otlpDoubleValue(val)
	case []any:
		values := make([]*otlpcommon.AnyValue, 0, len(val))
		for _, elem := range val {
			if av := anyToOTLPValue(elem); av != nil {
				values = append(values, av)
			}
		}
		return &otlpcommon.AnyValue{Value: &otlpcommon.AnyValue_ArrayValue{
			ArrayValue: &otlpcommon.ArrayValue{Values: values},
		}}
	case map[string]any:
		kvs := make([]*otlpcommon.KeyValue, 0, len(val))
		for k, elem := range val {
			if av := anyToOTLPValue(elem); av != nil {
				kvs = append(kvs, otlpKeyValue(k, av))
			}
		}
		return &otlpcommon.AnyValue{Value: &otlpcommon.AnyValue_KvlistValue{
			KvlistValue: &otlpcommon.KeyValueList{Values: kvs},
		}}
	case map[string]string:
		kvs := make([]*otlpcommon.KeyValue, 0, len(val))
		for k, elem := range val {
			kvs = append(kvs, otlpKeyValue(k, otlpStringValue(elem)))
		}
		return &otlpcommon.AnyValue{Value: &otlpcommon.AnyValue_KvlistValue{
			KvlistValue: &otlpcommon.KeyValueList{Values: kvs},
		}}
	default:
		// Types without a natural OTLP mapping (e.g. custom structs) are
		// JSON-encoded into a string as a best-effort fallback.
		b, err := json.Marshal(val)
		if err != nil {
			return otlpStringValue(fmt.Sprintf("%v", val))
		}
		return otlpStringValue(string(b))
	}
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
