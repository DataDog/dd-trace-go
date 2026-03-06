// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otelprocesscontext

import (
	"testing"

	"github.com/stretchr/testify/require"
	slimresourcev1 "go.opentelemetry.io/proto/slim/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// TestWireCompatibilityOurResourceToSlim marshals our Resource type and
// verifies the bytes decode correctly as the slim OTLP Resource.
func TestWireCompatibilityOurResourceToSlim(t *testing.T) {
	our := &Resource{
		// Test all different value types
		Attributes: []*KeyValue{
			{Key: "string", Value: &AnyValue{Value: &AnyValue_StringValue{StringValue: "my-service"}}},
			{Key: "bool", Value: &AnyValue{Value: &AnyValue_BoolValue{BoolValue: true}}},
			{Key: "int", Value: &AnyValue{Value: &AnyValue_IntValue{IntValue: 42}}},
			{Key: "double", Value: &AnyValue{Value: &AnyValue_DoubleValue{DoubleValue: 3.14}}},
			{Key: "bytes", Value: &AnyValue{Value: &AnyValue_BytesValue{BytesValue: []byte{0x01, 0x02, 0x03}}}},
			{Key: "array", Value: &AnyValue{Value: &AnyValue_ArrayValue{ArrayValue: &ArrayValue{Values: []*AnyValue{
				{Value: &AnyValue_StringValue{StringValue: "hello"}},
				{Value: &AnyValue_IntValue{IntValue: 42}},
				{Value: &AnyValue_DoubleValue{DoubleValue: 3.14}},
				{Value: &AnyValue_BytesValue{BytesValue: []byte{0x01, 0x02, 0x03}}},
			}}}}},
			{Key: "kvlist", Value: &AnyValue{Value: &AnyValue_KvlistValue{KvlistValue: &KeyValueList{Values: []*KeyValue{
				{Key: "key1", Value: &AnyValue{Value: &AnyValue_StringValue{StringValue: "value1"}}},
				{Key: "key2", Value: &AnyValue{Value: &AnyValue_IntValue{IntValue: 42}}},
				{Key: "key3", Value: &AnyValue{Value: &AnyValue_DoubleValue{DoubleValue: 3.14}}},
				{Key: "key4", Value: &AnyValue{Value: &AnyValue_BytesValue{BytesValue: []byte{0x01, 0x02, 0x03}}}},
			}}}}},
		},
		DroppedAttributesCount: 3,
	}

	b, err := proto.Marshal(our)
	require.NoError(t, err)

	var slim slimresourcev1.Resource
	require.NoError(t, proto.Unmarshal(b, &slim))

	require.Len(t, slim.GetAttributes(), 7)
	require.Equal(t, uint32(3), slim.GetDroppedAttributesCount())

	require.Equal(t, "string", slim.GetAttributes()[0].GetKey())
	require.Equal(t, "my-service", slim.GetAttributes()[0].GetValue().GetStringValue())

	require.Equal(t, "bool", slim.GetAttributes()[1].GetKey())
	require.True(t, slim.GetAttributes()[1].GetValue().GetBoolValue())

	require.Equal(t, "int", slim.GetAttributes()[2].GetKey())
	require.Equal(t, int64(42), slim.GetAttributes()[2].GetValue().GetIntValue())

	require.Equal(t, "double", slim.GetAttributes()[3].GetKey())
	require.Equal(t, 3.14, slim.GetAttributes()[3].GetValue().GetDoubleValue())

	require.Equal(t, "bytes", slim.GetAttributes()[4].GetKey())
	require.Equal(t, []byte{0x01, 0x02, 0x03}, slim.GetAttributes()[4].GetValue().GetBytesValue())

	require.Equal(t, "array", slim.GetAttributes()[5].GetKey())
	arr := slim.GetAttributes()[5].GetValue().GetArrayValue()
	require.Len(t, arr.GetValues(), 4)
	require.Equal(t, "hello", arr.GetValues()[0].GetStringValue())
	require.Equal(t, int64(42), arr.GetValues()[1].GetIntValue())
	require.Equal(t, 3.14, arr.GetValues()[2].GetDoubleValue())
	require.Equal(t, []byte{0x01, 0x02, 0x03}, arr.GetValues()[3].GetBytesValue())

	require.Equal(t, "kvlist", slim.GetAttributes()[6].GetKey())
	kvl := slim.GetAttributes()[6].GetValue().GetKvlistValue()
	require.Len(t, kvl.GetValues(), 4)
	require.Equal(t, "key1", kvl.GetValues()[0].GetKey())
	require.Equal(t, "value1", kvl.GetValues()[0].GetValue().GetStringValue())
	require.Equal(t, "key2", kvl.GetValues()[1].GetKey())
	require.Equal(t, int64(42), kvl.GetValues()[1].GetValue().GetIntValue())
	require.Equal(t, "key3", kvl.GetValues()[2].GetKey())
	require.Equal(t, 3.14, kvl.GetValues()[2].GetValue().GetDoubleValue())
	require.Equal(t, "key4", kvl.GetValues()[3].GetKey())
	require.Equal(t, []byte{0x01, 0x02, 0x03}, kvl.GetValues()[3].GetValue().GetBytesValue())
}
