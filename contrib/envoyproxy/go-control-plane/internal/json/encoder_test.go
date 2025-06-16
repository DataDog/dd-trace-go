// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package json

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"runtime"
	"sort"
	"testing"

	"github.com/DataDog/go-libddwaf/v4"
	"github.com/DataDog/go-libddwaf/v4/timer"

	"github.com/stretchr/testify/require"
)

type testCase struct {
	name                  string
	jsonInput             string
	encoderSetup          func(e *libddwaf.EncoderConfig) // For configuring limits
	expectOutput          any
	expectEncodingError   bool
	expectedDecodingError bool
	truncations           map[libddwaf.TruncationReason][]int
}

func verifyTestCases(t *testing.T, pinner *runtime.Pinner, tc testCase, initiallyTruncated bool, checkOutput bool) {
	encoder := newTestJSONEncodable(initiallyTruncated, []byte(tc.jsonInput))
	config := newTestMaxJsonEncoderConfig(pinner)

	if tc.encoderSetup != nil {
		tc.encoderSetup(&config)
	}

	wafObj := &libddwaf.WAFObject{}
	truncations, err := encoder.Encode(config, wafObj, 0)

	// Check truncations
	if len(tc.truncations) == 0 {
		require.Empty(t, truncations, "Expected no truncations")
	} else {
		require.Equal(t, sortTruncations(tc.truncations), sortTruncations(truncations), "truncations mismatch")
	}

	// Check on expected error, when there is an error, the WAFObject is in an undefined state
	if tc.expectEncodingError {
		require.Error(t, err, "Expected encoding to fail with an error")
		return
	}

	require.NoError(t, err, "Encode failed with an error")
	require.False(t, wafObj.IsUnusable(), "The WAFObject should not be Nil nor Invalid")

	if !checkOutput {
		return
	}

	decoded, decodeErr := wafObj.AnyValue()

	if tc.expectedDecodingError {
		require.Error(t, decodeErr, "Expected decoding to fail with an error")
		return
	}

	require.NoError(t, decodeErr, "Decode failed with an error")
	require.True(t, reflect.DeepEqual(tc.expectOutput, decoded), fmt.Sprintf("Decoded object mismatch.\nExpected: %v\nGot:      %v", tc.expectOutput, decoded))
}

func TestJSONEncode_SimpleTypes(t *testing.T) {
	var pinner runtime.Pinner
	defer pinner.Unpin()

	testCases := []testCase{
		{
			name:                "null",
			jsonInput:           `null`,
			expectEncodingError: true, // The null value in the root of the json will result in a Nil WAF Object
		},
		{
			name:         "boolean true",
			jsonInput:    `true`,
			expectOutput: true,
		},
		{
			name:         "boolean false",
			jsonInput:    `false`,
			expectOutput: false,
		},
		{
			name:         "simple string",
			jsonInput:    `"hello world"`,
			expectOutput: "hello world",
		},
		{
			name:         "empty string",
			jsonInput:    `""`,
			expectOutput: "",
		},
		{
			name:         "integer zero",
			jsonInput:    `0`,
			expectOutput: int64(0),
		},
		{
			name:         "positive integer",
			jsonInput:    `12345`,
			expectOutput: int64(12345),
		},
		{
			name:         "negative integer",
			jsonInput:    `-67890`,
			expectOutput: int64(-67890),
		},
		{
			name:         "float zero",
			jsonInput:    `0.0`,
			expectOutput: float64(0.0),
		},
		{
			name:         "positive float",
			jsonInput:    `123.456`,
			expectOutput: float64(123.456),
		},
		{
			name:         "negative float",
			jsonInput:    `-78.901`,
			expectOutput: float64(-78.901),
		},
		{
			name:         "float scientific",
			jsonInput:    `1.23e4`, // 12300
			expectOutput: float64(12300),
		},
		{
			name:         "large integer (fits int64)",
			jsonInput:    `9223372036854775807`, // MaxInt64
			expectOutput: int64(9223372036854775807),
		},
		{
			name:         "very large integer (becomes float64)",
			jsonInput:    `9223372036854775808`, // MaxInt64 + 1
			expectOutput: float64(9223372036854775808),
		},
		{
			name:         "very large negative integer (becomes float64)",
			jsonInput:    `-9223372036854775809`, // MinInt64 - 1
			expectOutput: float64(-9223372036854775809),
		},
		{
			name:         "number too large for float64 (becomes string)",
			jsonInput:    `1e400`, // Too large for float64
			expectOutput: "1e400", // Expected to be stored as string
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			verifyTestCases(t, &pinner, tc, false, true)
		})
	}
}

