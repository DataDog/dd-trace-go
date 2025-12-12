// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/)
// Copyright 2016 Datadog, Inc.

package urlsanitizer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// URLs with credentials - use Go's built-in redaction (preserves username, redacts password)
		{"https://user:password@example.com/path", "https://user:xxxxx@example.com/path"},
		{"http://admin:secret@db.example.com:5432/mydb", "http://admin:xxxxx@db.example.com:5432/mydb"},

		// URL with just username (no password) - preserved as-is
		{"http://token@example.com", "http://token@example.com"},

		// URLs without credentials - returned as-is
		{"https://example.com/path", "https://example.com/path"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"ftp://files.example.com/data", "ftp://files.example.com/data"},

		// Edge cases
		{"", ""},
		{"not-a-url", "not-a-url"}, // no credentials suspected, return as-is
		{"://invalid:password@host", "[REDACTED_URL_WITH_CREDENTIALS]"}, // unparseable but has credentials pattern
		{"://invalid", "://invalid"},                                    // unparseable but no credentials, return as-is
	}

	for _, test := range tests {
		result := SanitizeURL(test.input)
		assert.Equal(t, test.expected, result, "Failed for input: %s", test.input)
	}
}

func TestContainsCredentials(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		// Should detect credentials
		{"https://user:password@example.com", true},
		{"http://admin:secret@db.example.com:5432/mydb", true},
		{"postgres://user:pass@localhost:5432/db", true},

		// Should not detect credentials
		{"https://example.com/path", false},
		{"http://localhost:8080", false},
		{"ftp://files.example.com", false},
		{"http://token@example.com", false}, // no colon, so no password
		{"not-a-url", false},
		{"", false},
		{"://invalid", false}, // no valid scheme
	}

	for _, test := range tests {
		result := containsCredentials(test.input)
		assert.Equal(t, test.expected, result, "Failed for input: %s", test.input)
	}
}
