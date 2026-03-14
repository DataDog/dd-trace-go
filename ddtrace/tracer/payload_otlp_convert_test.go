// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
)

func TestConvertSpan(t *testing.T) {
	s := newSpan("op", "svc", "my-resource", 100, 200, 50)
	s.start = 1000
	s.duration = 100
	s.meta[ext.SpanKind] = ext.SpanKindServer
	s.meta["meta.key"] = "meta.val"
	s.metrics["metric.key"] = 42.5
	s.error = 1
	s.meta[ext.ErrorMsg] = "something failed"

	otlp := convertSpan(s)

	assert.Equal(t, "my-resource", otlp.Name)
	assert.Equal(t, uint64(1000), otlp.StartTimeUnixNano)
	assert.Equal(t, uint64(1100), otlp.EndTimeUnixNano)
	assert.Equal(t, otlptrace.Span_SPAN_KIND_SERVER, otlp.Kind)
	require.NotNil(t, otlp.Status)
	assert.Equal(t, otlptrace.Status_STATUS_CODE_ERROR, otlp.Status.Code)
	assert.Equal(t, "something failed", otlp.Status.Message)

	attrs := keyValuesToMap(otlp.Attributes)
	assert.Equal(t, "meta.val", attrs["meta.key"])
	assert.Equal(t, 42.5, attrs["metric.key"])
}

