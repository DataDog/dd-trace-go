// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/appsec"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var securityTestingHeaders = [...]struct {
	header string
	tag    string
}{
	{header: "x-datadog-endpoint-scan", tag: ext.HTTPRequestHeaders + ".x-datadog-endpoint-scan"},
	{header: "x-datadog-security-test", tag: ext.HTTPRequestHeaders + ".x-datadog-security-test"},
}

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

		// re-run config defaults based on new DD_TRACE_HTTP_SERVER_ERROR_STATUSES value
		ResetCfg()

		statuses := []int{0, 200, 400, 500}
		r := httptest.NewRequest(http.MethodGet, "/test", nil)
		for i, status := range statuses {
			sp, _, _ := StartRequestSpan(r)
			FinishRequestSpan(sp, status, nil)
			spans := mt.FinishedSpans()
			require.Len(t, spans, i+1)

			switch status {
			case 0:
				assert.Equal(t, "200", spans[i].Tag(ext.HTTPCode))
				assert.Nil(t, spans[i].Tag(ext.ErrorMsg))
			case 200, 400:
				assert.Equal(t, strconv.Itoa(status), spans[i].Tag(ext.HTTPCode))
				assert.Equal(t, fmt.Sprintf("%s: %s", strconv.Itoa(status), http.StatusText(status)), spans[i].Tag(ext.ErrorMsg))
			case 500:
				assert.Equal(t, strconv.Itoa(status), spans[i].Tag(ext.HTTPCode))
				assert.Nil(t, spans[i].Tag(ext.ErrorMsg))
			}
		}
	})
	t.Run("zero", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		os.Setenv("DD_TRACE_HTTP_SERVER_ERROR_STATUSES", "0")

		// re-run config defaults based on new DD_TRACE_HTTP_SERVER_ERROR_STATUSES value
		ResetCfg()

		r := httptest.NewRequest(http.MethodGet, "/test", nil)
		sp, _, _ := StartRequestSpan(r)
		FinishRequestSpan(sp, 0, nil)
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		assert.Equal(t, "0", spans[0].Tag(ext.HTTPCode))
		assert.Equal(t, fmt.Sprintf("0: %s", http.StatusText(0)), spans[0].Tag(ext.ErrorMsg))
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
	s, _, _ := StartRequestSpan(r, HeaderTagsFromRequest(r, ht))
	s.Finish()
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)

	for expectedTag, expectedTagVal := range expectedHeaderTags {
		assert.Equal(t, expectedTagVal, spans[0].Tags()[expectedTag])
	}
}

func TestStartRequestSpanSecurityTestingHeaders(t *testing.T) {
	for _, tc := range []struct {
		name     string
		headers  http.Header
		expected map[string]string
	}{
		{
			name: "present",
			headers: http.Header{
				"x-datadog-endpoint-scan": {"scan-uuid"},
				"X-Datadog-Security-Test": {" test-uuid "},
			},
			expected: map[string]string{
				"http.request.headers.x-datadog-endpoint-scan": "scan-uuid",
				"http.request.headers.x-datadog-security-test": "test-uuid",
			},
		},
		{
			name: "empty-values",
			headers: http.Header{
				"X-Datadog-Endpoint-Scan": {""},
				"X-Datadog-Security-Test": {""},
			},
			expected: map[string]string{
				"http.request.headers.x-datadog-endpoint-scan": "",
				"http.request.headers.x-datadog-security-test": "",
			},
		},
		{
			name:     "missing",
			headers:  http.Header{},
			expected: map[string]string{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()

			r := httptest.NewRequest(http.MethodGet, "/test", nil)
			maps.Copy(r.Header, tc.headers)
			span, _, _ := StartRequestSpan(r)
			span.Finish()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			tags := spans[0].Tags()

			for _, h := range securityTestingHeaders {
				value, ok := tc.expected[h.tag]
				if !ok {
					assert.NotContains(t, tags, h.tag)
					continue
				}
				assert.Contains(t, tags, h.tag)
				assert.Equal(t, value, tags[h.tag])
			}
		})
	}
}

func TestStartRequestSpanSecurityTestingHeadersNotPropagated(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	r := httptest.NewRequest(http.MethodGet, "/test", nil)
	r.Header.Set("X-Datadog-Endpoint-Scan", "scan-uuid")
	r.Header.Set("X-Datadog-Security-Test", "test-uuid")
	span, _, _ := StartRequestSpan(r)
	outgoing := http.Header{}
	require.NoError(t, tracer.Inject(span.Context(), tracer.HTTPHeadersCarrier(outgoing)))
	span.Finish()

	for _, h := range securityTestingHeaders {
		_, ok := outgoing[http.CanonicalHeaderKey(h.header)]
		assert.False(t, ok)
	}
}

func TestBeforeHandleSecurityTestingHeadersWithAppSec(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skipf("cgo disabled / no appsec tag")
	}

	oldCfg := cfg
	defer func() { cfg = oldCfg }()

	mt := mocktracer.Start()
	defer mt.Stop()

	t.Setenv("DD_APPSEC_ENABLED", "true")
	appsec.Start()
	defer appsec.Stop()
	ResetCfg()

	r := httptest.NewRequest(http.MethodGet, "https://example.com/test", nil)
	r.Header.Set("X-Datadog-Endpoint-Scan", "scan-uuid")
	r.Header.Set("X-Datadog-Security-Test", " test-uuid ")
	w := httptest.NewRecorder()

	rw, rt, after, handled := BeforeHandle(&ServeConfig{Route: "/test"}, w, r)
	assert.False(t, handled)
	http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }).ServeHTTP(rw, rt)
	after()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "scan-uuid", spans[0].Tag(securityTestingHeaders[0].tag))
	assert.Equal(t, "test-uuid", spans[0].Tag(securityTestingHeaders[1].tag))
}

func TestStartRequestSpan(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()
	r := httptest.NewRequest(http.MethodGet, "/somePath", nil)
	s, _, _ := StartRequestSpan(r)
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
			s, _, _ := StartRequestSpan(r)
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
			url := URLFromRequest(&r, true)
			require.Equal(t, tc.expectedURL, url)
		})
	}
}

func TestURLTagWithAllowlist(t *testing.T) {
	oldCfg := cfg
	defer func() { cfg = oldCfg }()

	type testCase struct {
		name        string
		allowlist   []string
		query       string
		expectedURL string
	}
	for _, tc := range []testCase{
		{
			name:        "keep single param",
			allowlist:   []string{"p1"},
			query:       "p1=a&p2=b&p3=c",
			expectedURL: "http://example.com?p1=a",
		},
		{
			name:        "keep multiple params",
			allowlist:   []string{"p1", "p3"},
			query:       "p1=a&p2=b&p3=c&p4=d",
			expectedURL: "http://example.com?p1=a&p3=c",
		},
		{
			name:        "no matching params",
			allowlist:   []string{"x"},
			query:       "p1=a&p2=b",
			expectedURL: "http://example.com",
		},
		{
			name:        "all params match",
			allowlist:   []string{"p1", "p2"},
			query:       "p1=a&p2=b",
			expectedURL: "http://example.com?p1=a&p2=b",
		},
		{
			name:        "empty query string",
			allowlist:   []string{"p1"},
			query:       "",
			expectedURL: "http://example.com",
		},
		{
			name:        "preserves url-encoded values",
			allowlist:   []string{"q"},
			query:       "q=hello%20world&secret=abc",
			expectedURL: "http://example.com?q=hello%20world",
		},
		{
			name:        "param with no value",
			allowlist:   []string{"flag"},
			query:       "flag&other=1",
			expectedURL: "http://example.com?flag",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allowlist := make(map[string]struct{})
			for _, k := range tc.allowlist {
				allowlist[k] = struct{}{}
			}
			cfg = oldCfg
			cfg.serverQueryStringAllowlist = allowlist
			cfg.queryString = true

			r := http.Request{
				URL:  &url.URL{RawQuery: tc.query},
				Host: "example.com",
			}
			got := URLFromRequest(&r, true)
			require.Equal(t, tc.expectedURL, got)
		})
	}
}

