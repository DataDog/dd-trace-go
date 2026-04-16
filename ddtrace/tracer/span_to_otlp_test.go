// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"encoding/binary"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	otlpcommon "go.opentelemetry.io/proto/otlp/common/v1"
	otlptrace "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	internalconfig "github.com/DataDog/dd-trace-go/v2/internal/config"
	"github.com/DataDog/dd-trace-go/v2/internal/samplernames"
	"github.com/DataDog/dd-trace-go/v2/internal/version"
)

func TestBuildResource(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		r := buildResource(nil)
		require.NotNil(t, r)
		assert.Empty(t, r.Attributes)
	})

	t.Run("populated", func(t *testing.T) {
		cfg := internalconfig.CreateNew()
		cfg.SetServiceName("my-service", internalconfig.OriginCode)
		cfg.SetEnv("prod", internalconfig.OriginCode)
		cfg.SetVersion("1.2.3", internalconfig.OriginCode)

		r := buildResource(cfg)
		attrs := keyValuesToMap(r.Attributes)

		assert.Equal(t, "my-service", attrs["service.name"])
		assert.Equal(t, "prod", attrs["deployment.environment.name"])
		assert.Equal(t, "1.2.3", attrs["service.version"])
		assert.Equal(t, "go", attrs["telemetry.sdk.language"])
		assert.Equal(t, "datadog", attrs["telemetry.sdk.name"])
		assert.Equal(t, version.Tag, attrs["telemetry.sdk.version"])
	})

	t.Run("optional fields omitted when empty", func(t *testing.T) {
		cfg := internalconfig.CreateNew()
		cfg.SetServiceName("svc", internalconfig.OriginCode)

		r := buildResource(cfg)
		attrs := keyValuesToMap(r.Attributes)

		assert.Equal(t, "svc", attrs["service.name"])
		_, hasEnv := attrs["deployment.environment.name"]
		assert.False(t, hasEnv, "deployment.environment.name should be absent when env is empty")
		_, hasVer := attrs["service.version"]
		assert.False(t, hasVer, "service.version should be absent when version is empty")
	})
}

func TestConvertSpan(t *testing.T) {
	s := newSpan("op", "svc", "my-resource", 100, 200, 50)
	s.start = 1000
	s.duration = 100
	s.meta[ext.SpanKind] = ext.SpanKindServer
	s.meta["meta.key"] = "meta.val"
	s.metrics["metric.key"] = 42.5
	s.error = 1
	s.meta[ext.ErrorMsg] = "something failed"

	otlp := convertSpan(s, "svc")
	require.NotNil(t, otlp)

	// DD resource → OTLP name (spec: "resource field must be encoded as the OTLP span's name field")
	assert.Equal(t, "my-resource", otlp.Name)

	assert.Equal(t, uint64(1000), otlp.StartTimeUnixNano)
	assert.Equal(t, uint64(1100), otlp.EndTimeUnixNano)
	assert.Equal(t, otlptrace.Span_SPAN_KIND_SERVER, otlp.Kind)

	// parent_span_id
	assert.Equal(t, uint64(50), binary.BigEndian.Uint64(otlp.ParentSpanId))

	// trace_id and span_id are populated
	assert.Len(t, otlp.TraceId, 16)
	assert.Len(t, otlp.SpanId, 8)
	assert.Equal(t, uint64(100), binary.BigEndian.Uint64(otlp.SpanId))

	// Status
	require.NotNil(t, otlp.Status)
	assert.Equal(t, otlptrace.Status_STATUS_CODE_ERROR, otlp.Status.Code)
	assert.Equal(t, "something failed", otlp.Status.Message)

	// Attributes: meta as strings, metrics as doubles
	attrs := keyValuesToMap(otlp.Attributes)
	assert.Equal(t, "meta.val", attrs["meta.key"])
	assert.Equal(t, 42.5, attrs["metric.key"])
}

func TestConvertSpanParentSpanId(t *testing.T) {
	t.Run("set when parent_id is non-zero", func(t *testing.T) {
		s := newSpan("op", "svc", "res", 100, 200, 50)
		otlp := convertSpan(s, "svc")
		require.NotNil(t, otlp.ParentSpanId)
		assert.Equal(t, uint64(50), binary.BigEndian.Uint64(otlp.ParentSpanId))
	})

	t.Run("omitted when parent_id is zero", func(t *testing.T) {
		s := newSpan("op", "svc", "res", 100, 200, 0)
		otlp := convertSpan(s, "svc")
		assert.Nil(t, otlp.ParentSpanId, "ParentSpanId must be omitted for root spans")
	})
}

