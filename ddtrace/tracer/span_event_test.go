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
)

func Test_spanAddEvent(t *testing.T) {
	type customType struct {
		Field1 string `json:"field_1"`
		Field2 int    `json:"field_2"`
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
	ts := time.Date(2025, 2, 12, 9, 0, 0, 0, time.UTC)

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
	assertAttrsJSON := func(t *testing.T, attrs map[string]any) {
		wantJSON := `{"key1":"val1","key10":123,"key11":{"hello":"world"},"key12":{"field_1":"field1","field_2":2},"key2":123,"key3":123,"key4":123,"key5":[1,2,3],"key6":[1,2,3],"key7":[true,false,true],"key8":["1","2","3"],"key9":[1.1,2.2,3.3]}`
		b, err := json.Marshal(attrs)
		require.NoError(t, err)
		assert.Equal(t, wantJSON, string(b))
	}

	t.Run("nil span should be a noop", func(t *testing.T) {
		var s *Span

		require.NotPanics(t, func() {
			s.AddEvent("test-event-1", WithSpanEventTimestamp(ts), WithSpanEventAttributes(attrs))
			s.AddEvent("test-event-2", WithSpanEventAttributes(attrs))
			s.AddEvent("test-event-3")
		})
	})

	t.Run("with native events support", func(t *testing.T) {
		s := newBasicSpan("test")
		s.supportsEvents = true
		s.AddEvent("test-event-1", WithSpanEventTimestamp(ts), WithSpanEventAttributes(attrs))
		s.AddEvent("test-event-2", WithSpanEventAttributes(attrs))
		s.AddEvent("test-event-3")
		s.Finish()

		require.Len(t, s.spanEvents, 3)
		evt := s.spanEvents[0]
		assert.Equal(t, "test-event-1", evt.Name)
		assert.EqualValues(t, ts.UnixNano(), evt.TimeUnixNano)
		assert.Equal(t, wantAttrs, evt.Attributes)
		assert.Nil(t, evt.RawAttributes)

		evt = s.spanEvents[1]
		assert.Equal(t, "test-event-2", evt.Name)
		assert.Greater(t, int64(evt.TimeUnixNano), ts.UnixNano())
		assert.Equal(t, wantAttrs, evt.Attributes)
		assert.Nil(t, evt.RawAttributes)

		evt = s.spanEvents[2]
		assert.Equal(t, "test-event-3", evt.Name)
		assert.Greater(t, int64(evt.TimeUnixNano), ts.UnixNano())
		assert.Nil(t, evt.Attributes)
		assert.Nil(t, evt.RawAttributes)
	})

	t.Run("without native events support", func(t *testing.T) {
		s := newBasicSpan("test")
		s.supportsEvents = false
		s.AddEvent("test-event-1", WithSpanEventTimestamp(ts), WithSpanEventAttributes(attrs))
		s.AddEvent("test-event-2", WithSpanEventAttributes(attrs))
		s.AddEvent("test-event-3")
		s.Finish()

		require.Empty(t, s.spanEvents)
		assert.NotEmpty(t, s.meta["events"])

		var spanEvents []spanEvent
		err := json.Unmarshal([]byte(s.meta["events"]), &spanEvents)
		require.NoError(t, err)

		require.Len(t, spanEvents, 3)
		evt := spanEvents[0]
		assert.Equal(t, "test-event-1", evt.Name)
		assert.EqualValues(t, ts.UnixNano(), evt.TimeUnixNano)
		assert.Nil(t, evt.Attributes)
		assertAttrsJSON(t, evt.RawAttributes)

		evt = spanEvents[1]
		assert.Equal(t, "test-event-2", evt.Name)
		assert.Greater(t, int64(evt.TimeUnixNano), ts.UnixNano())
		assert.Nil(t, evt.Attributes)
		assertAttrsJSON(t, evt.RawAttributes)

		evt = spanEvents[2]
		assert.Equal(t, "test-event-3", evt.Name)
		assert.Greater(t, int64(evt.TimeUnixNano), ts.UnixNano())
		assert.Nil(t, evt.Attributes)
		assert.Nil(t, evt.RawAttributes)
	})
}