func TestURLTagWithClientServerAllowlist(t *testing.T) {
	makeRequest := func(query string) *http.Request {
		return &http.Request{
			URL:  &url.URL{RawQuery: query},
			Host: "example.com",
		}
	}
	toMap := func(keys []string) map[string]struct{} {
		m := make(map[string]struct{}, len(keys))
		for _, k := range keys {
			m[k] = struct{}{}
		}
		return m
	}

	t.Run("different client and server allowlists", func(t *testing.T) {
		oldCfg := cfg
		defer func() { cfg = oldCfg }()
		cfg.queryString = true
		cfg.clientQueryStringAllowlist = toMap([]string{"ckey"})
		cfg.serverQueryStringAllowlist = toMap([]string{"skey"})

		r := makeRequest("ckey=1&skey=2&other=3")
		require.Equal(t, "http://example.com?ckey=1", URLFromClientRequest(r, true))
		require.Equal(t, "http://example.com?skey=2", URLFromRequest(r, true))
	})

	t.Run("client allowlist only", func(t *testing.T) {
		oldCfg := cfg
		defer func() { cfg = oldCfg }()
		cfg.queryString = true
		cfg.clientQueryStringAllowlist = toMap([]string{"ckey"})
		cfg.serverQueryStringAllowlist = nil

		r := makeRequest("ckey=1&password=secret")
		require.Equal(t, "http://example.com?ckey=1", URLFromClientRequest(r, true))
		// Server side has no allowlist, falls back to regex obfuscation.
		got := URLFromRequest(r, true)
		require.Contains(t, got, "ckey=1")
		require.NotContains(t, got, "secret")
	})

	t.Run("server allowlist only", func(t *testing.T) {
		oldCfg := cfg
		defer func() { cfg = oldCfg }()
		cfg.queryString = true
		cfg.clientQueryStringAllowlist = nil
		cfg.serverQueryStringAllowlist = toMap([]string{"skey"})

		r := makeRequest("skey=1&password=secret")
		require.Equal(t, "http://example.com?skey=1", URLFromRequest(r, true))
		// Client side has no allowlist, falls back to regex obfuscation.
		got := URLFromClientRequest(r, true)
		require.Contains(t, got, "skey=1")
		require.NotContains(t, got, "secret")
	})

	t.Run("no allowlists falls back to regex obfuscation", func(t *testing.T) {
		oldCfg := cfg
		defer func() { cfg = oldCfg }()
		cfg.queryString = true
		cfg.clientQueryStringAllowlist = nil
		cfg.serverQueryStringAllowlist = nil

		r := makeRequest("safe=1&password=secret")
		got := URLFromClientRequest(r, true)
		require.Contains(t, got, "safe=1")
		require.Contains(t, got, "<redacted>")
		require.NotContains(t, got, "secret")
	})

	t.Run("env var parsing", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST", "global_key")
		t.Setenv("DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_CLIENT", "client_key")
		t.Setenv("DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST_SERVER", "server_key")

		oldCfg := cfg
		defer func() { cfg = oldCfg }()
		cfg = newConfig()

		r := makeRequest("global_key=1&client_key=2&server_key=3")
		require.Equal(t, "http://example.com?client_key=2", URLFromClientRequest(r, true))
		require.Equal(t, "http://example.com?server_key=3", URLFromRequest(r, true))
	})

	t.Run("env var global only applies to both sides", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_URL_QUERY_STRING_ALLOWLIST", "shared")

		oldCfg := cfg
		defer func() { cfg = oldCfg }()
		cfg = newConfig()

		r := makeRequest("shared=1&other=2")
		require.Equal(t, "http://example.com?shared=1", URLFromClientRequest(r, true))
		require.Equal(t, "http://example.com?shared=1", URLFromRequest(r, true))
	})
}

func TestFilterQueryStringByAllowlist(t *testing.T) {
	allowlist := map[string]struct{}{"p1": {}, "p3": {}}

	tests := []struct {
		name     string
		raw      string
		expected string
	}{
		{"basic", "p1=a&p2=b&p3=c", "p1=a&p3=c"},
		{"empty", "", ""},
		{"no match", "x=1&y=2", ""},
		{"all match", "p1=a&p3=c", "p1=a&p3=c"},
		{"trailing ampersand", "p1=a&p2=b&", "p1=a"},
		{"no value", "p1&p2=b&p3", "p1&p3"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterQueryStringByAllowlist(tc.raw, allowlist)
			assert.Equal(t, tc.expected, got)
		})
	}
}