func TestJSONEncode_Arrays(t *testing.T) {
	var pinner runtime.Pinner
	defer pinner.Unpin()

	testCases := []testCase{
		{
			name:         "empty array",
			jsonInput:    `[]`,
			expectOutput: []any{},
		},
		{
			name:         "simple array of numbers",
			jsonInput:    `[1, 2, 3]`,
			expectOutput: []any{int64(1), int64(2), int64(3)},
		},
		{
			name:         "array of mixed types",
			jsonInput:    `[1, "two", true, null, 3.14]`,
			expectOutput: []any{int64(1), "two", true, float64(3.14)},
		},
		{
			name:      "nested arrays",
			jsonInput: `[[1, 2], ["three"], [], [true, null]]`,
			expectOutput: []any{
				[]any{int64(1), int64(2)},
				[]any{"three"},
				[]any{},
				[]any{true},
			},
		},
		{
			name:      "array container too large",
			jsonInput: `[1, 2, 3, 4, 5, 6]`,
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxContainerSize = 3
			},
			expectOutput: []any{int64(1), int64(2), int64(3)},
			truncations:  map[libddwaf.TruncationReason][]int{libddwaf.ContainerTooLarge: {6}},
		},
		{
			name:      "double array container too large",
			jsonInput: `[1, [11, 22, 33, 44, 55, 66], 3, 4, 5]`,
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxContainerSize = 3
			},
			expectOutput: []any{int64(1), []any{int64(11), int64(22), int64(33)}, int64(3)},
			truncations:  map[libddwaf.TruncationReason][]int{libddwaf.ContainerTooLarge: {6, 5}},
		},
		{
			name:      "array depth",
			jsonInput: `[1, 2, [4, 5]]`, // Depth 3 for the innermost array containing 1
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxObjectDepth = 1
			},
			expectOutput: []any{int64(1), int64(2)},
			truncations:  map[libddwaf.TruncationReason][]int{libddwaf.ObjectTooDeep: {2}},
		},
		{
			name:      "array object too deep",
			jsonInput: `[[[1]]]`, // Depth 3 for the innermost array containing 1
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxObjectDepth = 2
			},
			expectOutput: []any{[]any{}},
			truncations:  map[libddwaf.TruncationReason][]int{libddwaf.ObjectTooDeep: {3}},
		},
		{
			name:      "array object too deep - simple",
			jsonInput: `[0, [1, 2], 3]`, // Depth 2 for the innermost array containing 1
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxObjectDepth = 1
			},
			expectOutput: []any{int64(0), int64(3)},
			truncations:  map[libddwaf.TruncationReason][]int{libddwaf.ObjectTooDeep: {2}},
		},
		{
			name:      "array object too deep - complex",
			jsonInput: `[1, [2, [3, "deep"]]]`,
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxObjectDepth = 3 // Allows [1], [2, ...], [3, "deep"]. "deep" string is at depth 3 with its array.
			},
			expectOutput: []any{int64(1), []any{int64(2), []any{int64(3), "deep"}}},
		},
		{
			name:      "array object too deep - at limit",
			jsonInput: `[1, [2, [3, "deep"]]]`,
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxObjectDepth = 2 // Allows [1], [2, ...]. The array [3, "deep"] is at depth 3.
			},
			expectOutput: []any{int64(1), []any{int64(2)}},
			truncations:  map[libddwaf.TruncationReason][]int{libddwaf.ObjectTooDeep: {3}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			verifyTestCases(t, &pinner, tc, false, true)
		})
	}
}

