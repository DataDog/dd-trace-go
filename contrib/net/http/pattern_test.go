// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPathParams(t *testing.T) {
	for _, tt := range []struct {
		name     string
		pattern  string
		url      string
		expected map[string]string
	}{
		{
			name:     "simple",
			pattern:  "/foo/{bar}",
			url:      "/foo/123",
			expected: map[string]string{"bar": "123"},
		},
		{
			name:     "multiple",
			pattern:  "/foo/{bar}/{baz}",
			url:      "/foo/123/456",
			expected: map[string]string{"bar": "123", "baz": "456"},
		},
		{
			name:     "nested",
			pattern:  "/foo/{bar}/baz/{qux}",
			url:      "/foo/123/baz/456",
			expected: map[string]string{"bar": "123", "qux": "456"},
		},
		{
			name:     "empty",
			pattern:  "/foo/{bar}",
			url:      "/foo/",
			expected: map[string]string{"bar": ""},
		},
		{
			name:     "http method",
			pattern:  "GET /foo/{bar}",
			url:      "/foo/123",
			expected: map[string]string{"bar": "123"},
		},
		{
			name:     "host",
			pattern:  "example.com/foo/{bar}",
			url:      "http://example.com/foo/123",
			expected: map[string]string{"bar": "123"},
		},
		{
			name:     "host and method",
			pattern:  "GET example.com/foo/{bar}",
			url:      "http://example.com/foo/123",
			expected: map[string]string{"bar": "123"},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mux := NewServeMux()
			mux.HandleFunc(tt.pattern, func(_ http.ResponseWriter, r *http.Request) {
				_, pattern := mux.Handler(r)
				params := patternValues(pattern, r)
				assert.Equal(t, tt.expected, params)
			})

			r := httptest.NewRequest("GET", tt.url, nil)
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, r)
		})
	}
}

func TestPatternNames(t *testing.T) {
	tests := []struct {
		pattern  string
		expected []string
		err      bool
	}{
		{"/foo/{bar}", []string{"bar"}, false},
		{"/foo/{bar}/{baz}", []string{"bar", "baz"}, false},
		{"/foo/{bar}/{bar}", nil, true},
		{"/foo/{bar}...", nil, true},
		{"/foo/{bar}.../baz", nil, true},
		{"/foo/{bar}/{baz}...", nil, true},
		{"/foo/{bar", nil, true},
		{"/foo/{bar{baz}}", nil, true},
		{"/foo/{bar!}", nil, true},
		{"/foo/{}", nil, true},
		{"{}", nil, true},
		{"GET /foo/{bar}", []string{"bar"}, false},
		{"POST /foo/{bar}/{baz}", []string{"bar", "baz"}, false},
		{"PUT /foo/{bar}/{bar}", nil, true},
		{"DELETE /foo/{bar}...", nil, true},
		{"PATCH /foo/{bar}.../baz", nil, true},
		{"OPTIONS /foo/{bar}/{baz}...", nil, true},
		{"GET /foo/{bar", nil, true},
		{"POST /foo/{bar{baz}}", nil, true},
		{"PUT /foo/{bar!}", nil, true},
		{"DELETE /foo/{}", nil, true},
		{"OPTIONS {}", nil, true},
		{"GET example.com/foo/{bar}", []string{"bar"}, false},
		{"POST example.com/foo/{bar}/{baz}", []string{"bar", "baz"}, false},
		{"PUT example.com/foo/{bar}/{bar}", nil, true},
		{"DELETE example.com/foo/{bar}...", nil, true},
		{"PATCH example.com/foo/{bar}.../baz", nil, true},
		{"OPTIONS example.com/foo/{bar}/{baz}...", nil, true},
		{"GET example.com/foo/{bar", nil, true},
		{"POST example.com/foo/{bar{baz}}", nil, true},
		{"PUT example.com/foo/{bar!}", nil, true},
		{"DELETE example.com/foo/{}", nil, true},
		{"OPTIONS example.com/{}", nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			names, err := patternNames(tt.pattern)
			if tt.err {
				assert.Error(t, err)
				assert.Nil(t, names)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expected, names)
			}
		})
	}
}