func TestObfuscateQueryStringDefault(t *testing.T) {
	// SSH RSA key bodies for the 100-repetition boundary.
	// Note: {100,} counts group repetitions, not bytes — %2F/%5C/%2B each count as one.
	ssh99 := strings.Repeat("a", 99)
	ssh100 := strings.Repeat("a", 100)
	ssh101 := strings.Repeat("a", 101)

	tests := []struct {
		name  string
		input string
		want  string
	}{
		// Sensitive keys: key=value and JSON-quoted forms.
		{name: "empty", input: "", want: ""},
		{name: "no_sensitive_key", input: "safe=value", want: "safe=value"},
		{name: "key_only_no_eq", input: "pass", want: "pass"},
		// Basic keyword matches.
		{name: "password", input: "password=secret", want: "<redacted>"},
		{name: "PASSWORD_case", input: "PASSWORD=secret", want: "<redacted>"},
		{name: "pwd", input: "pwd=secret", want: "<redacted>"},
		{name: "passwd", input: "passwd=secret", want: "<redacted>"},
		{name: "pass", input: "pass=secret", want: "<redacted>"},
		{name: "passphrase", input: "passphrase=secret", want: "<redacted>"},
		{name: "pass_phrase", input: "pass_phrase=secret", want: "<redacted>"},
		{name: "secret", input: "secret=x", want: "<redacted>"},
		{name: "token_eq", input: "token=abc", want: "<redacted>"},
		{name: "auth", input: "auth=x", want: "<redacted>"},
		{name: "authentication", input: "authentication=x", want: "<redacted>"},
		{name: "authorization", input: "authorization=x", want: "<redacted>"},
		{name: "api_key", input: "api_key=x", want: "<redacted>"},
		{name: "apikey", input: "apikey=x", want: "<redacted>"},
		{name: "api_key_id", input: "api_key_id=x", want: "<redacted>"},
		{name: "access_key", input: "access_key=x", want: "<redacted>"},
		{name: "private_key", input: "private_key=x", want: "<redacted>"},
		{name: "consumer_id", input: "consumer_id=x", want: "<redacted>"},
		{name: "consumer_key", input: "consumer_key=x", want: "<redacted>"},
		{name: "consumer_secret", input: "consumer_secret=x", want: "<redacted>"},
		{name: "signature", input: "signature=x", want: "<redacted>"},
		{name: "signed", input: "signed=x", want: "<redacted>"},
		// Boundary: empty value does not match ([^&]+ requires ≥1 char).
		{name: "empty_value", input: "password=", want: "password="},
		{name: "empty_value_amp", input: "password=&foo=bar", want: "password=&foo=bar"},
		// Context: sensitive key embedded among safe params.
		{name: "prefix_safe", input: "safe=1&password=secret", want: "safe=1&<redacted>"},
		{name: "suffix_safe", input: "password=secret&safe=1", want: "<redacted>&safe=1"},
		{name: "surrounded", input: "a=1&password=secret&b=2", want: "a=1&<redacted>&b=2"},
		// Multiple sensitive params: each is redacted independently.
		{name: "two_sensitive", input: "password=x&token=y", want: "<redacted>&<redacted>"},
		// URL-encoded = (%3D).
		{name: "pct3D", input: "password%3Dsecret", want: "<redacted>"},
		// %20 spaces around =.
		{name: "pct20_spaces", input: "password%20=%20value", want: "<redacted>"},
		// JSON-quoted form "key":"value": the leading '"' before the key name is
		// not consumed by the match, so it is preserved in the output.
		{name: "json_form", input: `"password":"value"`, want: `"<redacted>`},
		// Same with %22/%3A URL-encoded delimiters.
		{name: "json_form_pct", input: `%22password%22:%22value%22`, want: `%22<redacted>`},

		// Alt 2: bearer token.
		// Quirk: only ONE char is consumed after the whitespace — the regex has
		// [a-z0-9._-] (no +), so "bearer xy" leaves "y" unredacted. Replicated verbatim.
		{name: "bearer_one_char", input: "bearer x", want: "<redacted>"},
		{name: "bearer_case", input: "Bearer X", want: "<redacted>"},
		{name: "bearer_one_char_digit", input: "bearer 1", want: "<redacted>"},
		{name: "bearer_one_char_dot", input: "bearer .", want: "<redacted>"},
		{name: "bearer_one_char_dash", input: "bearer -", want: "<redacted>"},
		{name: "bearer_one_char_underscore", input: "bearer _", want: "<redacted>"},
		{name: "bearer_pct20", input: "bearer%20x", want: "<redacted>"},
		{name: "bearer_multi_space", input: "bearer  x", want: "<redacted>"},
		// Quirk: only the first char after spaces is part of the match.
		{name: "bearer_two_chars_quirk", input: "bearer xy", want: "<redacted>y"},
		{name: "bearer_three_chars_quirk", input: "bearer abc", want: "<redacted>bc"},
		// No match: nothing after space.
		{name: "bearer_no_char", input: "bearer ", want: "bearer "},
		// No match: no space before token.
		{name: "bearer_no_space", input: "bearer", want: "bearer"},
		// No match: char not in [a-z0-9._-].
		{name: "bearer_invalid_char", input: "bearer !", want: "bearer !"},

		// Short token: exactly 13 [a-z0-9] chars after "token:" or "token%3A".
		{name: "token_colon_13", input: "token:1234567890abc", want: "<redacted>"},
		{name: "token_colon_13_upper", input: "TOKEN:1234567890ABC", want: "<redacted>"},
		{name: "token_pct3A_13", input: "token%3A1234567890abc", want: "<redacted>"},
		// Boundary: 12 chars → no match.
		{name: "token_colon_12", input: "token:123456789012", want: "token:123456789012"},
		// Boundary: 14 chars → first 13 matched, trailing char unredacted.
		{name: "token_colon_14", input: "token:12345678901234", want: "<redacted>4"},
		// No match: empty after colon.
		{name: "token_colon_empty", input: "token:", want: "token:"},
		// No match: invalid chars (not in [a-z0-9]).
		{name: "token_colon_invalid_chars", input: "token:!!!!!!!!!!!!!!", want: "token:!!!!!!!!!!!!!!"},
		// No match: colon with too few chars — the sensitive-key branch is also inapplicable (no =).
		{name: "token_colon_short", input: "token:abc", want: "token:abc"},
		// Embedded in params.
		{name: "token_colon_embedded", input: "safe=1&token:1234567890abc&other=2", want: "safe=1&<redacted>&other=2"},

		// GitHub tokens: gh[opsu]_ followed by exactly 36 alphanumeric chars.
		{name: "gho_36", input: "gho_abcdefghijklmnopqrstuvwxyz0123456789", want: "<redacted>"},
		{name: "ghp_36", input: "ghp_abcdefghijklmnopqrstuvwxyz0123456789", want: "<redacted>"},
		{name: "ghs_36", input: "ghs_abcdefghijklmnopqrstuvwxyz0123456789", want: "<redacted>"},
		{name: "ghu_36", input: "ghu_abcdefghijklmnopqrstuvwxyz0123456789", want: "<redacted>"},
		{name: "gho_36_upper", input: "GHO_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789", want: "<redacted>"},
		// No match: 'a' not in [opsu].
		{name: "gha_36", input: "gha_abcdefghijklmnopqrstuvwxyz0123456789", want: "gha_abcdefghijklmnopqrstuvwxyz0123456789"},
		// Boundary: 35 chars → no match.
		{name: "gho_35", input: "gho_abcdefghijklmnopqrstuvwxyz012345678", want: "gho_abcdefghijklmnopqrstuvwxyz012345678"},
		// Boundary: 37 chars → first 36 matched, trailing char unredacted.
		{name: "gho_37", input: "gho_abcdefghijklmnopqrstuvwxyz01234567890", want: "<redacted>0"},
		// No match: non-alphanumeric chars break the 36-char run.
		{name: "gho_invalid_chars", input: "gho_abcdefghijklmnopqrstuvwxyz012345!!!!!", want: "gho_abcdefghijklmnopqrstuvwxyz012345!!!!!"},
		// Embedded in params.
		{name: "gho_embedded", input: "key=x&gho_abcdefghijklmnopqrstuvwxyz0123456789&other=y", want: "key=x&<redacted>&other=y"},

		// JWT shape: ey[I-L] + body + dot + ey[I-L] + body, optional third segment.
		// Two-segment JWT.
		{name: "jwt_2seg", input: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIn0", want: "<redacted>"},
		// Three-segment JWT (with signature).
		{name: "jwt_3seg", input: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c", want: "<redacted>"},
		// Base64 padding '=' is in the char class.
		{name: "jwt_padding", input: "eyJhbGc=.eyJzdWI=", want: "<redacted>"},
		// URL-encoded '=' (%3D) is also accepted.
		{name: "jwt_pct3D", input: "eyJhbGc%3D.eyJzdWI%3D", want: "<redacted>"},
		// Case-insensitive prefix: EY + [I-L] matches.
		{name: "jwt_upper", input: "EYJhbGciOiJIUzI1NiJ9.EYJzdWIiOiJ1c2VyMTIzIn0", want: "<redacted>"},
		// Third segment with URL-encoded chars.
		{name: "jwt_3seg_pct", input: "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyIn0.sig%2Fwith%2Bchars", want: "<redacted>"},
		// No match: only one segment (no dot + second ey[I-L]).
		{name: "jwt_one_seg", input: "eyJhbGciOiJIUzI1NiJ9", want: "eyJhbGciOiJIUzI1NiJ9"},
		// No match: second segment doesn't start with ey[I-L].
		{name: "jwt_bad_second", input: "eyJhbGciOiJIUzI1NiJ9.abc123", want: "eyJhbGciOiJIUzI1NiJ9.abc123"},
		// No match: third char not in [I-L] (M is out of range).
		{name: "jwt_bad_prefix", input: "eyMhbGciOiJIUzI1NiJ9.eyMzdWIiOiJ1c2VyIn0", want: "eyMhbGciOiJIUzI1NiJ9.eyMzdWIiOiJ1c2VyIn0"},
		// No match: first segment body is empty (dot immediately after ey[I-L]).
		{name: "jwt_empty_first_body", input: "eyJ.eyJzdWIiOiJ1c2VyIn0", want: "eyJ.eyJzdWIiOiJ1c2VyIn0"},
		// Embedded in params.
		{name: "jwt_embedded", input: "safe=1&eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIn0&other=2", want: "safe=1&<redacted>&other=2"},

		// PEM private key block.
		// Note: the pattern ends at the final KEY — trailing "-----" is NOT consumed.
		// Note: (?:\s|%20) between PRIVATE and KEY has no +, so exactly one space/encoded-space.
		{name: "pem_rsa", input: "-----BEGIN RSA PRIVATE KEY-----MIIEABCDEF-----END RSA PRIVATE KEY-----", want: "<redacted>-----"},
		{name: "pem_ec", input: "-----BEGIN EC PRIVATE KEY-----MIIEABCDEF-----END EC PRIVATE KEY-----", want: "<redacted>-----"},
		{name: "pem_no_type", input: "-----BEGIN PRIVATE KEY-----MIIEABCDEF-----END PRIVATE KEY-----", want: "<redacted>-----"},
		{name: "pem_lower", input: "-----begin rsa private key-----miieabcdef-----end rsa private key-----", want: "<redacted>-----"},
		{name: "pem_pct20", input: "-----BEGIN%20RSA%20PRIVATE%20KEY-----MIIEABCDEF-----END%20RSA%20PRIVATE%20KEY-----", want: "<redacted>-----"},
		// No match: CERTIFICATE has no PRIVATE keyword.
		{name: "pem_certificate", input: "-----BEGIN CERTIFICATE-----MIIEABCDEF-----END CERTIFICATE-----", want: "-----BEGIN CERTIFICATE-----MIIEABCDEF-----END CERTIFICATE-----"},
		// No match: double space between PRIVATE and KEY — the single (?:\s|%20) can't span two spaces.
		{name: "pem_double_space", input: "-----BEGIN RSA PRIVATE  KEY-----MIIEABCDEF-----END RSA PRIVATE  KEY-----", want: "-----BEGIN RSA PRIVATE  KEY-----MIIEABCDEF-----END RSA PRIVATE  KEY-----"},
		// No match: missing END block.
		{name: "pem_no_end", input: "-----BEGIN RSA PRIVATE KEY-----MIIEABCDEF", want: "-----BEGIN RSA PRIVATE KEY-----MIIEABCDEF"},
		// Embedded in params: trailing "-----" before "&safe=1" is preserved.
		{name: "pem_embedded", input: "key=x&-----BEGIN RSA PRIVATE KEY-----BODY-----END RSA PRIVATE KEY-----&safe=1", want: "key=x&<redacted>-----&safe=1"},

		// SSH RSA key: ssh-rsa + optional spaces + ≥100 repetitions of [a-z0-9/\.+] or %2F/%5C/%2B.
		// Quirk: bare '\' is absent from [a-z0-9\/\.+]; only %5C (URL-encoded '\') is accepted.
		{name: "ssh_100", input: "ssh-rsa " + ssh100, want: "<redacted>"},
		{name: "ssh_101", input: "ssh-rsa " + ssh101, want: "<redacted>"},
		{name: "ssh_upper", input: "SSH-RSA " + ssh100, want: "<redacted>"},
		// Zero spaces also accepted ((?:\s|%20)*).
		{name: "ssh_no_space", input: "ssh-rsa" + ssh100, want: "<redacted>"},
		{name: "ssh_pct20", input: "ssh-rsa%20" + ssh100, want: "<redacted>"},
		// / . + are valid body chars.
		{name: "ssh_with_slash", input: "ssh-rsa " + ssh99 + "/", want: "<redacted>"},
		{name: "ssh_with_dot", input: "ssh-rsa " + ssh99 + ".", want: "<redacted>"},
		{name: "ssh_with_plus", input: "ssh-rsa " + ssh99 + "+", want: "<redacted>"},
		// %2F/%5C/%2B each count as one repetition toward the 100 minimum.
		{name: "ssh_pct2F_counts_one", input: "ssh-rsa " + ssh99 + "%2F", want: "<redacted>"},
		{name: "ssh_pct5C_counts_one", input: "ssh-rsa " + ssh99 + "%5C", want: "<redacted>"},
		{name: "ssh_pct2B_counts_one", input: "ssh-rsa " + ssh99 + "%2B", want: "<redacted>"},
		// Boundary: 99 repetitions → no match.
		{name: "ssh_99", input: "ssh-rsa " + ssh99, want: "ssh-rsa " + ssh99},
		// Quirk: bare '\' is not in the char class → the run stops before it, preventing 100 repetitions.
		{name: "ssh_bare_backslash", input: "ssh-rsa " + ssh99 + "\\", want: "ssh-rsa " + ssh99 + "\\"},
		// No match: wrong prefix.
		{name: "ssh_wrong_prefix", input: "ssh-dsa " + ssh100, want: "ssh-dsa " + ssh100},
		// Embedded in params.
		{name: "ssh_embedded", input: "key=x&ssh-rsa " + ssh100 + "&safe=1", want: "key=x&<redacted>&safe=1"},

		// Cross-alternative interactions.
		// Multiple sensitive keywords: each sensitive param redacted independently.
		{name: "mix_multi_sensitive_keys", input: "user=john&password=secret&api_key=mykey&safe=1", want: "user=john&<redacted>&<redacted>&safe=1"},
		// Sensitive key + bearer token.
		{name: "mix_sensitive_key_bearer", input: "safe=1&password=secret&bearer x", want: "safe=1&<redacted>&<redacted>"},
		// Sensitive key sub-string match: "token" inside "access_token" is matched (no word-boundary anchoring).
		{name: "mix_sensitive_key_substring", input: "access_token=xxx", want: "access_<redacted>"},
		// Sensitive key + JWT: safe param followed by a standalone JWT.
		{name: "mix_sensitive_key_jwt", input: "callback=ok&eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIn0&other=1", want: "callback=ok&<redacted>&other=1"},
		// Sensitive key + GitHub token.
		{name: "mix_sensitive_key_github", input: "password=x&gho_abcdefghijklmnopqrstuvwxyz0123456789", want: "<redacted>&<redacted>"},
		// All 7 alternatives in one string.
		// The PEM private key branch contributes "<redacted>-----" because the pattern ends at KEY (no trailing -----).
		{
			name: "mix_all_alts",
			input: "password=secret" +
				"&bearer x" +
				"&token:1234567890abc" +
				"&gho_abcdefghijklmnopqrstuvwxyz0123456789" +
				"&eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIn0" +
				"&-----BEGIN RSA PRIVATE KEY-----BODY-----END RSA PRIVATE KEY-----" +
				"&ssh-rsa " + ssh100,
			want: "<redacted>&<redacted>&<redacted>&<redacted>&<redacted>&<redacted>-----&<redacted>",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := obfuscateQueryStringDefault(tc.input)
			assert.Equal(t, tc.want, got)
			oracle := defaultQueryStringRegexp.ReplaceAllLiteralString(tc.input, "<redacted>")
			assert.Equal(t, oracle, got, "diverges from regex oracle")
		})
	}
}

func FuzzDefaultObfuscator(f *testing.F) {
	seeds := []string{
		"",
		"safe=value",
		"password=secret",
		"safe=1&password=secret&token=abc",
		"bearer x",
		"bearer xy",
		"token:1234567890abc",
		"gho_abcdefghijklmnopqrstuvwxyz0123456789",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyMTIzIn0",
		"-----BEGIN RSA PRIVATE KEY-----MIIEABCDEF-----END RSA PRIVATE KEY-----",
		"ssh-rsa " + strings.Repeat("a", 100),
		`"password":"value"`,
		"password%3Dsecret",
		"password=&foo=bar",
		"-----BEGIN " + strings.Repeat("a", 200) + "-----",
		"ssh-rsa " + strings.Repeat("a", 99) + "X",
		"passwor",
		"passwordX",
		"tokens=",
		"authz=",
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		want := defaultQueryStringRegexp.ReplaceAllLiteralString(s, "<redacted>")
		got := obfuscateQueryStringDefault(s)
		if got != want {
			t.Errorf("obfuscateQueryStringDefault(%q) = %q; want %q", s, got, want)
		}
	})
}

