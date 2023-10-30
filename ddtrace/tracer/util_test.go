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
		key   string
		value string
		err   error
	}{
		{"hello", "world", nil},
		{"hello", "world=", nil},
		{"hello=", "world", fmt.Errorf("key contains an invalid character 61")},
		{"", "world", fmt.Errorf("key length must be greater than zero")},
		{"hello", "", fmt.Errorf("value length must be greater than zero")},
		{"こんにちは", "world", fmt.Errorf("key contains an invalid character 12371")},
		{"hello", "世界", fmt.Errorf("value contains an invalid character 19990")},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			assert.Equal(t, tt.err, isValidPropagatableTag(tt.key, tt.value))
		})
	}
}

func TestParsePropagatableTraceTags(t *testing.T) {
	for i, tt := range [...]struct {
		input  string
		output map[string]string
		err    error
	}{
		{"hello=world", map[string]string{"hello": "world"}, nil},
		{" hello = world ", map[string]string{" hello ": " world "}, nil},
		{"hello=world,service=account", map[string]string{"hello": "world", "service": "account"}, nil},
		{"hello=wor=ld====,service=account,tag1=val=ue1", map[string]string{"hello": "wor=ld====", "service": "account", "tag1": "val=ue1"}, nil},
		{"hello", nil, fmt.Errorf("invalid format")},
		{"hello=world,service=", nil, fmt.Errorf("invalid format")},
		{"hello=world,", nil, fmt.Errorf("invalid format")},
		{"=world", nil, fmt.Errorf("invalid format")},
		{"hello=,tag1=value1", nil, fmt.Errorf("invalid format")},
		{",hello=world", nil, fmt.Errorf("invalid format")},
	} {
		t.Run(fmt.Sprintf("%d", i), func(t *testing.T) {
			output, err := parsePropagatableTraceTags(tt.input)
			assert.Equal(t, tt.output, output)
			assert.Equal(t, tt.err, err)
		})
	}
}
