// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package httptrace

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTokenizer(t *testing.T) {
	tokenizer := new(tokenizer)

	type val struct {
		tokenType tokenType
		data      string
	}

	type testCase struct {
		path     string
		expected []val
	}

	testCases := []testCase{
		{
			path:     "/",
			expected: nil,
		},
		{
			path:     "/1",
			expected: []val{{tokenWildcard, "1"}},
		},
		{
			path:     "/foo/1",
			expected: []val{{tokenString, "foo"}, {tokenWildcard, "1"}},
		},
		{
			path:     "/abc/def",
			expected: []val{{tokenString, "abc"}, {tokenString, "def"}},
		},
		{
			path:     "/abc/123/def",
			expected: []val{{tokenString, "abc"}, {tokenWildcard, "123"}, {tokenString, "def"}},
		},
		{
			path:     "/abc/def123",
			expected: []val{{tokenString, "abc"}, {tokenWildcard, "def123"}},
		},
		{
			path:     "/abc#def",
			expected: []val{{tokenWildcard, "abc#def"}},
		},
		{
			path:     "/v5/abc",
			expected: []val{{tokenAPIVersion, "v5"}, {tokenString, "abc"}},
		},
		{
			path:     "/こんにちは/世界",
			expected: []val{{tokenWildcard, "こんにちは"}, {tokenWildcard, "世界"}},
		},
		{
			path:     "/abc/123/🌟",
			expected: []val{{tokenString, "abc"}, {tokenWildcard, "123"}, {tokenWildcard, "🌟"}},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			tokenizer.Reset(tc.path)
			var got []val
			for tokenizer.Next() {
				tokenType, tokenValue := tokenizer.Value()
				got = append(got, val{tokenType, tokenValue})
			}

			assert.Equalf(t, tc.expected, got, "tokenization of %s should have returned %s. got %s", tc.path, tc.expected, got)
		})
	}
}

func TestURLQuantizer(t *testing.T) {
	var quantizer urlQuantizer

	type testCase struct {
		path     string
		expected string
	}

	testCases := []testCase{
		{
			path:     "/",
			expected: "/",
		},
		{
			path:     "/a",
			expected: "/a",
		},
		{
			path:     "/1",
			expected: "/*",
		},
		{
			path:     "/abc",
			expected: "/abc",
		},
		{
			path:     "/trailing/slash/",
			expected: "/trailing/slash/",
		},
		{
			path:     "/users/1/view",
			expected: "/users/*/view",
		},
		{
			path:     "/abc/def",
			expected: "/abc/def",
		},
		{
			path:     "/abc/123/def",
			expected: "/abc/*/def",
		},
		{
			path:     "/abc/def123",
			expected: "/abc/*",
		},
		{
			path:     "/abc#def",
			expected: "/*",
		},
		{
			path:     "/v5/abc",
			expected: "/v5/abc",
		},
		{
			path:     "/latest/meta-data",
			expected: "/latest/meta-data",
		},
		{
			path:     "/health_check",
			expected: "/health_check",
		},
		{
			path:     "/abc/F05065B2-7934-4480-8500-A2C40D76F59F",
			expected: "/abc/*",
		},
		{
			path:     "/DataDog/datadog-agent/pull/19720",
			expected: "/DataDog/datadog-agent/pull/*",
		},
		{
			path:     "/DataDog/datadog-agent/blob/22ba7d3d9d7cba67886dc905970d7f2f68b37dc5/pkg/network/protocols/http/quantization_test.go",
			expected: "/DataDog/datadog-agent/blob/*/pkg/network/protocols/http/*",
		},
		{
			path:     "/uuid/v1/f475ca90-71ab-11ee-b962-0242ac120002",
			expected: "/uuid/v1/*",
		},
		{
			path:     "/uuid/v4/0253ee45-3098-4a7e-8569-73a99a9fc030",
			expected: "/uuid/v4/*",
		},
		{
			path:     "/こんにちは/世界",
			expected: "/*/*",
		},
		{
			path:     "/abc/123/🌟",
			expected: "/abc/*/*",
		},
	}
	for _, tc := range testCases {
		t.Run(tc.path, func(t *testing.T) {
			result := quantizer.Quantize(tc.path)
			assert.Equalf(t, tc.expected, result, "expected: %s, got: %s", tc.expected, result)

			// Test quantization a second time to ensure idempotency.
			// We do this to validate that bringing the quantization code to
			// the agent-side won't cause any issues for the backend, which uses a
			// similar set of heuristics. In other words, an agent payload with
			// pre-quantized endpoint arriving at the backend should be a no-op.
			result = quantizer.Quantize(result)
			assert.Equalf(t, tc.expected, result, "expected: %s, got: %s", tc.expected, result)
		})
	}
}

// The purpose of this benchmark is to ensure that the whole quantization process doesn't allocate
func BenchmarkQuantization(b *testing.B) {
	// This should trigger the quantization since `/users/1/view` becomes
	// `/users/*/view` post-quantization (see test case above)
	path := "/users/1/view"

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runtime.KeepAlive(QuantizeURL(path))
	}
}

// This benchmark represents the case where a path does *not* trigger a quantization
func BenchmarkQuantizationHappyPath(b *testing.B) {
	var quantizer urlQuantizer
	path := "/foo/bar"
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		runtime.KeepAlive(quantizer.Quantize(path))
	}
}