func BenchmarkURLFromRequest(b *testing.B) {
	oldCfg := cfg
	defer func() { cfg = oldCfg }()
	customQueryStringRegexp := regexp.MustCompile("password=[^&]+")

	queries := []struct {
		name string
		raw  string
	}{
		{"few_params", "user=john&password=secret&token=abc123"},
		{"many_params", "user=john&password=secret&token=abc123&session=xyz&debug=true&page=1&sort=asc&filter=active&lang=en&ref=homepage"},
		{"really_long_1", "sz=300x50&iu=/12345678901/ad_unit_test&output=&tile=1&ss_req=1&d_imp=1&d_imp_hdr=1&t=%26devmake%3Dacme%26devmakedate%3D1700000000000%26uxloc%3DBANNER_TOP%26appname%3DTestApp%26devid%3Daaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee%26appver%3D10.200.0%26devmodel%3DAcme-default%26gppsid%3D%26usid%3Daaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-20260101120000-12345678901234567%26screen%3DLANDING%26contenttype%3DDISPLAY%26contentgenre%3D%26contentrating%3D%26dlid%3D11111111-2222-3333-4444-555555555555%26mvpd%3DTestApp%26tile%3D1%26carouselPosition%3D1%26gpp%3D%26carouselName%3DFeatured%2BBanner%2BSlot%2BUS%26devbrand%3DAcme%26partnername%3DTestApp%26devlang%3Den%26devlat%3D0%26apptype%3DEntertainment%26contentlang%3Den%26lowEnd%3Dtrue%26devcountry%3DUSA%26devtype%3Ddpid%26resellerId%3Dtestreseller%26platform%3DTVOS&ppid=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee&rdid=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee&is_lat=0&idtype=dpid&ip=198.51.100.1&c=1234567890123456789&gdpr=1&gdpr_consent=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.BBBBBBBBBBBBB"},
		{"really_long_2", "sz=300x50&iu=/12345678901/ad_unit_test&output=&tile=1&ss_req=1&d_imp=1&d_imp_hdr=1&t=%26gpp%3D%26mvpd%3DTestApp%26usid%3Daaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-20260101120000-12345678901234567%26platform%3DTVOS%26uxloc%3DBANNER_TOP%26tile%3D1%26devcountry%3DUSA%26devbrand%3DAcme%26devtype%3Ddpid%26appname%3DTestApp%26carouselPosition%3D1%26screen%3DLANDING%26devid%3Daaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee%26contenttype%3DDISPLAY%26contentgenre%3D%26contentrating%3D%26appver%3D10.200.0%26carouselName%3DFeatured%2BBanner%2BSlot%2BUS%26partnername%3DTestApp%26devmake%3Dacme%26devmakedate%3D1700000000000%26devlang%3Den%26contentlang%3Den%26devlat%3D0%26devmodel%3DAcme-default%26lowEnd%3Dtrue%26dlid%3D11111111-2222-3333-4444-555555555555%26apptype%3DEntertainment%26gppsid%3D&ppid=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee&rdid=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee&is_lat=0&idtype=dpid&ip=198.51.100.1&c=9876543210987654321&gdpr=1&gdpr_consent=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.BBBBBBBBBBBBB"},
	}

	setCfg := func(re *regexp.Regexp, useDefault bool, allow map[string]struct{}) {
		cfg = oldCfg
		cfg.queryString = true
		cfg.queryStringRegexp = re
		cfg.useDefaultObfuscator = useDefault
		cfg.serverQueryStringAllowlist = allow
	}
	allowlist := map[string]struct{}{
		"user": {}, "page": {}, "sort": {},
		"sz": {}, "tile": {}, "gdpr": {}, "ip": {}, "idtype": {},
	}

	variants := []struct {
		name  string
		setup func()
	}{
		{"default_regex", func() { setCfg(defaultQueryStringRegexp, false, nil) }},
		{"default_fast", func() { setCfg(nil, true, nil) }},
		{"custom_regex", func() { setCfg(customQueryStringRegexp, false, nil) }},
		{"allowlist", func() { setCfg(nil, false, allowlist) }},
	}

	for _, q := range queries {
		r := &http.Request{
			URL:  &url.URL{RawQuery: q.raw},
			Host: "example.com",
		}
		for _, v := range variants {
			b.Run(v.name+"/"+q.name, func(b *testing.B) {
				v.setup()
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					URLFromRequest(r, true)
				}
			})
		}
	}
}

