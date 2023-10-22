// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package tracer

import (
	"fmt"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestToFloat64(t *testing.T) {
	for i, tt := range [...]struct {
		value interface{}
		f     float64
		ok    bool
	}{
		0:  {1, 1, true},
		1:  {byte(1), 1, true},
		2:  {int(1), 1, true},
		3:  {int16(1), 1, true},
		4:  {int32(1), 1, true},
		5:  {int64(1), 1, true},
		6:  {uint(1), 1, true},
		7:  {uint16(1), 1, true},
		8:  {uint32(1), 1, true},
		9:  {uint64(1), 1, true},
		10: {"a", 0, false},
		11: {float32(1.25), 1.25, true},
		12: {float64(1.25), 1.25, true},
		13: {intUpperLimit, 0, false},
		14: {intUpperLimit + 1, 0, false},
		15: {intUpperLimit - 1, float64(intUpperLimit - 1), true},
		16: {intLowerLimit, 0, false},
		17: {intLowerLimit - 1, 0, false},
		18: {intLowerLimit + 1, float64(intLowerLimit + 1), true},
		19: {-1024, -1024.0, true},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			f, ok := toFloat64(tt.value)
			if ok != tt.ok {
				t.Fatalf("expected ok: %t", tt.ok)
			}
			if f != tt.f {
				t.Fatalf("expected: %#v, got: %#v", tt.f, f)
			}
		})
	}
}

func TestParseUint64(t *testing.T) {
	t.Run("negative", func(t *testing.T) {
		id, err := parseUint64("-8809075535603237910")
		assert.NoError(t, err)
		assert.Equal(t, uint64(9637668538106313706), id)
	})

	t.Run("positive", func(t *testing.T) {
		id, err := parseUint64(fmt.Sprintf("%d", uint64(math.MaxUint64)))
		assert.NoError(t, err)
		assert.Equal(t, uint64(math.MaxUint64), id)
	})

	t.Run("invalid", func(t *testing.T) {
		_, err := parseUint64("abcd")
		assert.Error(t, err)
	})
}

func TestIsValidPropagatableTraceTag(t *testing.T) {
	for i, tt := range [...]struct {
		err   error
		key   string
		value string
	}{
		{nil, "hello", "world"},
		{nil, "hello", "world="},
		{fmt.Errorf("key contains an invalid character 61"), "hello=", "world"},
		{fmt.Errorf("key length must be greater than zero"), "", "world"},
		{fmt.Errorf("value length must be greater than zero"), "hello", ""},
		{fmt.Errorf("key contains an invalid character 12371"), "こんにちは", "world"},
		{fmt.Errorf("value contains an invalid character 19990"), "hello", "世界"},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			assert.Equal(t, tt.err, isValidPropagatableTag(tt.key, tt.value))
		})
	}
}

func TestParsePropagatableTraceTags(t *testing.T) {
	for i, tt := range [...]struct {
		err    error
		output map[string]string
		input  string
	}{
		{nil, map[string]string{"hello": "world"}, "hello=world"},
		{nil, map[string]string{" hello ": " world "}, " hello = world "},
		{nil, map[string]string{"hello": "world", "service": "account"}, "hello=world,service=account"},
		{nil, map[string]string{"hello": "wor=ld====", "service": "account", "tag1": "val=ue1"}, "hello=wor=ld====,service=account,tag1=val=ue1"},
		{fmt.Errorf("invalid format"), nil, "hello"},
		{fmt.Errorf("invalid format"), nil, "hello=world,service="},
		{fmt.Errorf("invalid format"), nil, "hello=world,"},
		{fmt.Errorf("invalid format"), nil, "=world"},
		{fmt.Errorf("invalid format"), nil, "hello=,tag1=value1"},
		{fmt.Errorf("invalid format"), nil, ",hello=world"},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			output, err := parsePropagatableTraceTags(tt.input)
			assert.Equal(t, tt.output, output)
			assert.Equal(t, tt.err, err)
		})
	}
}
