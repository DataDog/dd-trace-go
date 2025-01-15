// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package httptrace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/normalizer"
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

	mux := http.NewServeMux()

	// set routes
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/error" {
			w.WriteHeader(http.StatusInternalServerError)
			err := json.NewEncoder(w).Encode(map[string]string{"message": "ERROR"})
			if err != nil {
				return
			}
		} else {
			w.WriteHeader(http.StatusOK)
			err := json.NewEncoder(w).Encode(map[string]string{"message": "OK"})
			if err != nil {
				return
			}
		}
	})

	srv := httptest.NewServer(mux)
	defer srv.Close()

	t.Run("should create parent and child spans for a 200", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := &http.Client{}
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/", srv.URL), nil)
		require.NoError(t, err)

		for k, v := range inferredHeaders {
			req.Header.Set(k, v)
		}

		_, _, finishSpans := StartRequestSpan(req)
		resp, err := client.Do(req)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		finishSpans(resp.StatusCode, nil)

		spans := mt.FinishedSpans()
		require.Equal(t, 2, len(spans))

		gwSpan := spans[1]
		webReqSpan := spans[0]
		assert.Equal(t, "aws.apigateway", gwSpan.OperationName())
		assert.Equal(t, "http.request", webReqSpan.OperationName())
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
			// compare expected and actual values
			assert.Equal(t, expectedTags, gwSpanTags)
		}
	})

	t.Run("should create parent and child spans for error", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := &http.Client{}
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/error", srv.URL), nil)
		require.NoError(t, err)

		for k, v := range inferredHeaders {
			req.Header.Set(k, v)
		}

		_, _, finishSpans := StartRequestSpan(req)

		resp, err := client.Do(req)
		require.NoError(t, err)

		finishSpans(resp.StatusCode, nil)

		assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)

		spans := mt.FinishedSpans()
		require.Equal(t, 2, len(spans))

		gwSpan := spans[1]
		webReqSpan := spans[0]
		assert.Equal(t, "aws.apigateway", gwSpan.OperationName())
		assert.Equal(t, "http.request", webReqSpan.OperationName())
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
			// compare expected and actual values
			assert.Equal(t, expectedTags, gwSpanTags)
		}
	})

	t.Run("should not create API Gateway span if headers are missing", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := &http.Client{}
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/no-aws-headers", srv.URL), nil)
		require.NoError(t, err)

		_, _, finishSpans := StartRequestSpan(req)
		resp, err := client.Do(req)
		require.NoError(t, err)

		finishSpans(resp.StatusCode, nil)

		assert.Equal(t, http.StatusOK, resp.StatusCode)

		spans := mt.FinishedSpans()
		require.Equal(t, 1, len(spans))
		assert.Equal(t, "http.request", spans[0].OperationName())
	})

	t.Run("should not create API Gateway span if x-dd-proxy is missing", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		client := &http.Client{}
		req, err := http.NewRequest("GET", fmt.Sprintf("%s/no-aws-headers", srv.URL), nil)
		require.NoError(t, err)

		for k, v := range inferredHeaders {
			if k != "x-dd-proxy" {
				req.Header.Set(k, v)
			}
		}

		_, _, finishSpans := StartRequestSpan(req)
		resp, err := client.Do(req)
		require.NoError(t, err)

		finishSpans(resp.StatusCode, nil)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		spans := mt.FinishedSpans()
		assert.Equal(t, 1, len(spans))
		assert.Equal(t, "http.request", spans[0].OperationName())
	})
}
