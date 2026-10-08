// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/internal/normalizer"
)

func TestInferredProxySpans(t *testing.T) {
	t.Setenv("DD_SERVICE", "aws-server")
	t.Setenv("DD_TRACE_INFERRED_PROXY_SERVICES_ENABLED", "true")
	ResetCfg()

	startTime := time.Now().Add(-5 * time.Second)

	inferredHeaders := map[string]string{
		"x-dd-proxy":                 "aws-apigateway",
		"x-dd-proxy-request-time-ms": strconv.FormatInt(startTime.UnixMilli(), 10),
		"x-dd-proxy-path":            "/test",
		"x-dd-proxy-httpmethod":      "GET",
		"x-dd-proxy-domain-name":     "example.com",
		"x-dd-proxy-stage":           "dev",
	}
	srvURL := "https://example.com/test"

	t.Run("should create parent and child spans for a 200", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("GET", srvURL+"/", nil)
		require.NoError(t, err)

		for k, v := range inferredHeaders {
			req.Header.Set(k, v)
		}

		_, _, finishSpans := StartRequestSpan(req)
		finishSpans(200, nil)

		spans := mt.FinishedSpans()
		require.Equal(t, 2, len(spans))

		gwSpan := spans[1]
		webReqSpan := spans[0]
		assert.Equal(t, "aws.apigateway", gwSpan.OperationName())
		assert.Equal(t, "http.request", webReqSpan.OperationName())
		assert.Equal(t, "example.com", gwSpan.Tag("service.name"))
		assert.Equal(t, float64(1), gwSpan.Tag("_dd.inferred_span"))
		assert.True(t, webReqSpan.ParentID() == gwSpan.SpanID())
		assert.Equal(t, webReqSpan.Tag("http.status_code"), gwSpan.Tag("http.status_code"))
		assert.Equal(t, webReqSpan.Tag("span.type"), gwSpan.Tag("span.type"))

		assert.Equal(t, startTime.UnixMilli(), gwSpan.StartTime().UnixMilli())

		for _, arg := range inferredHeaders {
			header, tag := normalizer.HeaderTag(arg)

			// Default to an empty string if the tag does not exist
			gwSpanTags, exists := gwSpan.Tags()[tag]
			if !exists {
				gwSpanTags = ""
			}
			expectedTags := strings.Join(req.Header.Values(header), ",")
			assert.Equal(t, expectedTags, gwSpanTags)
		}
	})

	t.Run("should create parent and child spans for error", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("GET", srvURL+"/error", nil)
		require.NoError(t, err)

		for k, v := range inferredHeaders {
			req.Header.Set(k, v)
		}

		_, _, finishSpans := StartRequestSpan(req)
		finishSpans(500, nil)

		spans := mt.FinishedSpans()
		require.Equal(t, 2, len(spans))

		gwSpan := spans[1]
		webReqSpan := spans[0]
		assert.Equal(t, "aws.apigateway", gwSpan.OperationName())
		assert.Equal(t, "http.request", webReqSpan.OperationName())
		assert.Equal(t, "example.com", gwSpan.Tag("service.name"))
		assert.Equal(t, float64(1), gwSpan.Tag("_dd.inferred_span"))
		assert.True(t, webReqSpan.ParentID() == gwSpan.SpanID())
		assert.Equal(t, webReqSpan.Tag("http.status_code"), gwSpan.Tag("http.status_code"))
		assert.Equal(t, webReqSpan.Tag("span.type"), gwSpan.Tag("span.type"))
		assert.Equal(t, startTime.UnixMilli(), gwSpan.StartTime().UnixMilli())

		assert.Equal(t, "500: Internal Server Error", gwSpan.Tag(ext.ErrorMsg))
		assert.Equal(t, "500: Internal Server Error", webReqSpan.Tag(ext.ErrorMsg))

		for _, arg := range inferredHeaders {
			header, tag := normalizer.HeaderTag(arg)

			// Default to an empty string if the tag does not exist
			gwSpanTags, exists := gwSpan.Tags()[tag]
			if !exists {
				gwSpanTags = ""
			}
			expectedTags := strings.Join(req.Header.Values(header), ",")
			assert.Equal(t, expectedTags, gwSpanTags)
		}
	})

	t.Run("should not create API Gateway span if headers are missing", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("GET", srvURL+"/no-aws-headers", nil)
		require.NoError(t, err)

		_, _, finishSpans := StartRequestSpan(req)
		finishSpans(200, nil)

		assert.Equal(t, http.StatusOK, 200)

		spans := mt.FinishedSpans()
		require.Equal(t, 1, len(spans))
		assert.Equal(t, "http.request", spans[0].OperationName())
	})

	t.Run("should not create API Gateway span if x-dd-proxy is missing", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("GET", srvURL+"/no-aws-headers", nil)
		require.NoError(t, err)

		for k, v := range inferredHeaders {
			if k != "x-dd-proxy" {
				req.Header.Set(k, v)
			}
		}

		_, _, finishSpans := StartRequestSpan(req)
		finishSpans(200, nil)

		spans := mt.FinishedSpans()
		assert.Equal(t, 1, len(spans))
		assert.Equal(t, "http.request", spans[0].OperationName())
	})

	t.Run("should not create more than one API Gateway span for a local trace", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("GET", srvURL+"/", nil)
		require.NoError(t, err)
		for k, v := range inferredHeaders {
			req.Header.Set(k, v)
		}

		_, ctx, finishSpans1 := StartRequestSpan(req)
		finishSpans1(200, nil)

		req2 := req.WithContext(ctx)
		_, _, finishSpans2 := StartRequestSpan(req2)
		finishSpans2(200, nil)

		spans := mt.FinishedSpans()
		require.Equal(t, 3, len(spans))

		gwSpan := spans[1]
		webReqSpan := spans[0]
		assert.Equal(t, "aws.apigateway", gwSpan.OperationName())
		assert.Equal(t, "http.request", webReqSpan.OperationName())
		assert.Equal(t, "example.com", gwSpan.Tag("service.name"))
		assert.Equal(t, float64(1), gwSpan.Tag("_dd.inferred_span"))
		assert.True(t, webReqSpan.ParentID() == gwSpan.SpanID())
		assert.Equal(t, webReqSpan.Tag("http.status_code"), gwSpan.Tag("http.status_code"))
		assert.Equal(t, webReqSpan.Tag("span.type"), gwSpan.Tag("span.type"))

		assert.Equal(t, startTime.UnixMilli(), gwSpan.StartTime().UnixMilli())
	})
}

