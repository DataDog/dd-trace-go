// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package tracer

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tinylib/msgp/msgp"

	"github.com/DataDog/dd-trace-go/v2/internal/civisibility/constants"
	"github.com/DataDog/dd-trace-go/v2/internal/urlsanitizer"
)

func TestCiVisibilityTransport(t *testing.T) {
	t.Run("agentless", func(t *testing.T) { runTransportTest(t, true, true) })
	t.Run("agentless_no_api_key", func(t *testing.T) { runTransportTest(t, true, false) })
	t.Run("agentbased", func(t *testing.T) { runTransportTest(t, false, true) })
}

func runTransportTest(t *testing.T, agentless, shouldSetAPIKey bool) {
	assert := assert.New(t)

	testCases := []struct {
		payload [][]*Span
	}{
		{getTestTrace(1, 1)},
		{getTestTrace(10, 1)},
		{getTestTrace(100, 10)},
	}

	remainingEvents := 1000 + 10 + 1
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		hits++
		metaLang := r.Header.Get("Datadog-Meta-Lang")
		assert.NotNil(metaLang)

		if agentless && shouldSetAPIKey {
			apikey := r.Header.Get("dd-api-key")
			assert.Equal("12345", apikey)
		}

		contentType := r.Header.Get("Content-Type")
		assert.Equal("application/msgpack", contentType)

		assert.True(strings.HasSuffix(r.RequestURI, TestCyclePath))

		bodyBuffer := new(bytes.Buffer)
		if r.Header.Get("Content-Encoding") == "gzip" {
			gzipReader, err := gzip.NewReader(r.Body)
			assert.NoError(err)

			_, err = bodyBuffer.ReadFrom(gzipReader)
			assert.NoError(err)
		} else {
			_, err := bodyBuffer.ReadFrom(r.Body)
			assert.NoError(err)
		}

		var testCyclePayload ciTestCyclePayload
		err := msgp.Decode(bodyBuffer, &testCyclePayload)
		assert.NoError(err)

		var events ciVisibilityEvents
		err = msgp.Decode(bytes.NewBuffer(testCyclePayload.Events), &events)
		assert.NoError(err)

		remainingEvents = remainingEvents - len(events)
	}))
	defer srv.Close()

	parsedURL, _ := url.Parse(srv.URL)
	c := config{
		ciVisibilityEnabled: true,
		httpClient:          defaultHTTPClient(0),
		agentURL:            parsedURL,
	}

	// Set CI Visibility environment variables for the test
	if agentless {
		t.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "1")
		t.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, srv.URL)
		if shouldSetAPIKey {
			t.Setenv(constants.APIKeyEnvironmentVariable, "12345")
		}
	}

	for _, tc := range testCases {
		transport := newCiVisibilityTransport(&c)

		p := newCiVisibilityPayload()
		for _, t := range tc.payload {
			for _, span := range t {
				err := p.push(getCiVisibilityEvent(span))
				assert.NoError(err)
			}
		}

		_, err := transport.send(p.payload)
		assert.NoError(err)
	}
	assert.Equal(hits, len(testCases))
	assert.Equal(remainingEvents, 0)
}

func TestCIVisibilityTransportSecureLogging(t *testing.T) {
	t.Run("agentless_mode_with_credentials_in_url", func(t *testing.T) {
		// Set environment variables with sensitive data
		os.Setenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable, "true")
		os.Setenv(constants.APIKeyEnvironmentVariable, "test-api-key")
		os.Setenv(constants.CIVisibilityAgentlessURLEnvironmentVariable, "https://user:secret@example.com/path")
		defer func() {
			os.Unsetenv(constants.CIVisibilityAgentlessEnabledEnvironmentVariable)
			os.Unsetenv(constants.APIKeyEnvironmentVariable)
			os.Unsetenv(constants.CIVisibilityAgentlessURLEnvironmentVariable)
		}()

		cfg := &config{}
		transport := newCiVisibilityTransport(cfg)
		assert.NotNil(t, transport)

		// Verify URL still contains credentials (stored for actual use)
		assert.Contains(t, transport.testCycleURLPath, "https://user:secret@example.com/path/api/v2/citestcycle")
	})

	t.Run("sanitize_url_function", func(t *testing.T) {
		// Test the sanitizeURL function directly
		tests := []struct {
			input    string
			expected string
		}{
			{"https://user:password@example.com/path", "https://user:xxxxx@example.com/path"},
			{"http://token@example.com", "http://token@example.com"}, // no password, so username preserved
			{"https://user:pass@example.com:8080/path", "https://user:xxxxx@example.com:8080/path"},
			{"https://example.com/path", "https://example.com/path"},
			{"", ""},
			{"://invalid", "://invalid"}, // unparseable but no credentials, returned as-is
		}

		for _, test := range tests {
			result := urlsanitizer.SanitizeURL(test.input)
			assert.Equal(t, test.expected, result, "Failed for input: %s", test.input)
		}
	})
}