func TestJSONEncode_Objects(t *testing.T) {
	var pinner runtime.Pinner
	defer pinner.Unpin()

	testCases := []testCase{
		{
			name:         "empty object",
			jsonInput:    `{}`,
			expectOutput: map[string]any{},
		},
		{
			name:         "simple object",
			jsonInput:    `{"key1": "value1", "key2": 123, "key3": true, "key4": null, "key5": 3.14}`,
			expectOutput: map[string]any{"key1": "value1", "key2": int64(123), "key3": true, "key4": nil, "key5": float64(3.14)},
		},
		{
			name:      "nested object",
			jsonInput: `{"level1_key1": "value1", "level1_key2": {"level2_key1": 456, "level2_key2": {"level3_key1": "deep_value"}}}`,
			expectOutput: map[string]any{
				"level1_key1": "value1",
				"level1_key2": map[string]any{
					"level2_key1": int64(456),
					"level2_key2": map[string]any{
						"level3_key1": "deep_value",
					},
				},
			},
		},
		{
			name:      "object container too large",
			jsonInput: `{"a":1, "b":2, "c":3, "d":4, "e":5}`,
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxContainerSize = 3
			},
			expectOutput: map[string]any{"a": int64(1), "b": int64(2), "c": int64(3)},
			truncations:  map[libddwaf.TruncationReason][]int{libddwaf.ContainerTooLarge: {5}},
		},
		{
			name:      "double object container too large",
			jsonInput: `{"a":1, "b": { "aa": 11, "bb": 22, "cc": 33, "dd": 44, "ee": 55, "ff": 66 }, "c":3, "d":4, "e":5}`,
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxContainerSize = 3
			},
			expectOutput: map[string]any{"a": int64(1), "b": map[string]any{"aa": int64(11), "bb": int64(22), "cc": int64(33)}, "c": int64(3)},
			truncations:  map[libddwaf.TruncationReason][]int{libddwaf.ContainerTooLarge: {5, 6}},
		},
		{
			name:      "object key string too long",
			jsonInput: `{"verylongkeyname": 1, "shortkey": 2}`,
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxStringSize = 5
			},
			expectOutput: map[string]any{"veryl": int64(1), "short": int64(2)},
			truncations:  map[libddwaf.TruncationReason][]int{libddwaf.StringTooLong: {15, 8}}, // Lengths of "verylongkeyname" and "shortkey"
		},
		{
			name:      "object value string too long",
			jsonInput: `{"key1": "verylongstringvalue", "key2": "short"}`,
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxStringSize = 5
			},
			expectOutput: map[string]any{"key1": "veryl", "key2": "short"},
			truncations:  map[libddwaf.TruncationReason][]int{libddwaf.StringTooLong: {19}}, // Length of "verylongstringvalue"
		},
		{
			name:      "object object too deep",
			jsonInput: `{"a": {"b": {"c": 1}}}`, // c:1 is at depth 3 (obj a, obj b, obj c)
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxObjectDepth = 2
			},
			// As the map is truncated, the "c" object value is replaced by an invalid wAF object
			// thus returning a decoding error
			expectedDecodingError: true,
			expectOutput:          nil,
			truncations:           map[libddwaf.TruncationReason][]int{libddwaf.ObjectTooDeep: {3}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			verifyTestCases(t, &pinner, tc, false, true)
		})
	}
}

