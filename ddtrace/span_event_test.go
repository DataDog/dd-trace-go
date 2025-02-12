package ddtrace

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestNewSpanEvent(t *testing.T) {
	attrs := map[string]any{
		"key1":  "val1",
		"key2":  123,
		"key3":  int64(123),
		"key4":  uintptr(123),
		"key5":  []int64{1, 2, 3},
		"key6":  []uintptr{1, 2, 3},
		"key7":  []bool{true, false, true},
		"key8":  []string{"1", "2", "3"},
		"key9":  []float64{1, 2, 3},
		"key10": float32(123),
	}
	wantAttrs := map[string]*SpanEventAttribute{
		"key1": {Type: SpanEventAttributeTypeString, StringValue: "val1"},
		"key2": {Type: SpanEventAttributeTypeInt, IntValue: 123},
		"key3": {Type: SpanEventAttributeTypeInt, IntValue: 123},
		"key4": {Type: SpanEventAttributeTypeInt, IntValue: 123},
		"key5": {Type: SpanEventAttributeTypeArray, ArrayValue: &SpanEventArrayAttribute{
			Values: []*SpanEventArrayAttributeValue{
				{Type: SpanEventArrayAttributeValueTypeInt, IntValue: 1},
				{Type: SpanEventArrayAttributeValueTypeInt, IntValue: 2},
				{Type: SpanEventArrayAttributeValueTypeInt, IntValue: 3},
			},
		}},
		"key6": {Type: SpanEventAttributeTypeArray, ArrayValue: &SpanEventArrayAttribute{
			Values: []*SpanEventArrayAttributeValue{
				{Type: SpanEventArrayAttributeValueTypeInt, IntValue: 1},
				{Type: SpanEventArrayAttributeValueTypeInt, IntValue: 2},
				{Type: SpanEventArrayAttributeValueTypeInt, IntValue: 3},
			},
		}},
		"key7": {Type: SpanEventAttributeTypeArray, ArrayValue: &SpanEventArrayAttribute{
			Values: []*SpanEventArrayAttributeValue{
				{Type: SpanEventArrayAttributeValueTypeBool, BoolValue: true},
				{Type: SpanEventArrayAttributeValueTypeBool, BoolValue: false},
				{Type: SpanEventArrayAttributeValueTypeBool, BoolValue: true},
			},
		}},
		"key8": {Type: SpanEventAttributeTypeArray, ArrayValue: &SpanEventArrayAttribute{
			Values: []*SpanEventArrayAttributeValue{
				{Type: SpanEventArrayAttributeValueTypeString, StringValue: "1"},
				{Type: SpanEventArrayAttributeValueTypeString, StringValue: "2"},
				{Type: SpanEventArrayAttributeValueTypeString, StringValue: "3"},
			},
		}},
		"key9": {Type: SpanEventAttributeTypeArray, ArrayValue: &SpanEventArrayAttribute{
			Values: []*SpanEventArrayAttributeValue{
				{Type: SpanEventArrayAttributeValueTypeDouble, DoubleValue: 1},
				{Type: SpanEventArrayAttributeValueTypeDouble, DoubleValue: 2},
				{Type: SpanEventArrayAttributeValueTypeDouble, DoubleValue: 3},
			},
		}},
		"key10": {Type: SpanEventAttributeTypeDouble, DoubleValue: 123},
	}
	event := NewSpanEvent("test", attrs)
	require.NotNil(t, event)
	assert.Equal(t, "test", event.Name)
	assert.NotEmpty(t, event.TimeUnixNano)
	assert.Equal(t, wantAttrs, event.Attributes)
}