func TestConvertSpanKind(t *testing.T) {
	tests := []struct {
		dd   string
		want otlptrace.Span_SpanKind
	}{
		{ext.SpanKindInternal, otlptrace.Span_SPAN_KIND_INTERNAL},
		{ext.SpanKindServer, otlptrace.Span_SPAN_KIND_SERVER},
		{ext.SpanKindClient, otlptrace.Span_SPAN_KIND_CLIENT},
		{ext.SpanKindProducer, otlptrace.Span_SPAN_KIND_PRODUCER},
		{ext.SpanKindConsumer, otlptrace.Span_SPAN_KIND_CONSUMER},
		{"", otlptrace.Span_SPAN_KIND_INTERNAL},
		{"unknown", otlptrace.Span_SPAN_KIND_INTERNAL},
	}
	for _, tt := range tests {
		t.Run(tt.dd, func(t *testing.T) {
			got := convertSpanKind(tt.dd)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestConvertSpanStatus(t *testing.T) {
	t.Run("unset", func(t *testing.T) {
		s := newBasicSpan("op")
		s.error = 0
		st := convertSpanStatus(s)
		require.NotNil(t, st)
		assert.Equal(t, otlptrace.Status_STATUS_CODE_UNSET, st.Code)
	})

	t.Run("error", func(t *testing.T) {
		s := newBasicSpan("op")
		s.error = 1
		s.meta = map[string]string{ext.ErrorMsg: "err msg"}
		st := convertSpanStatus(s)
		require.NotNil(t, st)
		assert.Equal(t, otlptrace.Status_STATUS_CODE_ERROR, st.Code)
		assert.Equal(t, "err msg", st.Message)
	})
}

func TestConvertSpanAttributes(t *testing.T) {
	s := newBasicSpan("op")
	s.meta = map[string]string{"tag": "val", "env": "test"}
	s.metrics = map[string]float64{"count": 10, "rate": 0.5}

	attrs := convertSpanAttributes(s)
	m := keyValuesToMap(attrs)
	assert.Equal(t, "val", m["tag"])
	assert.Equal(t, "test", m["env"])
	assert.Equal(t, 10.0, m["count"])
	assert.Equal(t, 0.5, m["rate"])
}

func TestConvertMapToOTLPAttributesString(t *testing.T) {
	dd := map[string]string{"k1": "v1", "k2": "v2"}
	otlp := convertMapToOTLPAttributesString(dd)
	require.Len(t, otlp, 2)
	m := keyValuesToMap(otlp)
	assert.Equal(t, "v1", m["k1"])
	assert.Equal(t, "v2", m["k2"])
}

func TestConvertMapToOTLPAttributesString_EmptyNil(t *testing.T) {
	assert.Empty(t, convertMapToOTLPAttributesString(nil))
	assert.Empty(t, convertMapToOTLPAttributesString(map[string]string{}))
}

func TestConvertEvents(t *testing.T) {
	s := newBasicSpan("op")
	s.spanEvents = []spanEvent{
		{
			Name:         "event1",
			TimeUnixNano: 1000,
			Attributes: map[string]*spanEventAttribute{
				"attr1": {Type: spanEventAttributeTypeString, StringValue: "s1"},
			},
		},
		{
			Name:         "event2",
			TimeUnixNano: 2000,
			Attributes:   nil,
		},
	}

	events := convertEvents(s)
	require.Len(t, events, 2)
	assert.Equal(t, "event1", events[0].Name)
	assert.Equal(t, uint64(1000), events[0].TimeUnixNano)
	require.Len(t, events[0].Attributes, 1)
	assert.Equal(t, "attr1", events[0].Attributes[0].Key)
	assert.Equal(t, "s1", events[0].Attributes[0].Value.GetStringValue())

	assert.Equal(t, "event2", events[1].Name)
	assert.Equal(t, uint64(2000), events[1].TimeUnixNano)
	assert.Empty(t, events[1].Attributes)
}

func TestConvertEventAttributes(t *testing.T) {
	dd := map[string]*spanEventAttribute{
		"str":   {Type: spanEventAttributeTypeString, StringValue: "x"},
		"bool":  {Type: spanEventAttributeTypeBool, BoolValue: true},
		"int":   {Type: spanEventAttributeTypeInt, IntValue: -7},
		"float": {Type: spanEventAttributeTypeDouble, DoubleValue: 3.14},
		"arr": {
			Type: spanEventAttributeTypeArray,
			ArrayValue: &spanEventArrayAttribute{
				Values: []*spanEventArrayAttributeValue{
					{Type: spanEventArrayAttributeValueTypeString, StringValue: "elem1"},
					{Type: spanEventArrayAttributeValueTypeInt, IntValue: 99},
				},
			},
		},
	}
	otlp := convertEventAttributes(dd)
	require.Len(t, otlp, 5)

	m := keyValuesToMap(otlp)
	assert.Equal(t, "x", m["str"])
	assert.Equal(t, true, m["bool"])
	assert.Equal(t, int64(-7), m["int"])
	assert.Equal(t, 3.14, m["float"])

	// Array: assert outside keyValuesToMap (it doesn't flatten arrays)
	var arrKV *otlpcommon.KeyValue
	for _, kv := range otlp {
		if kv != nil && kv.Key == "arr" {
			arrKV = kv
			break
		}
	}
	require.NotNil(t, arrKV)
	av := arrKV.Value.GetArrayValue()
	require.NotNil(t, av)
	require.Len(t, av.Values, 2)
	assert.Equal(t, "elem1", av.Values[0].GetStringValue())
	assert.Equal(t, int64(99), av.Values[1].GetIntValue())
}

func TestConvertEventAttributes_NilEmpty(t *testing.T) {
	assert.Empty(t, convertEventAttributes(nil))
	assert.Empty(t, convertEventAttributes(map[string]*spanEventAttribute{}))
}

func TestConvertSpanLinks(t *testing.T) {
	links := []SpanLink{
		{TraceID: 1, SpanID: 10, Attributes: map[string]string{"k": "v"}, Tracestate: "ts", Flags: 1},
		{TraceID: 2, SpanID: 20},
	}
	otlp := convertSpanLinks(links)
	require.Len(t, otlp, 2)
	attrs := keyValuesToMap(otlp[0].Attributes)
	assert.Equal(t, "v", attrs["k"])
	assert.Equal(t, "ts", otlp[0].TraceState)
	assert.Equal(t, uint32(1), otlp[0].Flags)
	assert.Empty(t, otlp[1].Attributes)
}

func TestConvertSpanLinks_EmptyNil(t *testing.T) {
	assert.Empty(t, convertSpanLinks(nil))
	assert.Empty(t, convertSpanLinks([]SpanLink{}))
}

// keyValuesToMap converts []*otlpcommon.KeyValue into a map for easier assertion.
// Values are returned as any (string or float64 for double).
func keyValuesToMap(kvs []*otlpcommon.KeyValue) map[string]any {
	m := make(map[string]any)
	for _, kv := range kvs {
		if kv == nil || kv.Value == nil {
			continue
		}
		switch v := kv.Value.Value.(type) {
		case *otlpcommon.AnyValue_StringValue:
			m[kv.Key] = v.StringValue
		case *otlpcommon.AnyValue_DoubleValue:
			m[kv.Key] = v.DoubleValue
		case *otlpcommon.AnyValue_IntValue:
			m[kv.Key] = v.IntValue
		case *otlpcommon.AnyValue_BoolValue:
			m[kv.Key] = v.BoolValue
		default:
			m[kv.Key] = nil
		}
	}
	return m
}
