// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"testing"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/baggage"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal"
	"github.com/DataDog/dd-trace-go/v2/internal/log"
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"

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
	for i := 0; i < b.N; i++ {
		StartRequestSpan(r, opts...)
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

func TestBaggageSpanTagsDefault(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()

	// Create a base context with pre-set baggage.
	baseCtx := baggage.Set(context.Background(), "user.id", "1234")

	// Create an HTTP request with that context.
	req := httptest.NewRequest(http.MethodGet, "/somePath", nil).WithContext(baseCtx)

	// Set the baggage header with additional baggage items.
	req.Header.Set("baggage", "header_key=header_value,account.id=456,session.id=789")

	// Start the request span, which will extract header baggage and merge it with the context's baggage.
	span, _, _ := StartRequestSpan(req)
	m := span.AsMap()

	// Keys that SHOULD be present:
	assert.Contains(t, m, "baggage.account.id", "baggage.account.id should be included in span tags")
	assert.Equal(t, "456", m["baggage.account.id"], "should contain account.id value")

	assert.Contains(t, m, "baggage.session.id", "baggage.session.id should be included in span tags")
	assert.Equal(t, "789", m["baggage.session.id"], "should contain session.id value")

	// Keys that should NOT be present:
	assert.NotContains(t, m, "baggage.header_key", "baggage.header_key should not be included in span tags")
	assert.NotContains(t, m, "baggage.user.id", "baggage.user.id should not be included in span tags")

	span.Finish()
}

func TestBaggageSpanTagsWildcard(t *testing.T) {
	os.Setenv("DD_TRACE_BAGGAGE_TAG_KEYS", "*")
	ResetCfg()
	tracer.Start()
	defer tracer.Stop()

	// Create an HTTP request with that context.
	req := httptest.NewRequest(http.MethodGet, "/somePath", nil).WithContext(context.Background())

	// Set the baggage header with additional baggage items.
	req.Header.Set("baggage", "user.id=abcd,account.id=456,session.id=789,color=blue,foo=bar")

	// Start the request span, which will extract header baggage and merge it with the context's baggage.
	span, _, _ := StartRequestSpan(req)
	m := span.AsMap()

	// Assert that the expected baggage keys are present with the correct values.
	assert.Contains(t, m, "baggage.account.id", "baggage.account.id should be included in span tags")
	assert.Equal(t, "456", m["baggage.account.id"], "should contain account.id value")

	assert.Contains(t, m, "baggage.user.id", "baggage.user.id should be included in span tags")
	assert.Equal(t, "abcd", m["baggage.user.id"], "should contain user.id value")

	assert.Contains(t, m, "baggage.session.id", "baggage.session.id should be included in span tags")
	assert.Equal(t, "789", m["baggage.session.id"], "should contain session.id value")

	assert.Contains(t, m, "baggage.color", "baggage.color should be included in span tags")
	assert.Equal(t, "blue", m["baggage.color"], "should contain color value")

	assert.Contains(t, m, "baggage.foo", "baggage.foo should be included in span tags")
	assert.Equal(t, "bar", m["baggage.foo"], "should contain foo value")

	span.Finish()
}

func TestBaggageSpanTagsDisabled(t *testing.T) {
	os.Setenv("DD_TRACE_BAGGAGE_TAG_KEYS", "")
	ResetCfg()
	tracer.Start()
	defer tracer.Stop()

	// Create an HTTP request with that context.
	req := httptest.NewRequest(http.MethodGet, "/somePath", nil).WithContext(context.Background())

	// Set the baggage header with additional baggage items.
	req.Header.Set("baggage", "user.id=abcd,account.id=456,session.id=789,color=blue")

	// Start the request span, which will extract header baggage and merge it with the context's baggage.
	span, _, _ := StartRequestSpan(req)
	m := span.AsMap()

	// No values should be present in span tags:
	assert.NotContains(t, m, "baggage.account.id", "baggage.account.id should not be included in span tags")
	assert.NotContains(t, m, "baggage.user.id", "baggage.user.id should not be included in span tags")
	assert.NotContains(t, m, "baggage.session.id", "baggage.session.id should not be included in span tags")
	assert.NotContains(t, m, "baggage.color", "baggage.color should not be included in span tags")

	span.Finish()
}

func TestBaggageSpanTagsSpecifyKeys(t *testing.T) {
	os.Setenv("DD_TRACE_BAGGAGE_TAG_KEYS", "device,os.version,app.version")
	ResetCfg()
	tracer.Start()
	defer tracer.Stop()

	// Create an HTTP request with that context.
	req := httptest.NewRequest(http.MethodGet, "/somePath", nil).WithContext(context.Background())

	// Set the baggage header with additional baggage items.
	req.Header.Set("baggage", "device=mobile,os.version=14.2,app.version=5.3.1,account.id=456,session.id=789,color=blue")

	// Start the request span, which will extract header baggage and merge it with the context's baggage.
	span, _, _ := StartRequestSpan(req)
	m := span.AsMap()

	// Keys that should NOT be present:
	assert.NotContains(t, m, "baggage.account.id", "baggage.account.id should not be included in span tags")
	assert.NotContains(t, m, "baggage.session.id", "baggage.session.id should not be included in span tags")
	assert.NotContains(t, m, "baggage.color", "baggage.color should not be included in span tags")

	// Keys that SHOULD be present, with correct values:
	assert.Contains(t, m, "baggage.device", "baggage.device should be included in span tags")
	assert.Equal(t, "mobile", m["baggage.device"], "should contain baggage.device value")

	assert.Contains(t, m, "baggage.os.version", "baggage.os.version should be included in span tags")
	assert.Equal(t, "14.2", m["baggage.os.version"], "should contain baggage.os.version value")

	assert.Contains(t, m, "baggage.app.version", "baggage.app.version should be included in span tags")
	assert.Equal(t, "5.3.1", m["baggage.app.version"], "should contain baggage.app.version value")

	span.Finish()
}

func TestBaggageSpanTagsAsteriskKey(t *testing.T) {
	os.Setenv("DD_TRACE_BAGGAGE_TAG_KEYS", "user.id,*version")
	ResetCfg()
	tracer.Start()
	defer tracer.Stop()

	// Create an HTTP request with that context.
	req := httptest.NewRequest(http.MethodGet, "/somePath", nil).WithContext(context.Background())

	// Set the baggage header with additional baggage items.
	req.Header.Set("baggage", "usr.id=fakeuser,*version=9.4,app.version=9.1.2")

	// Start the request span, which will extract header baggage and merge it with the context's baggage.
	span, _, _ := StartRequestSpan(req)
	m := span.AsMap()

	// Keys that should NOT be present:
	assert.NotContains(t, m, "baggage.user.id", "baggage.user.id should not be included in span tags")
	assert.NotContains(t, m, "baggage.usr.id", "baggage.usr.id should not be included in span tags")
	assert.NotContains(t, m, "baggage.app.version", "baggage.app.version should not be included in span tags")

	// Key that SHOULD be present, with correct value:
	assert.Contains(t, m, "baggage.*version", "baggage.*version should be included in span tags")
	assert.Equal(t, "9.4", m["baggage.*version"], "should contain baggage.*version value")

	span.Finish()
}

func TestBaggageSpanTagsMalformedHeader(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()

	// Create an HTTP request with that context.
	req := httptest.NewRequest(http.MethodGet, "/somePath", nil).WithContext(context.Background())

	// Set the baggage header with additional baggage items.
	req.Header.Set("baggage", "user.id=,account.id=456,session.id=789,foo=bar")

	// Start the request span, which will extract header baggage and merge it with the context's baggage.
	span, _, _ := StartRequestSpan(req)
	m := span.AsMap()

	// If the baggage header is malformed, the entire header should be dropped,
	// so none of these keys should be present in the span tags.
	assert.NotContains(t, m, "baggage.account.id", "baggage.account.id should not be included in span tags")
	assert.NotContains(t, m, "baggage.user.id", "baggage.user.id should not be included in span tags")
	assert.NotContains(t, m, "baggage.session.id", "baggage.session.id should not be included in span tags")
	assert.NotContains(t, m, "baggage.foo", "baggage.foo should not be included in span tags")

	span.Finish()
}

func TestBaggageSpanTagsCaseSensitive(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()

	// Create an HTTP request with that context.
	req := httptest.NewRequest(http.MethodGet, "/somePath", nil).WithContext(context.Background())

	// Set the baggage header with additional baggage items.
	req.Header.Set("baggage", "user.id=doggo,ACCOUNT.id=456,seSsIon.id=789")

	// Start the request span, which will extract header baggage and merge it with the context's baggage.
	span, _, _ := StartRequestSpan(req)
	m := span.AsMap()

	// Only the exact lowercase key "user.id" should be included.
	assert.NotContains(t, m, "baggage.account.id", "baggage.account.id should not be included in span tags")
	assert.NotContains(t, m, "baggage.session.id", "baggage.session.id should not be included in span tags")

	assert.Contains(t, m, "baggage.user.id", "baggage.user.id should be included in span tags")
	assert.Equal(t, "doggo", m["baggage.user.id"], "should contain baggage.user.id value")

	span.Finish()
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

func TestBaggageSpanTagsOpentracer(t *testing.T) {
	tracer.Start()
	defer tracer.Stop()

	// Create an HTTP request with no additional baggage context
	req := httptest.NewRequest(http.MethodGet, "/somePath", nil).WithContext(context.Background())
	req.Header.Set("x-datadog-trace-id", "1")
	req.Header.Set("x-datadog-parent-id", "2")
	req.Header.Set("baggage", "session.id=789") // w3c baggage header
	req.Header.Set("baggage", "session.id=789") // w3c baggage header

	// Start the request span, which will extract baggage and add it as span tags
	reqSpan, _, _ := StartRequestSpan(req)
	m := reqSpan.AsMap()

	// Keys that SHOULD be present:
	assert.Contains(t, m, "baggage.session.id", "baggage.session.id should be included in span tags")
	assert.Equal(t, "789", m["baggage.session.id"], "should contain session.id value")

	// Keys that should NOT be present (user.id is ot-baggage header)
	// This assertion WILL FAIL until baggage revamp is complete; therefore, commented out
	// assert.NotContains(t, m, "baggage.user.id", "baggage.user.id should not be included in span tags")

	reqSpan.Finish()
}
