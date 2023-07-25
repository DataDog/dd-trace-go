// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httpsec

import (
	"encoding/json"
	"fmt"
	"testing"

	testlib "gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/_testlib"

	"github.com/stretchr/testify/require"
)

func TestTags(t *testing.T) {
	for _, eventCase := range []struct {
		name          string
		events        []json.RawMessage
		expectedTag   string
		expectedError bool
	}{
		{
			name:   "no-event",
			events: nil,
		},
		{
			name:        "one-event",
			events:      []json.RawMessage{json.RawMessage(`["one","two"]`)},
			expectedTag: `{"triggers":["one","two"]}`,
		},
		{
			name:          "one-event-with-json-error",
			events:        []json.RawMessage{json.RawMessage(`["one",two"]`)},
			expectedError: true,
		},
		{
			name:        "two-events",
			events:      []json.RawMessage{json.RawMessage(`["one","two"]`), json.RawMessage(`["three","four"]`)},
			expectedTag: `{"triggers":["one","two","three","four"]}`,
		},
		{
			name:          "two-events-with-json-error",
			events:        []json.RawMessage{json.RawMessage(`["one","two"]`), json.RawMessage(`["three,"four"]`)},
			expectedError: true,
		},
		{
			name:          "three-events-with-json-error",
			events:        []json.RawMessage{json.RawMessage(`["one","two"]`), json.RawMessage(`["three","four"]`), json.RawMessage(`"five"`)},
			expectedError: true,
		},
	} {
		eventCase := eventCase
		for _, reqHeadersCase := range []struct {
			name         string
			headers      map[string][]string
			expectedTags map[string]interface{}
		}{
			{
				name: "zero-headers",
			},
			{
				name: "xff-header",
				headers: map[string][]string{
					"X-Forwarded-For": {"1.2.3.4", "4.5.6.7"},
					"my-header":       {"something"},
				},
				expectedTags: map[string]interface{}{
					"http.request.headers.x-forwarded-for": "1.2.3.4,4.5.6.7",
				},
			},
			{
				name: "xff-header",
				headers: map[string][]string{
					"X-Forwarded-For": {"1.2.3.4"},
					"my-header":       {"something"},
				},
				expectedTags: map[string]interface{}{
					"http.request.headers.x-forwarded-for": "1.2.3.4",
				},
			},
			{
				name: "no-monitored-headers",
				headers: map[string][]string{
					"my-header": {"something"},
				},
			},
		} {
			reqHeadersCase := reqHeadersCase
			for _, respHeadersCase := range []struct {
				name         string
				headers      map[string][]string
				expectedTags map[string]interface{}
			}{
				{
					name: "zero-headers",
				},
				{
					name: "ct-header",
					headers: map[string][]string{
						"Content-Type": {"application/json"},
						"my-header":    {"something"},
					},
					expectedTags: map[string]interface{}{
						"http.response.headers.content-type": "application/json",
					},
				},
				{
					name: "no-monitored-headers",
					headers: map[string][]string{
						"my-header": {"something"},
					},
				},
			} {
				respHeadersCase := respHeadersCase
				t.Run(fmt.Sprintf("%s-%s-%s", eventCase.name, reqHeadersCase.name, respHeadersCase.name), func(t *testing.T) {
					var span testlib.MockSpan
					err := setSecurityEventsTags(&span, eventCase.events)
					if eventCase.expectedError {
						require.Error(t, err)
						return
					}
					require.NoError(t, err)
					setRequestHeadersTags(&span, reqHeadersCase.headers)
					setResponseHeadersTags(&span, respHeadersCase.headers)

					if eventCase.events != nil {
						testlib.RequireContainsMapSubset(t, span.Tags, map[string]interface{}{
							"_dd.appsec.json": eventCase.expectedTag,
							"manual.keep":     true,
							"appsec.event":    true,
							"_dd.origin":      "appsec",
						})
					}

					if l := len(reqHeadersCase.expectedTags); l > 0 {
						testlib.RequireContainsMapSubset(t, span.Tags, reqHeadersCase.expectedTags)
					}

					if l := len(respHeadersCase.expectedTags); l > 0 {
						testlib.RequireContainsMapSubset(t, span.Tags, respHeadersCase.expectedTags)
					}

					require.False(t, span.Finished)
				})
			}
		}
	}
}

func TestNormalizeHTTPHeaders(t *testing.T) {
	for _, tc := range []struct {
		headers  map[string][]string
		expected map[string]string
	}{
		{
			headers:  nil,
			expected: nil,
		},
		{
			headers: map[string][]string{
				"cookie": {"not-collected"},
			},
			expected: nil,
		},
		{
			headers: map[string][]string{
				"cookie":          {"not-collected"},
				"x-forwarded-for": {"1.2.3.4,5.6.7.8"},
			},
			expected: map[string]string{
				"x-forwarded-for": "1.2.3.4,5.6.7.8",
			},
		},
		{
			headers: map[string][]string{
				"cookie":          {"not-collected"},
				"x-forwarded-for": {"1.2.3.4,5.6.7.8", "9.10.11.12,13.14.15.16"},
			},
			expected: map[string]string{
				"x-forwarded-for": "1.2.3.4,5.6.7.8,9.10.11.12,13.14.15.16",
			},
		},
	} {
		headers := NormalizeHTTPHeaders(tc.headers)
		require.Equal(t, tc.expected, headers)
	}
}
