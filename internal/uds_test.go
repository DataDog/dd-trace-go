// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2025 Datadog, Inc.

package internal

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

func TestUDSClientTransportConfig(t *testing.T) {
	client := UDSClient("/var/run/datadog/apm.socket", 10*time.Second)
	tr, ok := client.Transport.(*http.Transport)
	require.True(t, ok, "Transport should be *http.Transport")
	assert.Equal(t, 100, tr.MaxIdleConns)
	assert.Equal(t, 100, tr.MaxIdleConnsPerHost)
	assert.Equal(t, 90*time.Second, tr.IdleConnTimeout)
	assert.Equal(t, 10*time.Second, tr.TLSHandshakeTimeout)
	assert.Equal(t, 1*time.Second, tr.ExpectContinueTimeout)
}