func BenchmarkObfuscateQueryStringDefault(b *testing.B) {
	noMatchLong := strings.Repeat("xx=yy&zz=ww&", 350) // ~4.2 KiB, no keyword prefixes
	sensitiveDense := strings.Repeat("password=x&apikey=y&token=z&", 50)
	pemAdversarial := "-----BEGIN " + strings.Repeat("a", 4096)  // matcher walks deep, no closing "-----"
	sshAdversarial := "ssh-rsa " + strings.Repeat("a", 99) + "X" // 'X' is alphaNum → count=100 → matches (min-length boundary)
	sshFail := "ssh-rsa " + strings.Repeat("a", 99) + "!"        // '!' not in body charset → count=99 → fails after full scan
	sshMatch := "ssh-rsa " + strings.Repeat("a", 200)
	mixedLong1 := "sz=300x50&iu=/12345678901/ad_unit_test&output=&tile=1&ss_req=1&d_imp=1&d_imp_hdr=1&t=%26devmake%3Dacme%26devmakedate%3D1700000000000%26uxloc%3DBANNER_TOP%26appname%3DTestApp%26devid%3Daaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee%26appver%3D10.200.0%26devmodel%3DAcme-default%26gppsid%3D%26usid%3Daaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-20260101120000-12345678901234567%26screen%3DLANDING%26contenttype%3DDISPLAY%26contentgenre%3D%26contentrating%3D%26dlid%3D11111111-2222-3333-4444-555555555555%26mvpd%3DTestApp%26tile%3D1%26carouselPosition%3D1%26gpp%3D%26carouselName%3DFeatured%2BBanner%2BSlot%2BUS%26devbrand%3DAcme%26partnername%3DTestApp%26devlang%3Den%26devlat%3D0%26apptype%3DEntertainment%26contentlang%3Den%26lowEnd%3Dtrue%26devcountry%3DUSA%26devtype%3Ddpid%26resellerId%3Dtestreseller%26platform%3DTVOS&ppid=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee&rdid=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee&is_lat=0&idtype=dpid&ip=198.51.100.1&c=1234567890123456789&gdpr=1&gdpr_consent=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.BBBBBBBBBBBBB"
	mixedLong2 := "sz=300x50&iu=/12345678901/ad_unit_test&output=&tile=1&ss_req=1&d_imp=1&d_imp_hdr=1&t=%26gpp%3D%26mvpd%3DTestApp%26usid%3Daaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-20260101120000-12345678901234567%26platform%3DTVOS%26uxloc%3DBANNER_TOP%26tile%3D1%26devcountry%3DUSA%26devbrand%3DAcme%26devtype%3Ddpid%26appname%3DTestApp%26carouselPosition%3D1%26screen%3DLANDING%26devid%3Daaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee%26contenttype%3DDISPLAY%26contentgenre%3D%26contentrating%3D%26appver%3D10.200.0%26carouselName%3DFeatured%2BBanner%2BSlot%2BUS%26partnername%3DTestApp%26devmake%3Dacme%26devmakedate%3D1700000000000%26devlang%3Den%26contentlang%3Den%26devlat%3D0%26devmodel%3DAcme-default%26lowEnd%3Dtrue%26dlid%3D11111111-2222-3333-4444-555555555555%26apptype%3DEntertainment%26gppsid%3D&ppid=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee&rdid=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee&is_lat=0&idtype=dpid&ip=198.51.100.1&c=9876543210987654321&gdpr=1&gdpr_consent=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.BBBBBBBBBBBBB"

	cases := []struct {
		name  string
		input string
	}{
		{"no_match/short", "foo=1&bar=2&baz=3"},
		{"no_match/long", noMatchLong},
		{"sensitive_key/dense", sensitiveDense},
		{"bearer/match", "bearer " + strings.Repeat("abcdefghij", 4)},
		{"jwt/match", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJ1c2VyMTIzIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"},
		{"pem/adversarial", pemAdversarial},
		{"ssh_rsa/near_miss", sshAdversarial}, // body is alphaNum → matches; validates min-length boundary
		{"ssh_rsa/fail", sshFail},
		{"ssh_rsa/match", sshMatch},
		{"mixed_traffic/really_long_1", mixedLong1},
		{"mixed_traffic/really_long_2", mixedLong2},
	}

	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				obfuscateQueryStringDefault(tc.input)
			}
		})
	}
}