func TestJSONEncode_MalformedInput(t *testing.T) {
	var pinner runtime.Pinner
	defer pinner.Unpin()

	type sharedTestCase struct {
		name                 string
		jsonInput            string
		encoderSetup         func(e *libddwaf.EncoderConfig) // For configuring limits
		truncatedTestCase    testCase
		notTruncatedTestCase testCase
	}

	testCases := []sharedTestCase{
		{
			name:      "malformed json - missing value",
			jsonInput: `{"k":`,
			truncatedTestCase: testCase{
				expectOutput:          map[string]any{"k": nil},
				expectedDecodingError: true, // Decoding error expected due to nil as WAFInvalidType
			},
			notTruncatedTestCase: testCase{
				expectEncodingError: true,
			},
		},
		{
			name:      "malformed json - unclosed object",
			jsonInput: `{"key": "value"`,
			truncatedTestCase: testCase{
				expectOutput: map[string]any{"key": "value"},
			},
			notTruncatedTestCase: testCase{
				expectEncodingError: true,
			},
		},
		{
			// malformed json - full discard
			name:      "malformed json - trailing comma in array",
			jsonInput: `[1, 2, ]`,
			truncatedTestCase: testCase{
				expectEncodingError: true,
			},
			notTruncatedTestCase: testCase{
				expectEncodingError: true,
			},
		},
		{
			name:      "malformed json - trailing comma in array",
			jsonInput: `[1, 2,`,
			truncatedTestCase: testCase{
				expectOutput: []any{int64(1), int64(2)},
			},
			notTruncatedTestCase: testCase{
				expectEncodingError: true,
			},
		},
		{
			name:      "malformed json - trailing comma in object",
			jsonInput: `{"key1":"val1",`,
			truncatedTestCase: testCase{
				expectOutput: map[string]any{"key1": "val1"},
			},
			notTruncatedTestCase: testCase{
				expectEncodingError: true,
			},
		},
		{
			name:      "malformed json - missing colon",
			jsonInput: `{"key" "value"}`,
			truncatedTestCase: testCase{
				expectEncodingError: true,
			},
			notTruncatedTestCase: testCase{
				expectEncodingError: true,
			},
		},
		{
			name:      "malformed json - bare string",
			jsonInput: `hello world`,
			truncatedTestCase: testCase{
				expectEncodingError: true,
			},
			notTruncatedTestCase: testCase{
				expectEncodingError: true,
			},
		},
		{
			name:      "root string too long",
			jsonInput: `"thisisaverylongstringthatwillbetruncated"`,
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxStringSize = 10
			},
			truncatedTestCase: testCase{
				expectOutput: "thisisaver",
				truncations:  map[libddwaf.TruncationReason][]int{libddwaf.StringTooLong: {40}}, // Length of original string
			},
			notTruncatedTestCase: testCase{
				expectOutput: "thisisaver",
				truncations:  map[libddwaf.TruncationReason][]int{libddwaf.StringTooLong: {40}},
			},
		},
		{
			name:      "root level not an object or array - max depth 0",
			jsonInput: `"string"`, // A string is not a container, depth is effectively 0 for it.
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxObjectDepth = 0
			},
			truncatedTestCase: testCase{
				expectOutput: "string",
			},
			notTruncatedTestCase: testCase{
				expectOutput: "string",
			},
		},
		{
			name:      "object max depth 0 - actual object",
			jsonInput: `{}`,
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxObjectDepth = 0
			},
			truncatedTestCase: testCase{
				expectEncodingError: true,
			},
			notTruncatedTestCase: testCase{
				expectEncodingError: true,
			},
		},
		{
			name:      "array max depth 0 - actual array",
			jsonInput: `[]`,
			encoderSetup: func(e *libddwaf.EncoderConfig) {
				e.MaxObjectDepth = 0
			},
			truncatedTestCase: testCase{
				expectEncodingError: true,
			},
			notTruncatedTestCase: testCase{
				expectEncodingError: true,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.truncatedTestCase.encoderSetup = tc.encoderSetup
			tc.truncatedTestCase.jsonInput = tc.jsonInput

			tc.notTruncatedTestCase.encoderSetup = tc.encoderSetup
			tc.notTruncatedTestCase.jsonInput = tc.jsonInput

			// Test with truncation
			t.Run("truncated", func(t *testing.T) {
				verifyTestCases(t, &pinner, tc.truncatedTestCase, true, true)
			})

			// Test without truncation
			t.Run("not truncated", func(t *testing.T) {
				verifyTestCases(t, &pinner, tc.notTruncatedTestCase, false, true)
			})
		})
	}
}

func TestJSONEncode_TruncatedInput_AllStringError(t *testing.T) {
	var pinner runtime.Pinner
	defer pinner.Unpin()

	input := `{"a": 1, "b": [[], false, {"c": 3.0}, "d"], "e": true}`

	// Loop through the input string and remove a character at the end and check that the parsing errors out every time
	for i := 0; i < len(input); i++ {
		if i == 0 {
			continue // First loop is the full valid json
		}

		newInput := input[:len(input)-i]
		testName := fmt.Sprintf("truncated invalid structure: %s", newInput)
		t.Run(testName, func(t *testing.T) {
			tc := testCase{
				name:                testName,
				jsonInput:           newInput,
				expectEncodingError: true,
			}

			verifyTestCases(t, &pinner, tc, false, false)
		})
	}
}