// TestInferredProxySpanQueryString checks that the query string in the
// X-Dd-Proxy-Path header is obfuscated in http.url, removed when the
// configured obfuscation regexp is not valid, and never in the route or the
// resource name.
func TestInferredProxySpanQueryString(t *testing.T) {
	for _, tc := range []struct {
		name   string
		env    map[string]string
		path   string
		domain string
		want   string
	}{
		{name: "no query", path: "/test", domain: "example.com", want: "example.com/test"},
		{name: "obfuscated", path: "/test?password=secret&a=1", domain: "example.com", want: "example.com/test?<redacted>&a=1"},
		// A domain name cannot contain '?': the query string of the domain header is removed.
		{name: "query in domain", path: "/test?a=1", domain: "example.com?token=secret", want: "example.com/test?a=1"},
		{name: "invalid regexp fails closed", env: map[string]string{EnvQueryStringRegexp: `(?<=x)a`}, path: "/test?password=secret&a=1", domain: "example.com", want: "example.com/test"},
		{name: "obfuscation disabled", env: map[string]string{EnvQueryStringRegexp: ""}, path: "/test?a=1", domain: "example.com", want: "example.com/test?a=1"},
		{name: "query string disabled", env: map[string]string{envQueryStringDisabled: "true"}, path: "/test?a=1", domain: "example.com", want: "example.com/test"},
		{name: "server allowlist", env: map[string]string{envServerQueryStringAllowlist: "a", envClientQueryStringAllowlist: "b"}, path: "/test?a=1&b=2&token=x", domain: "example.com", want: "example.com/test?a=1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldCfg := cfg
			t.Cleanup(func() { cfg = oldCfg })
			t.Setenv("DD_TRACE_INFERRED_PROXY_SERVICES_ENABLED", "true")
			for _, k := range []string{EnvQueryStringRegexp, envQueryStringDisabled, envQueryStringAllowlist, envClientQueryStringAllowlist, envServerQueryStringAllowlist} {
				if v, ok := tc.env[k]; ok {
					t.Setenv(k, v)
				} else {
					unsetEnv(t, k)
				}
			}
			ResetCfg()

			mt := mocktracer.Start()
			defer mt.Stop()

			req, err := http.NewRequest("GET", "https://example.com/test", nil)
			require.NoError(t, err)
			req.Header.Set("x-dd-proxy", "aws-apigateway")
			req.Header.Set("x-dd-proxy-request-time-ms", strconv.FormatInt(time.Now().UnixMilli(), 10))
			req.Header.Set("x-dd-proxy-path", tc.path)
			req.Header.Set("x-dd-proxy-httpmethod", "GET")
			req.Header.Set("x-dd-proxy-domain-name", tc.domain)

			_, _, finishSpans := StartRequestSpan(req)
			finishSpans(200, nil)

			spans := mt.FinishedSpans()
			require.Len(t, spans, 2)
			gwSpan := spans[1]
			require.Equal(t, "aws.apigateway", gwSpan.OperationName())
			assert.Equal(t, tc.want, gwSpan.Tag(ext.HTTPURL))
			assert.Equal(t, "/test", gwSpan.Tag(ext.HTTPRoute))
			assert.Equal(t, "GET /test", gwSpan.Tag(ext.ResourceName))
			assert.Equal(t, "example.com", gwSpan.Tag(ext.ServiceName))
		})
	}
}