func TestConvertSpanFiltersUnsampled(t *testing.T) {
	tests := []struct {
		name     string
		priority int
		wantNil  bool
	}{
		{"auto-reject", ext.PriorityAutoReject, true},
		{"auto-keep", ext.PriorityAutoKeep, false},
		{"user-keep", ext.PriorityUserKeep, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newSpan("op", "svc", "res", 1, 1, 0)
			s.context.setSamplingPriority(tt.priority, samplernames.Unknown)
			result := convertSpan(s, "svc")
			if tt.wantNil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
			}
		})
	}
}

func TestConvertSpanPriorityUnset(t *testing.T) {
	s := newSpan("op", "svc", "res", 1, 1, 0)
	// priority is not set — span should be included (not dropped)
	result := convertSpan(s, "")
	assert.NotNil(t, result)
}

func TestConvertSpanServiceNameOverride(t *testing.T) {
	t.Run("same as default - no service.name attribute", func(t *testing.T) {
		s := newSpan("op", "my-service", "res", 1, 1, 0)
		otlp := convertSpan(s, "my-service")
		require.NotNil(t, otlp)
		attrs := keyValuesToMap(otlp.Attributes)
		_, hasServiceName := attrs["service.name"]
		assert.False(t, hasServiceName)
	})

	t.Run("different from default - service.name attribute added", func(t *testing.T) {
		s := newSpan("op", "other-service", "res", 1, 1, 0)
		otlp := convertSpan(s, "my-service")
		require.NotNil(t, otlp)
		attrs := keyValuesToMap(otlp.Attributes)
		assert.Equal(t, "other-service", attrs["service.name"])
	})
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
		{"", otlptrace.Span_SPAN_KIND_UNSPECIFIED},
		{"unknown", otlptrace.Span_SPAN_KIND_UNSPECIFIED},
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

	attrs := convertSpanAttributes(s, "")
	m := keyValuesToMap(attrs)
	assert.Equal(t, "val", m["tag"])
	assert.Equal(t, "test", m["env"])
	assert.Equal(t, 10.0, m["count"])
	assert.Equal(t, 0.5, m["rate"])
	assert.Equal(t, "op", m["operation.name"])
	assert.Contains(t, m, "resource.name")
	assert.Contains(t, m, "span.type")
}

func TestConvertSpanAttributesWithMetaStruct(t *testing.T) {
	s := newBasicSpan("op")
	s.meta = map[string]string{"tag": "val"}
	s.metaStruct = map[string]any{
		"nested": map[string]any{"a": "b"},
		"simple": map[string]string{"x": "y"},
	}

	attrs := convertSpanAttributes(s, "")

	var nestedKV, simpleKV *otlpcommon.KeyValue
	for _, kv := range attrs {
		switch kv.Key {
		case "nested":
			nestedKV = kv
		case "simple":
			simpleKV = kv
		}
	}

	require.NotNil(t, nestedKV, "nested metaStruct key should be present")
	kvlist := nestedKV.Value.GetKvlistValue()
	require.NotNil(t, kvlist)
	assert.Equal(t, "a", kvlist.Values[0].Key)
	assert.Equal(t, "b", kvlist.Values[0].Value.GetStringValue())

	require.NotNil(t, simpleKV, "simple metaStruct key should be present")
	kvlist = simpleKV.Value.GetKvlistValue()
	require.NotNil(t, kvlist)
	assert.Equal(t, "x", kvlist.Values[0].Key)
	assert.Equal(t, "y", kvlist.Values[0].Value.GetStringValue())
}

func TestConvertSpanAttributesMaxLimit(t *testing.T) {
	s := newBasicSpan("op")
	s.meta = make(map[string]string, 200)
	for i := range 200 {
		s.meta[fmt.Sprintf("key-%d", i)] = "val"
	}

	attrs := convertSpanAttributes(s, "other-service")
	assert.LessOrEqual(t, len(attrs), maxAttributesCount)
}

func TestConvertSpanAttributesPriorityOrder(t *testing.T) {
	s := newBasicSpan("op")
	s.meta = make(map[string]string, maxAttributesCount)
	for i := range maxAttributesCount {
		s.meta[fmt.Sprintf("key-%d", i)] = "val"
	}
	s.metrics = map[string]float64{"should-be-dropped": 1.0}

	attrs := convertSpanAttributes(s, "")
	m := keyValuesToMap(attrs)

	assert.Equal(t, "op", m["operation.name"], "operation.name should always be present")
	assert.Contains(t, m, "resource.name", "resource.name should always be present")
	assert.Contains(t, m, "span.type", "span.type should always be present")
	assert.NotContains(t, m, "should-be-dropped", "metrics should be dropped when meta fills the limit")
	assert.Equal(t, maxAttributesCount, len(attrs))
}