func TestJSONEncode_TruncatedInvalidStructure(t *testing.T) {
	var pinner runtime.Pinner
	defer pinner.Unpin()

	inputsEncodingError := []string{
		`{'k'`,
		`{[]}`,
		`{{}}`,
		`{hello: world}`,
		`{"hello":: 1}`,
		`{1: 2}`,
		`[,,]`,
		`[1,,2]`,
		`["Hello", 3.14, true, ]`,
		`["Hello", 3.14, true,]`,
		`["k": "v"]`,
		`hello world`,
	}

	t.Run("truncated invalid structure", func(t *testing.T) {
		for _, input := range inputsEncodingError {
			testName := "truncated invalid structure: " + input
			t.Run(testName, func(t *testing.T) {
				tc := testCase{
					name:                testName,
					jsonInput:           input,
					expectEncodingError: true,
				}

				verifyTestCases(t, &pinner, tc, true, true)
			})
		}
	})

	// Using the current `jsoniter` library, when a provided json is malformed on the last character,
	// it's difficult to know if the error is due to an EOF or by the fact that the json is malformed (while being initially truncated).
	// In this case, we won't errors out but return the partially parsed object as is.

	inputsThatShouldErrorButPass := map[string]any{
		`{"k": 1    ,}`: map[string]any{"k": int64(1)}, // '}' unexpected, should have saw a '"' for string key
		`{"k": 1, "v}`:  map[string]any{"k": int64(1)}, // '}' unexpected, should have saw a '"' for end of string key
		`{"a}`:          map[string]any{},              // No key because the value is not parsed completely
		`{"t"}`:         map[string]any{},              // No key because the ':' is not found
		`{"k"`:          map[string]any{},              // No key because the ':' is not found
		`[1"`:           []any{int64(1)},               // '1' parsed then iter over, '"' is found and was expecting a comma or a closing bracket
	}

	t.Run("truncated invalid structure that should error but pass", func(t *testing.T) {
		for k, v := range inputsThatShouldErrorButPass {
			t.Run(k, func(t *testing.T) {
				tc := testCase{
					jsonInput:    k,
					expectOutput: v,
				}

				verifyTestCases(t, &pinner, tc, true, true)
			})
		}
	})
}

// newTestJSONEncodable creates a new JSON encoder for testing purposes
// Overrides the truncation behavior to simulate different scenarios
func newTestJSONEncodable(truncated bool, data []byte) *Encodable {
	return &Encodable{
		truncated: truncated,
		data:      data,
	}
}

// newTestMaxJsonEncoderConfig creates a new JSON encoder configuration for testing purposes with all configs set to max
func newTestMaxJsonEncoderConfig(pinner *runtime.Pinner) libddwaf.EncoderConfig {
	tm, err := timer.NewTimer(timer.WithUnlimitedBudget())
	if err != nil {
		panic(err)
	}

	return libddwaf.EncoderConfig{
		Pinner:           pinner,
		Timer:            tm,
		MaxObjectDepth:   math.MaxInt,
		MaxContainerSize: math.MaxInt,
		MaxStringSize:    math.MaxInt,
	}
}

// sortTruncations sorts the truncation values for consistent comparison
func sortTruncations(truncations map[libddwaf.TruncationReason][]int) map[libddwaf.TruncationReason][]int {
	if truncations == nil {
		return nil
	}
	for k := range truncations {
		sort.Ints(truncations[k])
	}
	return truncations
}

func BenchmarkEncoder(b *testing.B) {
	rnd := rand.New(rand.NewSource(33))
	buf := make([]byte, 16384)
	n, err := rnd.Read(buf)
	fullstr := string(buf)
	encodeTimer, _ := timer.NewTimer(timer.WithUnlimitedBudget())
	var pinner runtime.Pinner
	defer pinner.Unpin()

	for _, l := range []int{4, 16, 128, 1024, 4096} {
		config := libddwaf.EncoderConfig{
			Pinner:           &pinner,
			MaxObjectDepth:   10,
			MaxStringSize:    1 * 1024 * 1024,
			MaxContainerSize: 100,
			Timer:            encodeTimer,
		}
		b.Run(fmt.Sprintf("%d", l), func(b *testing.B) {
			b.ReportAllocs()
			str := fullstr[:l]
			slice := []string{str, str, str, str, str, str, str, str, str, str}
			data := map[string]any{
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
			bytes, err := json.Marshal(data)
			if err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				encodable := Encodable{data: bytes}
				var wafObj libddwaf.WAFObject
				truncations, err := encodable.Encode(config, &wafObj, 0)
				if err != nil {
					b.Fatalf("Error encoding: %v", err)
				}

				runtime.KeepAlive(encodable)
				runtime.KeepAlive(wafObj)
				runtime.KeepAlive(truncations)
			}
		})
	}
}
