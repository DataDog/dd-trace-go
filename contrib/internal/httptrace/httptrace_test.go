// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec/dyngo/instrumentation"
)

func TestStartRequestSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	r := httptest.NewRequest(http.MethodGet, "/somePath", nil)
	s, _ := StartRequestSpan(r)
	s.Finish()
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "example.com", spans[0].Tag("http.host"))
}

func TestClientIP(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	type ipTestCase struct {
		name          string
		remoteAddr    string
		expectedIP    instrumentation.NetaddrIP
		enableTraceIP string
	}
	ipAddr := "0.0.0.0"

	for _, tc := range []ipTestCase{
		{
			name:          "enable-ip",
			remoteAddr:    ipAddr,
			expectedIP:    instrumentation.NetaddrMustParseIP(ipAddr),
			enableTraceIP: "true",
		},
		{
			name:          "disable-ip",
			remoteAddr:    ipAddr,
			expectedIP:    instrumentation.NetaddrMustParseIP(ipAddr),
			enableTraceIP: "false",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.enableTraceIP == "true" {
				t.Setenv("DD_TRACE_CLIENT_IP_ENABLED", tc.enableTraceIP)
			}
			r := httptest.NewRequest(http.MethodGet, "/somePath", nil)
			r.RemoteAddr = tc.remoteAddr
			s, _ := StartRequestSpan(r)
			s.Finish()
			spans := mt.FinishedSpans()
			targetSpan := spans[len(spans)-1]
			if tc.enableTraceIP == "true" {
				assert.Equal(t, tc.expectedIP.String(), targetSpan.Tag(ext.HTTPClientIP))
				assert.Equal(t, tc.expectedIP.String(), targetSpan.Tag("network.client.ip"))
			} else {
				assert.NotContains(t, targetSpan.Tags(), ext.HTTPClientIP)
				assert.NotContains(t, targetSpan.Tags(), "network.client.ip")
			}
		})
	}
}

func TestURLTag(t *testing.T) {
	type URLTestCase struct {
		name, expectedURL, host, port, path, query, fragment string
	}
	for _, tc := range []URLTestCase{
		{
			name:        "no-host",
			expectedURL: "/test",
			path:        "/test",
		},
		{
			name:        "basic",
			expectedURL: "http://example.com",
			host:        "example.com",
		},
		{
			name:        "basic-path",
			expectedURL: "http://example.com/test",
			host:        "example.com",
			path:        "/test",
		},
		{
			name:        "basic-port",
			expectedURL: "http://example.com:8080",
			host:        "example.com",
			port:        "8080",
		},
		{
			name:        "basic-fragment",
			expectedURL: "http://example.com#test",
			host:        "example.com",
			fragment:    "test",
		},
		{
			name:        "query1",
			expectedURL: "http://example.com?test1=test2",
			host:        "example.com",
			query:       "test1=test2",
		},
		{
			name:        "query2",
			expectedURL: "http://example.com?test1=test2&test3=test4",
			host:        "example.com",
			query:       "test1=test2&test3=test4",
		},
		{
			name:        "combined",
			expectedURL: "http://example.com:7777/test?test1=test2&test3=test4#test",
			host:        "example.com",
			path:        "/test",
			query:       "test1=test2&test3=test4",
			port:        "7777",
			fragment:    "test",
		},
		{
			name:        "basic-obfuscation1",
			expectedURL: "http://example.com?<redacted>",
			host:        "example.com",
			query:       "token=value",
		},
		{
			name:        "basic-obfuscation2",
			expectedURL: "http://example.com?test0=test1&<redacted>&test1=test2",
			host:        "example.com",
			query:       "test0=test1&token=value&test1=test2",
		},
		{
			name:        "combined-obfuscation",
			expectedURL: "http://example.com:7777/test?test1=test2&<redacted>&test3=test4#test",
			host:        "example.com",
			path:        "/test",
			query:       "test1=test2&token=value&test3=test4",
			port:        "7777",
			fragment:    "test",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := http.Request{
				URL: &url.URL{
					Path:     tc.path,
					RawQuery: tc.query,
					Fragment: tc.fragment,
				},
				Host: tc.host,
			}
			if tc.port != "" {
				r.Host += ":" + tc.port
			}
			url := urlFromRequest(&r)
			require.Equal(t, tc.expectedURL, url)
		})
	}
}
