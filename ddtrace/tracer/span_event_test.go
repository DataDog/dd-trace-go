// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package tracer

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
)

func Test_toSpanEventsMsg(t *testing.T) {
	type customType struct {
		Field1 string
		Field2 int
	}
	attrs := map[string]any{
		"key1":  "val1",
		"key2":  123,
		"key3":  int64(123),
		"key4":  uintptr(123),
		"key5":  []int64{1, 2, 3},
		"key6":  []uintptr{1, 2, 3},
		"key7":  []bool{true, false, true},
		"key8":  []string{"1", "2", "3"},
		"key9":  []float64{1.1, 2.2, 3.3},
		"key10": float32(123),
		// not supported
		"key11": map[string]string{
			"hello": "world",
		},
		"key12": customType{
			Field1: "field1",
			Field2: 2,
		},
	}
	wantAttrs := map[string]*spanEventAttribute{
		"key1": {Type: spanEventAttributeTypeString, StringValue: "val1"},
		"key2": {Type: spanEventAttributeTypeInt, IntValue: 123},
		"key3": {Type: spanEventAttributeTypeInt, IntValue: 123},
		"key4": {Type: spanEventAttributeTypeInt, IntValue: 123},
		"key5": {Type: spanEventAttributeTypeArray, ArrayValue: &spanEventArrayAttribute{
			Values: []*spanEventArrayAttributeValue{
				{Type: spanEventArrayAttributeValueTypeInt, IntValue: 1},
				{Type: spanEventArrayAttributeValueTypeInt, IntValue: 2},
				{Type: spanEventArrayAttributeValueTypeInt, IntValue: 3},
			},
		}},
		"key6": {Type: spanEventAttributeTypeArray, ArrayValue: &spanEventArrayAttribute{
			Values: []*spanEventArrayAttributeValue{
				{Type: spanEventArrayAttributeValueTypeInt, IntValue: 1},
				{Type: spanEventArrayAttributeValueTypeInt, IntValue: 2},
				{Type: spanEventArrayAttributeValueTypeInt, IntValue: 3},
			},
		}},
		"key7": {Type: spanEventAttributeTypeArray, ArrayValue: &spanEventArrayAttribute{
			Values: []*spanEventArrayAttributeValue{
				{Type: spanEventArrayAttributeValueTypeBool, BoolValue: true},
				{Type: spanEventArrayAttributeValueTypeBool, BoolValue: false},
				{Type: spanEventArrayAttributeValueTypeBool, BoolValue: true},
			},
		}},
		"key8": {Type: spanEventAttributeTypeArray, ArrayValue: &spanEventArrayAttribute{
			Values: []*spanEventArrayAttributeValue{
				{Type: spanEventArrayAttributeValueTypeString, StringValue: "1"},
				{Type: spanEventArrayAttributeValueTypeString, StringValue: "2"},
				{Type: spanEventArrayAttributeValueTypeString, StringValue: "3"},
			},
		}},
		"key9": {Type: spanEventAttributeTypeArray, ArrayValue: &spanEventArrayAttribute{
			Values: []*spanEventArrayAttributeValue{
				{Type: spanEventArrayAttributeValueTypeDouble, DoubleValue: 1.1},
				{Type: spanEventArrayAttributeValueTypeDouble, DoubleValue: 2.2},
				{Type: spanEventArrayAttributeValueTypeDouble, DoubleValue: 3.3},
			},
		}},
		"key10": {Type: spanEventAttributeTypeDouble, DoubleValue: 123},
	}
	ts := time.Date(2025, 2, 12, 9, 0, 0, 0, time.UTC)
	event := ddtrace.NewSpanEvent("test", ddtrace.WithSpanEventTimestamp(ts), ddtrace.WithSpanEventAttributes(attrs))
	require.NotNil(t, event)

	assert.Equal(t, "test", event.Name)
	assert.Equal(t, ts, event.Time)
	assert.Equal(t, attrs, event.Attributes)

	eventMsg := toSpanEventsMsg([]ddtrace.SpanEvent{event}, true)[0]
	assert.Equal(t, "test", eventMsg.Name)
	assert.EqualValues(t, ts.UnixNano(), eventMsg.TimeUnixNano)
	assert.Equal(t, wantAttrs, eventMsg.Attributes)
	assert.Equal(t, attrs, eventMsg.RawAttributes)

	b, err := json.Marshal(eventMsg)
	require.NoError(t, err)
	wantJSON := `{"name":"test","time_unix_nano":1739350800000000000,"attributes":{"key1":"val1","key10":123,"key11":{"hello":"world"},"key12":{"Field1":"field1","Field2":2},"key2":123,"key3":123,"key4":123,"key5":[1,2,3],"key6":[1,2,3],"key7":[true,false,true],"key8":["1","2","3"],"key9":[1.1,2.2,3.3]}}`

	assert.Equal(t, wantJSON, string(b))
}
