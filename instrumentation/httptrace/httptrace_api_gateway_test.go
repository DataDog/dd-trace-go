// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"fmt"
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

		req, err := http.NewRequest("GET", fmt.Sprintf("%s/", srvURL), nil)
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

		req, err := http.NewRequest("GET", fmt.Sprintf("%s/error", srvURL), nil)
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

		req, err := http.NewRequest("GET", fmt.Sprintf("%s/no-aws-headers", srvURL), nil)
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

		req, err := http.NewRequest("GET", fmt.Sprintf("%s/no-aws-headers", srvURL), nil)
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

		req, err := http.NewRequest("GET", fmt.Sprintf("%s/", srvURL), nil)
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
