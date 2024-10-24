// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
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

func TestGetErrorCodesFromInput(t *testing.T) {
	codesOnly := "400,401,402"
	rangesOnly := "400-405,408-410"
	mixed := "400,403-405,407-410,412"
	invalid1 := "1,100-200-300-"
	invalid2 := "abc:@3$5^,"
	empty := ""
	t.Run("codesOnly", func(t *testing.T) {
		fn := GetErrorCodesFromInput(codesOnly)
		for i := 400; i <= 402; i++ {
			assert.True(t, fn(i))
		}
		assert.False(t, fn(500))
		assert.False(t, fn(0))
	})
	t.Run("rangesOnly", func(t *testing.T) {
		fn := GetErrorCodesFromInput(rangesOnly)
		for i := 400; i <= 405; i++ {
			assert.True(t, fn(i))
		}
		for i := 408; i <= 410; i++ {
			assert.True(t, fn(i))
		}
		assert.False(t, fn(406))
		assert.False(t, fn(411))
		assert.False(t, fn(500))
	})
	t.Run("mixed", func(t *testing.T) {
		fn := GetErrorCodesFromInput(mixed)
		assert.True(t, fn(400))
		assert.False(t, fn(401))
		for i := 403; i <= 405; i++ {
			assert.True(t, fn(i))
		}
		assert.False(t, fn(406))
		for i := 407; i <= 410; i++ {
			assert.True(t, fn(i))
		}
		assert.False(t, fn(411))
		assert.False(t, fn(500))
	})
	// invalid entries below should result in nils
	t.Run("invalid1", func(t *testing.T) {
		fn := GetErrorCodesFromInput(invalid1)
		assert.Nil(t, fn)
	})
	t.Run("invalid2", func(t *testing.T) {
		fn := GetErrorCodesFromInput(invalid2)
		assert.Nil(t, fn)
	})
	t.Run("empty", func(t *testing.T) {
		fn := GetErrorCodesFromInput(empty)
		assert.Nil(t, fn)
	})
}

func TestConfiguredErrorStatuses(t *testing.T) {
	defer os.Unsetenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES")
	t.Run("configured", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		os.Setenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES", "199-399,400,501")

		// reset config based on new DD_TRACE_HTTP_SERVER_ERROR_STATUSES value
		oldConfig := cfg
		defer func() { cfg = oldConfig }()
		cfg = newConfig()

		statuses := []int{0, 200, 400, 500}
		r := httptest.NewRequest(http.MethodGet, "/test", nil)
		for i, status := range statuses {
			sp, _ := StartRequestSpan(r)
			FinishRequestSpan(sp, status, nil)
			spans := mt.FinishedSpans()
			require.Len(t, spans, i+1)

			switch status {
			case 0:
				assert.Equal(t, "200", spans[i].Tag(ext.HTTPCode))
				assert.Nil(t, spans[i].Tag(ext.Error))
			case 200, 400:
				assert.Equal(t, strconv.Itoa(status), spans[i].Tag(ext.HTTPCode))
				assert.Equal(t, fmt.Errorf("%s: %s", strconv.Itoa(status), http.StatusText(status)), spans[i].Tag(ext.Error).(error))
			case 500:
				assert.Equal(t, strconv.Itoa(status), spans[i].Tag(ext.HTTPCode))
				assert.Nil(t, spans[i].Tag(ext.Error))
			}
		}
	})
	t.Run("zero", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		os.Setenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES", "0")

		// reset config based on new DD_TRACE_HTTP_SERVER_ERROR_STATUSES value
		oldConfig := cfg
		defer func() { cfg = oldConfig }()
		cfg = newConfig()

		r := httptest.NewRequest(http.MethodGet, "/test", nil)
		sp, _ := StartRequestSpan(r)
		FinishRequestSpan(sp, 0, nil)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Equal(t, "0", spans[0].Tag(ext.HTTPCode))
		assert.Equal(t, fmt.Errorf("0: %s", http.StatusText(0)), spans[0].Tag(ext.Error).(error))
	})
}

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