// TestObfuscateAdversarialCompletes is a timing regression guard: it asserts that
// the worst-case JWT adversarial input (300 KB of repeated "eyJ") completes well
// within 1 s.  Before the jwtSkipEnd fix this took ~5 s on typical hardware; the
// fixed implementation runs in < 1 ms, so 1 s gives a ~1000× safety margin.
func TestObfuscateAdversarialCompletes(t *testing.T) {
	input := strings.Repeat("eyJ", 100000) // 300 KB
	start := time.Now()
	obfuscateQueryStringDefault(input)
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("obfuscateQueryStringDefault took %v for 300 KB adversarial input (limit 1s) — possible O(N²) regression", elapsed)
	}
}

// BenchmarkObfuscateAdversarial demonstrates an O(N²) scaling previously
// present.
//
// Pre-fix: 4× input length ≈ 16× time.  Post-fix: 4× input ≈ 4× time.
func BenchmarkObfuscateAdversarial(b *testing.B) {
	cases := []struct {
		name  string
		input string
	}{
		// JWT: "eyJ" repeated — re-anchors matchJWT every 3 bytes, each
		// consumeJWTSegment call scans the entire remaining string (e, y, J are
		// all in classJWTSeg = [\w=-]).
		{"jwt_adversarial/9k", strings.Repeat("eyJ", 3000)},
		{"jwt_adversarial/30k", strings.Repeat("eyJ", 10000)},
		{"jwt_adversarial/100k", strings.Repeat("eyJ", 33333)},
		// PEM: repeated "PRIVATE KEY" in the label — matchPEMPrivateKeyLiteral
		// succeeds at every 12-byte boundary and matchPEMBodyAndEnd scans the
		// rest of the string for each hit.
		{"pem_adversarial/4k", "-----BEGIN " + strings.Repeat("PRIVATE KEY ", 170)},
		{"pem_adversarial/12k", "-----BEGIN " + strings.Repeat("PRIVATE KEY ", 500)},
		{"pem_adversarial/40k", "-----BEGIN " + strings.Repeat("PRIVATE KEY ", 1666)},
	}
	for _, tc := range cases {
		b.Run(tc.name, func(b *testing.B) {
			b.SetBytes(int64(len(tc.input)))
			for b.Loop() {
				obfuscateQueryStringDefault(tc.input)
			}
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
	log.UseLogger(log.DiscardLogger{})
	opts := []tracer.StartSpanOption{
		tracer.ServiceName("SomeService"),
		tracer.ResourceName("SomeResource"),
		tracer.Tag(ext.HTTPRoute, "/some/route/?"),
	}
	b.ResetTimer()
	for b.Loop() {
		StartRequestSpan(r, opts...)
	}
}

func BenchmarkStartRequestSpanQueryObfuscation(b *testing.B) {
	log.UseLogger(log.DiscardLogger{})
	opts := []tracer.StartSpanOption{
		tracer.ServiceName("SomeService"),
		tracer.ResourceName("SomeResource"),
		tracer.Tag(ext.HTTPRoute, "/some/route/?"),
	}
	queries := []struct {
		name string
		raw  string
	}{
		{"few_params", "user=john&password=secret&token=abc123"},
		{"many_params", "user=john&password=secret&token=abc123&session=xyz&debug=true&page=1&sort=asc&filter=active&lang=en&ref=homepage"},
		{"really_long_1", "sz=300x50&iu=/12345678901/ad_unit_test&output=&tile=1&ss_req=1&d_imp=1&d_imp_hdr=1&t=%26devmake%3Dacme%26devmakedate%3D1700000000000%26uxloc%3DBANNER_TOP%26appname%3DTestApp%26devid%3Daaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee%26appver%3D10.200.0%26devmodel%3DAcme-default%26gppsid%3D%26usid%3Daaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-20260101120000-12345678901234567%26screen%3DLANDING%26contenttype%3DDISPLAY%26contentgenre%3D%26contentrating%3D%26dlid%3D11111111-2222-3333-4444-555555555555%26mvpd%3DTestApp%26tile%3D1%26carouselPosition%3D1%26gpp%3D%26carouselName%3DFeatured%2BBanner%2BSlot%2BUS%26devbrand%3DAcme%26partnername%3DTestApp%26devlang%3Den%26devlat%3D0%26apptype%3DEntertainment%26contentlang%3Den%26lowEnd%3Dtrue%26devcountry%3DUSA%26devtype%3Ddpid%26resellerId%3Dtestreseller%26platform%3DTVOS&ppid=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee&rdid=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee&is_lat=0&idtype=dpid&ip=198.51.100.1&c=1234567890123456789&gdpr=1&gdpr_consent=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.BBBBBBBBBBBBB"},
		{"really_long_2", "sz=300x50&iu=/12345678901/ad_unit_test&output=&tile=1&ss_req=1&d_imp=1&d_imp_hdr=1&t=%26gpp%3D%26mvpd%3DTestApp%26usid%3Daaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee-20260101120000-12345678901234567%26platform%3DTVOS%26uxloc%3DBANNER_TOP%26tile%3D1%26devcountry%3DUSA%26devbrand%3DAcme%26devtype%3Ddpid%26appname%3DTestApp%26carouselPosition%3D1%26screen%3DLANDING%26devid%3Daaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee%26contenttype%3DDISPLAY%26contentgenre%3D%26contentrating%3D%26appver%3D10.200.0%26carouselName%3DFeatured%2BBanner%2BSlot%2BUS%26partnername%3DTestApp%26devmake%3Dacme%26devmakedate%3D1700000000000%26devlang%3Den%26contentlang%3Den%26devlat%3D0%26devmodel%3DAcme-default%26lowEnd%3Dtrue%26dlid%3D11111111-2222-3333-4444-555555555555%26apptype%3DEntertainment%26gppsid%3D&ppid=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee&rdid=aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee&is_lat=0&idtype=dpid&ip=198.51.100.1&c=9876543210987654321&gdpr=1&gdpr_consent=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA.BBBBBBBBBBBBB"},
	}
	for _, q := range queries {
		r, err := http.NewRequest("GET", "http://example.com", nil)
		if err != nil {
			b.Fatalf("Failed to create request: %v", err)
		}
		r.URL.RawQuery = q.raw
		b.Run(q.name, func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				StartRequestSpan(r, opts...)
			}
		})
	}
}

func TestStartRequestSpanWithBaggage(t *testing.T) {
	t.Setenv("DD_TRACE_PROPAGATION_STYLE", "datadog,tracecontext,baggage")
	tracer.Start()
	defer tracer.Stop()

	r := httptest.NewRequest(http.MethodGet, "/somePath", nil)
	r.Header.Set("baggage", "key1=value1,key2=value2")
	s, ctx, _ := StartRequestSpan(r)
	s.Finish()
	// TODO: This behavior is not ideal. We want baggage headers accessible with r.Context (baggage.All(r.Context())) -- not the generated span's context.
	spanBm := make(map[string]string)
	s.Context().ForeachBaggageItem(func(k, v string) bool {
		spanBm[k] = v
		return true
	})
	assert.Equal(t, "value1", spanBm["key1"])
	assert.Equal(t, "value2", spanBm["key2"])
	baggageMap := baggage.All(ctx)
	assert.Equal(t, "value1", baggageMap["key1"], "should propagate baggage from header to context")
	assert.Equal(t, "value2", baggageMap["key2"], "should propagate baggage from header to context")
}

func TestBeforeHandleHTTPEndpoint(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	type tc struct {
		name   string
		trn    string // DD_TRACE_RESOURCE_RENAMING_ENABLED
		always string // DD_TRACE_RESOURCE_RENAMING_ALWAYS_SIMPLIFIED_ENDPOINT
		route  string
		expect func(*testing.T, *mocktracer.Span, *http.Request)
	}

	route := "/api/v1/users/{id}"
	cases := []tc{
		{
			name:   "no route, TRN=false, ALWAYS=false",
			trn:    "false",
			always: "false",
			route:  "",
			expect: func(t *testing.T, s *mocktracer.Span, r *http.Request) {
				assert.Equal(t, nil, s.Tag(ext.HTTPEndpoint))
			},
		},
		{
			name:   "no route, TRN=true, ALWAYS=false",
			trn:    "true",
			always: "false",
			route:  "",
			expect: func(t *testing.T, s *mocktracer.Span, r *http.Request) {
				expected := simplifyHTTPUrl(URLFromRequest(r, true))
				assert.Equal(t, expected, s.Tag(ext.HTTPEndpoint))
			},
		},
		{
			name:   "route present, TRN=true, ALWAYS=false",
			trn:    "true",
			always: "false",
			route:  route,
			expect: func(t *testing.T, s *mocktracer.Span, r *http.Request) {
				assert.Equal(t, route, s.Tag(ext.HTTPEndpoint))
			},
		},
		{
			name:   "route present, TRN=true, ALWAYS=true",
			trn:    "true",
			always: "true",
			route:  "/a/b/{id}",
			expect: func(t *testing.T, s *mocktracer.Span, r *http.Request) {
				expected := simplifyHTTPUrl(URLFromRequest(r, true))
				assert.Equal(t, expected, s.Tag(ext.HTTPEndpoint))
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("DD_TRACE_RESOURCE_RENAMING_ENABLED", c.trn)
			t.Setenv("DD_TRACE_RESOURCE_RENAMING_ALWAYS_SIMPLIFIED_ENDPOINT", c.always)
			ResetCfg()

			r := httptest.NewRequest(http.MethodGet, "https://example.com/api/v1/users/123?foo=bar", nil)
			w := httptest.NewRecorder()
			cfg := &ServeConfig{Route: c.route}

			rw, rt, after, handled := BeforeHandle(cfg, w, r)
			assert.False(t, handled)
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }).ServeHTTP(rw, rt)
			after()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			c.expect(t, spans[0], r)
			mt.Reset()
		})
	}
}

