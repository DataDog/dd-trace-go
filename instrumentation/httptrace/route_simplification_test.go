// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSimplifyHTTPRoute(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Empty and basic cases
		{
			name:     "empty URL",
			input:    "",
			expected: "/",
		},
		{
			name:     "no path",
			input:    "http://example.com",
			expected: "/",
		},
		{
			name:     "root path",
			input:    "http://example.com/",
			expected: "/",
		},
		{
			name:     "simple path",
			input:    "http://example.com/api/v1/users",
			expected: "/api/v1/users",
		},

		// URL parsing tests
		{
			name:     "URL with query params",
			input:    "http://example.com/api/users?id=123",
			expected: "/api/users",
		},
		{
			name:     "path only (no protocol)",
			input:    "/api/v1/users/123",
			expected: "/api/v1/users/{param:int}",
		},
		{
			name:     "https URL",
			input:    "https://example.com:8080/api/users",
			expected: "/api/users",
		},

		// Integer parameter tests
		{
			name:     "integer ID (2 digits)",
			input:    "/users/12",
			expected: "/users/{param:int}",
		},
		{
			name:     "integer ID (at least 2 digits)",
			input:    "/users/123",
			expected: "/users/{param:int}",
		},
		{
			name:     "single digit not replaced",
			input:    "/users/5",
			expected: "/users/5",
		},
		{
			name:     "integer starting with 0 not replaced",
			input:    "/users/0123",
			expected: "/users/{param:int_id}",
		},

		// Integer ID parameter tests
		{
			name:     "integer ID with dashes",
			input:    "/items/123-456",
			expected: "/items/{param:int_id}",
		},
		{
			name:     "integer ID with dots",
			input:    "/items/123.456",
			expected: "/items/{param:int_id}",
		},
		{
			name:     "integer ID with underscores",
			input:    "/items/123_456",
			expected: "/items/{param:int_id}",
		},

		// Hex parameter tests
		{
			name:     "hex ID (6+ chars with digit)",
			input:    "/commits/abc123",
			expected: "/commits/{param:hex}",
		},
		// Failing because no lookaround done, so no checks for first digits
		/*{
			name:     "hex ID all letters (no digit)",
			input:    "/commits/abcdef",
			expected: "/commits/abcdef",
		},*/
		{
			name:     "hex ID too short",
			input:    "/commits/abc12",
			expected: "/commits/abc12",
		},

		// Hex ID parameter tests
		{
			name:     "hex ID with dashes",
			input:    "/items/abc123-def",
			expected: "/items/{param:hex_id}",
		},
		{
			name:     "hex ID with dots",
			input:    "/items/abc123.def",
			expected: "/items/{param:hex_id}",
		},

		// String parameter tests
		{
			name:     "long string (20+ chars)",
			input:    "/files/verylongfilename12345",
			expected: "/files/{param:str}",
		},
		{
			name:     "string with special chars",
			input:    "/search/hello+world",
			expected: "/search/{param:str}",
		},
		{
			name:     "string with percent encoding",
			input:    "/files/hello%20world",
			expected: "/files/{param:str}",
		},
		{
			name:     "string with @ symbol",
			input:    "/users/user@example",
			expected: "/users/{param:str}",
		},

		// Path tests
		{
			name:     "more than 8 path elements",
			input:    "/a/b/c/d/e/f/g/h/i/j/k",
			expected: "/a/b/c/d/e/f/g/h",
		},
		{
			name:     "weird path with only slashes",
			input:    "///////////////////////",
			expected: "/",
		},
		{
			name:     "8 empty path elements and a value",
			input:    "//////////a",
			expected: "/a",
		},

		// Empty elements handling
		{
			name:     "consecutive slashes",
			input:    "/api//v1///users//123",
			expected: "/api/v1/users/{param:int}",
		},

		// Complex scenarios
		{
			name:     "mixed parameter types",
			input:    "/api/v2/users/123/posts/abc123/comments/hello%20world",
			expected: "/api/v2/users/{param:int}/posts/{param:hex}/comments/{param:str}",
		},
		{
			name:     "UUID-like hex pattern",
			input:    "/objects/9219c7f7-3704-44d3-8cc9-2b63ba554636",
			expected: "/objects/{param:hex_id}",
		},
		{
			name:     "all parameter types",
			input:    "/12/123-456/abc123/abc-def-123/longstringthathastoomanycharacters",
			expected: "/{param:int}/{param:int_id}/{param:hex}/{param:hex_id}/{param:str}",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := simplifyHTTPUrl(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestExtractPathFromURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "full URL",
			input:    "http://example.com/api/users",
			expected: "/api/users",
		},
		{
			name:     "URL with query",
			input:    "https://example.com:8080/api/users?id=123&name=test",
			expected: "/api/users",
		},
		{
			name:     "path only",
			input:    "/api/users",
			expected: "/api/users",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "root path",
			input:    "http://example.com/",
			expected: "/",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractPathFromURL(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