func TestConvertSpanAttributesServiceNameOverride(t *testing.T) {
	t.Run("same as default - no attribute", func(t *testing.T) {
		s := newSpan("op", "my-service", "res", 1, 1, 0)
		attrs := convertSpanAttributes(s, "my-service")
		m := keyValuesToMap(attrs)
		_, hasServiceName := m["service.name"]
		assert.False(t, hasServiceName, "service.name attribute should be absent when it matches the default")
	})

	t.Run("different from default - attribute added", func(t *testing.T) {
		s := newSpan("op", "other-service", "res", 1, 1, 0)
		attrs := convertSpanAttributes(s, "my-service")
		m := keyValuesToMap(attrs)
		assert.Equal(t, "other-service", m["service.name"])
	})
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

func TestConvertSpanTraceState(t *testing.T) {
	t.Run("populated from span context", func(t *testing.T) {
		s := newSpan("op", "svc", "res", 1, 1, 0)
		setPropagatingTag(s.context, tracestateHeader, "dd=s:2;o:rum,othervendor=abc")

		otlpSpan := convertSpan(s, "svc")
		require.NotNil(t, otlpSpan)
		assert.Equal(t, "dd=s:2;o:rum,othervendor=abc", otlpSpan.TraceState)
	})

	t.Run("empty when no tracestate", func(t *testing.T) {
		s := newSpan("op", "svc", "res", 1, 1, 0)

		otlpSpan := convertSpan(s, "svc")
		require.NotNil(t, otlpSpan)
		assert.Empty(t, otlpSpan.TraceState)
	})

	t.Run("empty when trace is nil", func(t *testing.T) {
		s := newSpan("op", "svc", "res", 1, 1, 0)
		s.context.trace = nil

		otlpSpan := convertSpan(s, "svc")
		require.NotNil(t, otlpSpan)
		assert.Empty(t, otlpSpan.TraceState)
	})
}

func TestAnyToOTLPValue(t *testing.T) {
	t.Run("string", func(t *testing.T) {
		av := anyToOTLPValue("hello")
		assert.Equal(t, "hello", av.GetStringValue())
	})

	t.Run("bool", func(t *testing.T) {
		av := anyToOTLPValue(true)
		assert.Equal(t, true, av.GetBoolValue())
	})

	t.Run("int64", func(t *testing.T) {
		av := anyToOTLPValue(int64(42))
		assert.Equal(t, int64(42), av.GetIntValue())
	})

	t.Run("uint64", func(t *testing.T) {
		av := anyToOTLPValue(uint64(64))
		assert.Equal(t, int64(64), av.GetIntValue())
	})

	t.Run("float32", func(t *testing.T) {
		av := anyToOTLPValue(float32(2.5))
		assert.InDelta(t, 2.5, av.GetDoubleValue(), 1e-6)
	})

	t.Run("float64", func(t *testing.T) {
		av := anyToOTLPValue(3.14)
		assert.Equal(t, 3.14, av.GetDoubleValue())
	})

	t.Run("[]any", func(t *testing.T) {
		av := anyToOTLPValue([]any{"a", int64(1), true})
		arr := av.GetArrayValue()
		require.NotNil(t, arr)
		require.Len(t, arr.Values, 3)
		assert.Equal(t, "a", arr.Values[0].GetStringValue())
		assert.Equal(t, int64(1), arr.Values[1].GetIntValue())
		assert.Equal(t, true, arr.Values[2].GetBoolValue())
	})

	t.Run("map[string]any", func(t *testing.T) {
		av := anyToOTLPValue(map[string]any{"k": "v"})
		kvlist := av.GetKvlistValue()
		require.NotNil(t, kvlist)
		require.Len(t, kvlist.Values, 1)
		assert.Equal(t, "k", kvlist.Values[0].Key)
		assert.Equal(t, "v", kvlist.Values[0].Value.GetStringValue())
	})

	t.Run("map[string]string", func(t *testing.T) {
		av := anyToOTLPValue(map[string]string{"x": "y"})
		kvlist := av.GetKvlistValue()
		require.NotNil(t, kvlist)
		require.Len(t, kvlist.Values, 1)
		assert.Equal(t, "x", kvlist.Values[0].Key)
		assert.Equal(t, "y", kvlist.Values[0].Value.GetStringValue())
	})

	t.Run("nested map", func(t *testing.T) {
		av := anyToOTLPValue(map[string]any{
			"triggers": []any{map[string]any{"id": "1"}},
		})
		kvlist := av.GetKvlistValue()
		require.NotNil(t, kvlist)
		require.Len(t, kvlist.Values, 1)
		assert.Equal(t, "triggers", kvlist.Values[0].Key)
		inner := kvlist.Values[0].Value.GetArrayValue()
		require.NotNil(t, inner)
		require.Len(t, inner.Values, 1)
		innerMap := inner.Values[0].GetKvlistValue()
		require.NotNil(t, innerMap)
		assert.Equal(t, "1", innerMap.Values[0].Value.GetStringValue())
	})

	t.Run("default JSON fallback", func(t *testing.T) {
		type custom struct{ A string }
		av := anyToOTLPValue(custom{A: "test"})
		assert.Contains(t, av.GetStringValue(), `"A":"test"`)
	})
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
