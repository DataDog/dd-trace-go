// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnixDataSocketURL(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected *url.URL
	}{
		{
			name: "empty-path",
			path: "",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_",
			},
		},
		{
			name: "no-special-chars",
			path: "path",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path",
			},
		},
		{
			name: "with-colon",
			path: "path:with:colons",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_with_colons",
			},
		},
		{
			name: "with-forward-slash",
			path: "path/with/slashes",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_with_slashes",
			},
		},
		{
			name: "with-backward-slash",
			path: `path\with\backslashes`,
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_with_backslashes",
			},
		},
		{
			name: "mixed-special-chars",
			path: `path:with/all\chars`,
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_with_all_chars",
			},
		},
		{
			name: "leading-special-char-colon",
			path: ":path",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS__path",
			},
		},
		{
			name: "trailing-special-char-colon",
			path: "path:",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_",
			},
		},
		{
			name: "leading-special-char-slash",
			path: "/path",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS__path",
			},
		},
		{
			name: "trailing-special-char-slash",
			path: "path/",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_",
			},
		},
		{
			name: "leading-special-char-backslash",
			path: `\path`,
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS__path",
			},
		},
		{
			name: "trailing-special-char-backslash",
			path: `path\`,
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path_",
			},
		},
		{
			name: "multiple-leading-special-chars",
			path: "://path",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS____path",
			},
		},
		{
			name: "multiple-trailing-special-chars",
			path: "path://",
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS_path___",
			},
		},
		{
			name: "all-special-chars",
			path: `:/\`,
			expected: &url.URL{
				Scheme: "http",
				Host:   "UDS____",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, UnixDataSocketURL(tt.path))
		})
	}
}
