// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/ext"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/tracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/log"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/normalizer"

	"github.com/DataDog/appsec-internal-go/netip"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHeaderTagsFromRequest(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.Header.Set("header1", "val1")
	r.Header.Set("header2", " val2 ")
	r.Header.Set("header3", "v a l 3")
	r.Header.Set("x-datadog-header", "val4")

	expectedHeaderTags := map[string]string{
		"tag1": "val1",
		"tag2": "val2",
		"tag3": "v a l 3",
		"tag4": "val4",
	}

	hs := []string{"header1:tag1", "header2:tag2", "header3:tag3", "x-datadog-header:tag4"}
	ht := internal.NewLockMap(normalizer.HeaderTagSlice(hs))
	s, _ := StartRequestSpan(r, HeaderTagsFromRequest(r, ht))
	s.Finish()
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	for expectedTag, expectedTagVal := range expectedHeaderTags {
		assert.Equal(t, expectedTagVal, spans[0].Tags()[expectedTag])
	}
}

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

// TestClientIP tests behavior of StartRequestSpan based on
// the DD_TRACE_CLIENT_IP_ENABLED environment variable
func TestTraceClientIPFlag(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	tp := new(log.RecordLogger)
	defer log.UseLogger(tp)()

	// use 0.0.0.0 as ip address of all test cases
	// more comprehensive ip address testing is done in testing
	// of ClientIPTags in appsec/dyngo/instrumentation/httpsec
	validIPAddr := "0.0.0.0"

	type ipTestCase struct {
		name                string
		remoteAddr          string
		traceClientIPEnvVal string
		expectTrace         bool
		expectedIP          netip.Addr
	}

	oldConfig := cfg
	defer func() { cfg = oldConfig }()

	for _, tc := range []ipTestCase{
		{
			name:                "Trace client IP set to true",
			remoteAddr:          validIPAddr,
			expectedIP:          netip.MustParseAddr(validIPAddr),
			traceClientIPEnvVal: "true",
			expectTrace:         true,
		},
		{
			name:                "Trace client IP set to false",
			remoteAddr:          validIPAddr,
			expectedIP:          netip.MustParseAddr(validIPAddr),
			traceClientIPEnvVal: "false",
			expectTrace:         false,
		},
		{
			name:                "Trace client IP unset",
			remoteAddr:          validIPAddr,
			expectedIP:          netip.MustParseAddr(validIPAddr),
			traceClientIPEnvVal: "",
			expectTrace:         false,
		},
		{
			name:                "Trace client IP set to non-boolean value",
			remoteAddr:          validIPAddr,
			expectedIP:          netip.MustParseAddr(validIPAddr),
			traceClientIPEnvVal: "asdadsasd",
			expectTrace:         false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envTraceClientIPEnabled, tc.traceClientIPEnvVal)

			// reset config based on new DD_TRACE_CLIENT_IP_ENABLED value
			cfg = newConfig()

			r := httptest.NewRequest(http.MethodGet, "/somePath", nil)
			r.RemoteAddr = tc.remoteAddr
			s, _ := StartRequestSpan(r)
			s.Finish()
			spans := mt.FinishedSpans()
			targetSpan := spans[0]

			if tc.expectTrace {
				assert.Equal(t, tc.expectedIP.String(), targetSpan.Tag(ext.HTTPClientIP))
			} else {
				assert.NotContains(t, targetSpan.Tags(), ext.HTTPClientIP)
				if _, err := strconv.ParseBool(tc.traceClientIPEnvVal); err != nil && tc.traceClientIPEnvVal != "" {
					logs := tp.Logs()
					assert.Contains(t, logs[len(logs)-1], "Non-boolean value for env var DD_TRACE_CLIENT_IP_ENABLED")
					tp.Reset()
				}
			}
			mt.Reset()
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

func BenchmarkStartRequestSpan(b *testing.B) {
	b.ReportAllocs()
	r, err := http.NewRequest("GET", "http://example.com", nil)
	if err != nil {
		b.Errorf("Failed to create request: %v", err)
		return
	}
	opts := []ddtrace.StartSpanOption{
		tracer.ServiceName("SomeService"),
		tracer.ResourceName("SomeResource"),
		tracer.Tag(ext.HTTPRoute, "/some/route/?"),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		StartRequestSpan(r, opts...)
	}
}