func TestResourceRenamingActivation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skipf("cgo disabled / no appsec tag")
	}

	type tc struct {
		name        string
		trn         *string // nil => unset
		appsecStart bool
		expectSet   bool
	}

	makeReq := func() (*http.Request, *httptest.ResponseRecorder, *ServeConfig) {
		r := httptest.NewRequest(http.MethodGet, "https://example.com/a/b/123", nil)
		w := httptest.NewRecorder()
		cfg := &ServeConfig{Route: "/a/b/{id}"}
		return r, w, cfg
	}

	trueStr := "true"
	falseStr := "false"

	cases := []tc{
		{name: "TRN true -> enabled", trn: &trueStr, appsecStart: false, expectSet: true},
		{name: "TRN false -> disabled", trn: &falseStr, appsecStart: false, expectSet: false},
		{name: "APPSEC true, TRN unset -> enabled", trn: nil, appsecStart: true, expectSet: true},
		{name: "APPSEC false, TRN unset -> disabled", trn: nil, appsecStart: false, expectSet: false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			os.Unsetenv("DD_TRACE_RESOURCE_RENAMING_ENABLED")
			os.Unsetenv("DD_TRACE_RESOURCE_RENAMING_ALWAYS_SIMPLIFIED_ENDPOINT")
			os.Unsetenv("DD_APPSEC_ENABLED")

			mt := mocktracer.Start()
			defer mt.Stop()

			if c.trn != nil {
				t.Setenv("DD_TRACE_RESOURCE_RENAMING_ENABLED", *c.trn)
			}

			if c.appsecStart {
				os.Setenv("DD_APPSEC_ENABLED", "true")
				appsec.Start()
			}
			defer appsec.Stop()

			ResetCfg()

			r, w, cfg := makeReq()
			rw, rt, after, handled := BeforeHandle(cfg, w, r)
			assert.False(t, handled)
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }).ServeHTTP(rw, rt)
			after()

			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			if c.expectSet {
				assert.NotEmpty(t, spans[0].Tag(ext.HTTPEndpoint))
			} else {
				assert.Empty(t, spans[0].Tag(ext.HTTPEndpoint))
			}
		})
	}
}

// Ensure the resource renaming is enabled only if appsec was enabled at the startup with the env var.
func TestResourceRenamingActivationAppSecNotStartup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skipf("cgo disabled / no appsec tag")
	}

	mt := mocktracer.Start()
	defer mt.Stop()

	r := httptest.NewRequest(http.MethodGet, "https://example.com/a/b/123", nil)
	w := httptest.NewRecorder()
	cfg := &ServeConfig{Route: "/a/b/{id}"}

	rw, rt, after, handled := BeforeHandle(cfg, w, r)
	assert.False(t, handled)
	http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }).ServeHTTP(rw, rt)
	after()

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Empty(t, spans[0].Tag(ext.HTTPEndpoint))

	t.Setenv("DD_APPSEC_ENABLED", "true") // Force activation
	appsec.Start()
	defer appsec.Stop()

	rw, rt, after, handled = BeforeHandle(cfg, w, r)
	assert.False(t, handled)
	http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }).ServeHTTP(rw, rt)
	after()

	spans = mt.FinishedSpans()
	require.Len(t, spans, 2)
	assert.Empty(t, spans[1].Tag(ext.HTTPEndpoint))
}

func TestRenamedRouteSelection(t *testing.T) {
	type tc struct {
		name     string
		route    string
		endpoint string
		url      string // escaped path
		expect   string
	}

	cases := []tc{
		{
			name:   "route used when available",
			route:  "/users/{id}",
			url:    "/users/123",
			expect: "/users/{id}",
		},
		{
			name:     "endpoint used when route empty",
			endpoint: "/users/{id}",
			url:      "/users/123",
			expect:   "/users/{id}",
		},
		{
			name:   "fallback to simplified url when both empty",
			url:    "/a/b/123456",
			expect: simplifyHTTPUrl("/a/b/123456"),
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := renamedRoute(c.route, c.endpoint, c.url)
			assert.Equal(t, c.expect, got)
		})
	}
}

