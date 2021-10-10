// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

//go:build !without_ddwaf && cgo && !windows && amd64 && (linux || darwin)
// +build !without_ddwaf
// +build cgo
// +build !windows
// +build amd64
// +build linux darwin

package bindings

import (
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEncoder(t *testing.T) {
	for _, tc := range []struct {
		Name                   string
		Data                   interface{}
		ExpectedError          error
		ExpectedWAFValueType   int
		ExpectedWAFValueLength int
		MaxValueDepth          interface{}
		MaxArrayLength         interface{}
		MaxMapLength           interface{}
		MaxStringLength        interface{}
	}{
		{
			Name:          "unsupported type",
			Data:          make(chan struct{}),
			ExpectedError: errUnsupportedValue,
		},
		{
			Name:                   "string",
			Data:                   "hello, waf",
			ExpectedWAFValueType:   wafStringType,
			ExpectedWAFValueLength: len("hello, waf"),
		},
		{
			Name:                   "string",
			Data:                   "",
			ExpectedWAFValueType:   wafStringType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:                   "byte slice",
			Data:                   []byte("hello, waf"),
			ExpectedWAFValueType:   wafStringType,
			ExpectedWAFValueLength: len("hello, waf"),
		},
		{
			Name:                   "nil byte slice",
			Data:                   []byte(nil),
			ExpectedWAFValueType:   wafStringType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:                   "map with empty key string",
			Data:                   map[string]int{"": 1},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "empty struct",
			Data:                   struct{}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name: "empty struct with private fields",
			Data: struct {
				a string
				b int
				c bool
			}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:          "nil interface value",
			Data:          nil,
			ExpectedError: errUnsupportedValue,
		},
		{
			Name:          "nil pointer value",
			Data:          (*string)(nil),
			ExpectedError: errUnsupportedValue,
		},
		{
			Name:          "nil pointer value",
			Data:          (*int)(nil),
			ExpectedError: errUnsupportedValue,
		},
		{
			Name:                 "non nil pointer value",
			Data:                 new(int),
			ExpectedWAFValueType: wafIntType,
		},
		{
			Name:                 "non nil pointer value",
			Data:                 new(string),
			ExpectedWAFValueType: wafStringType,
		},
		{
			Name:                   "having an empty map",
			Data:                   map[string]interface{}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:          "a Go function value",
			Data:          func() {},
			ExpectedError: errUnsupportedValue,
		},
		{
			Name:                 "int",
			Data:                 int(33),
			ExpectedWAFValueType: wafIntType,
		},
		{
			Name:                 "uint",
			Data:                 uint(33),
			ExpectedWAFValueType: wafUintType,
		},
		{
			Name:                 "bool",
			Data:                 true,
			ExpectedWAFValueType: wafStringType,
		},
		{
			Name:                 "float",
			Data:                 33.12345,
			ExpectedWAFValueType: wafIntType,
		},
		{
			Name:                   "slice",
			Data:                   []interface{}{33.12345, "ok", 27},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name:                   "slice having unsupported types",
			Data:                   []interface{}{33.12345, func() {}, "ok", 27, nil},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name:                   "array",
			Data:                   [...]interface{}{func() {}, 33.12345, "ok", 27},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name:                   "map",
			Data:                   map[string]interface{}{"k1": 1, "k2": "2"},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 2,
		},
		{
			Name:                   "map with invalid key type",
			Data:                   map[interface{}]interface{}{"k1": 1, 27: "int key", "k2": "2"},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 2,
		},
		{
			Name:                   "map with indirect string values",
			Data:                   map[interface{}]interface{}{"k1": 1, new(string): "string pointer key", "k2": "2"},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name: "struct",
			Data: struct {
				Public  string
				private string
				a       string
				A       string
			}{
				Public:  "Public",
				private: "private",
				a:       "a",
				A:       "A",
			},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 2, // public fields only
		},
		{
			Name: "struct with unsupported values",
			Data: struct {
				Public  string
				private string
				a       string
				A       func()
			}{
				Public:  "Public",
				private: "private",
				a:       "a",
				A:       nil,
			},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 1, // public fields of supported types
		},
		{
			Name:                   "array max depth",
			MaxValueDepth:          0,
			Data:                   []interface{}{1, 2, 3, 4, []int{1, 2, 3, 4}},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 4,
		},
		{
			Name:                   "array max depth",
			MaxValueDepth:          1,
			Data:                   []interface{}{1, 2, 3, 4, []int{1, 2, 3, 4}},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 5,
		},
		{
			Name:                   "array max depth",
			MaxValueDepth:          0,
			Data:                   []interface{}{},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:                   "map max depth",
			MaxValueDepth:          0,
			Data:                   map[string]interface{}{"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4", "k5": map[string]string{}},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 4,
		},
		{
			Name:                   "map max depth",
			MaxValueDepth:          1,
			Data:                   map[string]interface{}{"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4", "k5": map[string]string{}},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 5,
		},
		{
			Name:                   "map max depth",
			MaxValueDepth:          0,
			Data:                   map[string]interface{}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:                   "struct max depth",
			MaxValueDepth:          0,
			Data:                   struct{}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name:          "struct max depth",
			MaxValueDepth: 0,
			Data: struct {
				F0 string
				F1 struct{}
			}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:          "struct max depth",
			MaxValueDepth: 1,
			Data: struct {
				F0 string
				F1 struct{}
			}{},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 2,
		},
		{
			Name:                 "scalar values max depth not accounted",
			MaxValueDepth:        0,
			Data:                 33,
			ExpectedWAFValueType: wafIntType,
		},
		{
			Name:                 "scalar values max depth not accounted",
			MaxValueDepth:        0,
			Data:                 uint(33),
			ExpectedWAFValueType: wafUintType,
		},
		{
			Name:                 "scalar values max depth not accounted",
			MaxValueDepth:        0,
			Data:                 false,
			ExpectedWAFValueType: wafStringType,
		},
		{
			Name:                   "array max length",
			MaxArrayLength:         3,
			Data:                   []interface{}{1, 2, 3, 4, 5},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name:                   "map max length",
			MaxMapLength:           3,
			Data:                   map[string]string{"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4", "k5": "v5"},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name:                   "string max length",
			MaxStringLength:        3,
			Data:                   "123456789",
			ExpectedWAFValueType:   wafStringType,
			ExpectedWAFValueLength: 3,
		},
		{
			Name:                   "string max length truncation leading to same map keys",
			MaxStringLength:        1,
			Data:                   map[string]string{"k1": "v1", "k2": "v2", "k3": "v3", "k4": "v4", "k5": "v5"},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 5,
		},
		{
			Name:                   "unsupported array values",
			MaxArrayLength:         1,
			Data:                   []interface{}{"supported", func() {}, "supported", make(chan struct{})},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "unsupported array values",
			MaxArrayLength:         2,
			Data:                   []interface{}{"supported", func() {}, "supported", make(chan struct{})},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 2,
		},
		{
			Name:                   "unsupported array values",
			MaxArrayLength:         1,
			Data:                   []interface{}{func() {}, "supported", make(chan struct{}), "supported"},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "unsupported array values",
			MaxArrayLength:         2,
			Data:                   []interface{}{func() {}, "supported", make(chan struct{}), "supported"},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 2,
		},
		{
			Name:                   "unsupported array values",
			MaxArrayLength:         1,
			Data:                   []interface{}{func() {}, make(chan struct{}), "supported"},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "unsupported array values",
			MaxArrayLength:         3,
			Data:                   []interface{}{"supported", func() {}, make(chan struct{})},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "unsupported array values",
			MaxArrayLength:         3,
			Data:                   []interface{}{func() {}, "supported", make(chan struct{})},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "unsupported array values",
			MaxArrayLength:         3,
			Data:                   []interface{}{func() {}, make(chan struct{}), "supported"},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name:                   "unsupported array values",
			MaxArrayLength:         2,
			Data:                   []interface{}{func() {}, make(chan struct{}), "supported", "supported"},
			ExpectedWAFValueType:   wafArrayType,
			ExpectedWAFValueLength: 2,
		},
		{
			Name: "unsupported map key types",
			Data: map[interface{}]int{
				"supported":           1,
				interface{ m() }(nil): 1,
				nil:                   1,
				(*int)(nil):           1,
				(*string)(nil):        1,
				make(chan struct{}):   1,
			},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 1,
		},
		{
			Name: "unsupported map key types",
			Data: map[interface{}]int{
				interface{ m() }(nil): 1,
				nil:                   1,
				(*int)(nil):           1,
				(*string)(nil):        1,
				make(chan struct{}):   1,
			},
			ExpectedWAFValueType:   wafMapType,
			ExpectedWAFValueLength: 0,
		},
		{
			Name: "unsupported map values",
			Data: map[string]interface{}{
				"k0": "supported",
				"k1": func() {},
				"k2": make(chan struct{}),
			},
			MaxMapLength:           3,
			ExpectedWAFValueLength: 1,
		},
		{
			Name: "unsupported map values",
			Data: map[string]interface{}{
				"k0": "supported",
				"k1": "supported",
				"k2": make(chan struct{}),
			},
			MaxMapLength:           3,
			ExpectedWAFValueLength: 2,
		},
		{
			Name: "unsupported map values",
			Data: map[string]interface{}{
				"k0": "supported",
				"k1": "supported",
				"k2": make(chan struct{}),
			},
			MaxMapLength:           1,
			ExpectedWAFValueLength: 1,
		},
		{
			Name: "unsupported struct values",
			Data: struct {
				F0 string
				F1 func()
				F2 chan struct{}
			}{
				F0: "supported",
				F1: func() {},
				F2: make(chan struct{}),
			},
			MaxMapLength:           3,
			ExpectedWAFValueLength: 1,
		},
		{
			Name: "unsupported map values",
			Data: struct {
				F0 string
				F1 string
				F2 chan struct{}
			}{
				F0: "supported",
				F1: "supported",
				F2: make(chan struct{}),
			},
			MaxMapLength:           3,
			ExpectedWAFValueLength: 2,
		},
		{
			Name: "unsupported map values",
			Data: struct {
				F0 string
				F1 string
				F2 chan struct{}
			}{
				F0: "supported",
				F1: "supported",
				F2: make(chan struct{}),
			},
			MaxMapLength:           1,
			ExpectedWAFValueLength: 1,
		},
	} {
		tc := tc
		t.Run(tc.Name, func(t *testing.T) {
			defer requireZeroNBLiveCObjects(t)

			maxValueDepth := 10
			if max := tc.MaxValueDepth; max != nil {
				maxValueDepth = max.(int)
			}
			maxArrayLength := 1000
			if max := tc.MaxArrayLength; max != nil {
				maxArrayLength = max.(int)
			}
			maxMapLength := 1000
			if max := tc.MaxMapLength; max != nil {
				maxMapLength = max.(int)
			}
			maxStringLength := 4096
			if max := tc.MaxStringLength; max != nil {
				maxStringLength = max.(int)
			}
			e := encoder{
				maxDepth:        maxValueDepth,
				maxStringLength: maxStringLength,
				maxArrayLength:  maxArrayLength,
				maxMapLength:    maxMapLength,
			}
			wo, err := e.encode(tc.Data)
			if tc.ExpectedError != nil {
				require.Error(t, err)
				require.Equal(t, tc.ExpectedError, err)
				require.Nil(t, wo)
				return
			}
			defer free(wo)

			require.NoError(t, err)
			require.NotEqual(t, &wafObject{}, wo)

			if tc.ExpectedWAFValueType != 0 {
				require.Equal(t, tc.ExpectedWAFValueType, int(wo._type))
			}
			if tc.ExpectedWAFValueLength != 0 {
				require.Equal(t, tc.ExpectedWAFValueLength, int(wo.nbEntries), "waf value type")
			}

			// Pass the encoded value to the WAF to make sure it doesn't return an error
			waf, err := NewWAF(newTestRule("my.input"))
			require.NoError(t, err)
			defer waf.Close()
			wafCtx := NewWAFContext(waf)
			require.NotNil(t, wafCtx)
			defer wafCtx.Close()
			_, _, err = wafCtx.Run(map[string]interface{}{
				"my.input": tc.Data,
			}, time.Second)
			require.NoError(t, err)
		})
	}
}

func TestFree(t *testing.T) {
	t.Run("nil value", func(t *testing.T) {
		require.NotPanics(t, func() {
			free(nil)
		})
	})

	t.Run("zero value", func(t *testing.T) {
		require.NotPanics(t, func() {
			free(&wafObject{})
		})
	})
}

func BenchmarkEncoder(b *testing.B) {
	defer requireZeroNBLiveCObjects(b)

	rnd := rand.New(rand.NewSource(33))
	buf := make([]byte, 16384)
	n, err := rnd.Read(buf)
	fullstr := string(buf)
	encoder := encoder{
		maxDepth:        10,
		maxStringLength: 1 * 1024 * 1024,
		maxArrayLength:  100,
		maxMapLength:    100,
	}
	for _, l := range []int{1024, 4096, 8192, 16384} {
		b.Run(fmt.Sprintf("%d", l), func(b *testing.B) {
			str := fullstr[:l]
			slice := []string{str, str, str, str, str, str, str, str, str, str}
			data := map[string]interface{}{
				"k0": slice,
				"k1": slice,
				"k2": slice,
				"k3": slice,
				"k4": slice,
				"k5": slice,
				"k6": slice,
				"k7": slice,
				"k8": slice,
				"k9": slice,
			}
			if err != nil || n != len(buf) {
				b.Fatal(err)
			}
			b.ResetTimer()
			for n := 0; n < b.N; n++ {
				v, err := encoder.encode(data)
				if err != nil {
					b.Fatal(err)
				}
				free(v)
			}
		})
	}
}