func TestStartRequestSpanMergedBaggage(t *testing.T) {
	t.Setenv("DD_TRACE_PROPAGATION_STYLE", "datadog,tracecontext,baggage")
	tracer.Start()
	defer tracer.Stop()

	// Create a base context with pre-set baggage.
	baseCtx := baggage.Set(context.Background(), "pre_key", "pre_value")

	// Create an HTTP request with that context.
	req := httptest.NewRequest(http.MethodGet, "/somePath", nil).WithContext(baseCtx)

	// Set the baggage header with additional baggage items.
	req.Header.Set("baggage", "header_key=header_value,another_header=another_value")

	// Start the request span, which will extract header baggage and merge it with the context's baggage.
	span, ctx, _ := StartRequestSpan(req)
	span.Finish()

	// Retrieve the merged baggage from the span's context.
	mergedBaggage := baggage.All(ctx)

	// Verify that both pre-set and header baggage items are present.
	assert.Equal(t, "pre_value", mergedBaggage["pre_key"], "should contain pre-set baggage")
	assert.Equal(t, "header_value", mergedBaggage["header_key"], "should contain header baggage")
	assert.Equal(t, "another_value", mergedBaggage["another_header"], "should contain header baggage")
}

func TestBaggageSpanTagsOpentracer(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()
	ctx := context.Background()

	// Create an HTTP request with no additional baggage context
	req := httptest.NewRequest(http.MethodGet, "/somePath", nil).WithContext(ctx)
	req.Header.Set("x-datadog-trace-id", "1")
	req.Header.Set("x-datadog-parent-id", "2")
	req.Header.Set("baggage", "session.id=789")  // w3c baggage header
	req.Header.Set("ot-baggage-user.id", "1234") // opentracer baggage header

	// Start the request span, which will extract baggage and add it as span tags
	reqSpan, _, _ := StartRequestSpan(req)
	m := reqSpan.AsMap()

	// Keys that SHOULD be present:
	assert.Contains(t, m, "baggage.session.id", "baggage.session.id should be included in span tags")
	assert.Equal(t, "789", m["baggage.session.id"], "should contain session.id value")

	// Keys that should NOT be present (user.id is ot-baggage header)
	// This assertion WILL FAIL until baggage revamp is complete; therefore, commented out
	// Baggage revamp Jira card: APMAPI-1442
	// assert.NotContains(t, m, "baggage.user.id", "baggage.user.id should not be included in span tags")

	reqSpan.Finish()
}

// baggageSpanTagTest represents a test case for baggage span tag functionality
type baggageSpanTagTest struct {
	name           string
	envValue       string            // DD_TRACE_BAGGAGE_TAG_KEYS value
	baggageHeader  string            // baggage header value
	preSetBaggage  map[string]string // baggage to set in context before request
	expectedTags   map[string]string // tags that should be present with their values
	unexpectedTags []string          // tag keys that should not be present
	needsResetCfg  bool              // whether to call ResetCfg()
}

// runBaggageSpanTagTest is a helper function that runs a baggage span tag test case
func runBaggageSpanTagTest(t *testing.T, tc baggageSpanTagTest) {
	t.Helper()

	// Set up environment variable if specified
	if tc.envValue != "" {
		os.Setenv("DD_TRACE_BAGGAGE_TAG_KEYS", tc.envValue)
		defer os.Unsetenv("DD_TRACE_BAGGAGE_TAG_KEYS")
		if tc.needsResetCfg {
			ResetCfg()
		}
	}

	tracer.Start()
	defer tracer.Stop()

	// Create base context with pre-set baggage if specified
	ctx := context.Background()
	for key, value := range tc.preSetBaggage {
		ctx = baggage.Set(ctx, key, value)
	}

	// Create HTTP request with context
	req := httptest.NewRequest(http.MethodGet, "/somePath", nil).WithContext(ctx)

	// Set baggage header
	if tc.baggageHeader != "" {
		req.Header.Set("baggage", tc.baggageHeader)
	}

	// Start request span and get span tags
	span, _, _ := StartRequestSpan(req)
	m := span.AsMap()

	// Check expected tags
	for key, expectedValue := range tc.expectedTags {
		assert.Contains(t, m, key, fmt.Sprintf("%s should be included in span tags", key))
		assert.Equal(t, expectedValue, m[key], fmt.Sprintf("should contain %s value", key))
	}

	// Check unexpected tags
	for _, key := range tc.unexpectedTags {
		assert.NotContains(t, m, key, fmt.Sprintf("%s should not be included in span tags", key))
	}

	span.Finish()
}

func TestBaggageSpanTags(t *testing.T) {
	tests := []baggageSpanTagTest{
		{
			name:          "default",
			baggageHeader: "header_key=header_value,account.id=456,session.id=789",
			preSetBaggage: map[string]string{"user.id": "1234"},
			expectedTags: map[string]string{
				"baggage.account.id": "456",
				"baggage.session.id": "789",
			},
			unexpectedTags: []string{"baggage.header_key", "baggage.user.id"},
		},
		{
			name:          "wildcard",
			envValue:      "*",
			needsResetCfg: true,
			baggageHeader: "user.id=abcd,account.id=456,session.id=789,color=blue,foo=bar",
			expectedTags: map[string]string{
				"baggage.account.id": "456",
				"baggage.user.id":    "abcd",
				"baggage.session.id": "789",
				"baggage.color":      "blue",
				"baggage.foo":        "bar",
			},
		},
		{
			name:          "disabled",
			envValue:      " ",
			needsResetCfg: true,
			baggageHeader: "user.id=abcd,account.id=456,session.id=789,color=blue",
			unexpectedTags: []string{
				"baggage.account.id",
				"baggage.user.id",
				"baggage.session.id",
				"baggage.color",
			},
		},
		{
			name:          "specify_keys",
			envValue:      "device,os.version,app.version",
			needsResetCfg: true,
			baggageHeader: "device=mobile,os.version=14.2,app.version=5.3.1,account.id=456,session.id=789,color=blue",
			expectedTags: map[string]string{
				"baggage.device":      "mobile",
				"baggage.os.version":  "14.2",
				"baggage.app.version": "5.3.1",
			},
			unexpectedTags: []string{
				"baggage.account.id",
				"baggage.session.id",
				"baggage.color",
			},
		},
		{
			name:          "asterisk_key",
			envValue:      "user.id,*version",
			needsResetCfg: true,
			baggageHeader: "usr.id=fakeuser,*version=9.4,app.version=9.1.2",
			expectedTags: map[string]string{
				"baggage.*version": "9.4",
			},
			unexpectedTags: []string{
				"baggage.user.id",
				"baggage.usr.id",
				"baggage.app.version",
			},
		},
		{
			name:          "malformed_header",
			baggageHeader: "user.id=,account.id=456,session.id=789,foo=bar",
			unexpectedTags: []string{
				"baggage.account.id",
				"baggage.user.id",
				"baggage.session.id",
				"baggage.foo",
			},
		},
		{
			name:          "case_sensitive",
			baggageHeader: "user.id=doggo,ACCOUNT.id=456,seSsIon.id=789",
			expectedTags: map[string]string{
				"baggage.user.id": "doggo",
			},
			unexpectedTags: []string{
				"baggage.account.id",
				"baggage.session.id",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runBaggageSpanTagTest(t, tt)
		})
	}
}

// TestStartRequestSpanOnlyBaggageCreatesNewTrace verifies that when only baggage headers are present
// (no trace/span IDs), a new trace is created with a non-zero trace ID while still preserving the baggage.
func TestStartRequestSpanOnlyBaggageCreatesNewTrace(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()

	// Create a request with only baggage header, no trace context
	req := httptest.NewRequest(http.MethodGet, "/somePath", nil).WithContext(context.Background())
	req.Header.Set("baggage", "foo=bar")

	span, ctx, _ := StartRequestSpan(req)
	span.Finish()

	// Verify that a new trace was created with a non-zero trace ID
	sc := span.Context()
	lower := sc.TraceIDLower()
	assert.NotZero(
		t,
		lower,
		"expected a new non‐zero TraceIDLower when only baggage header is present",
	)

	// Verify that baggage is still propagated despite the new trace
	baggageMap := baggage.All(ctx)
	assert.Equal(t, "bar", baggageMap["foo"], "should propagate baggage even when it's the only header")

}
