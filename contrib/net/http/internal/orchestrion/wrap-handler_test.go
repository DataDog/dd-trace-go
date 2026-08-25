// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package orchestrion

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

func TestWrapHandlerOTelSemantics(t *testing.T) {
	setOTelSemantics(t, true)

	t.Run("generic handler uses method only", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_HANDLER_RESOURCE_NAME_QUANTIZE", "true")
		span := orchestrionHandlerSpan(t, WrapHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})), httptest.NewRequest(http.MethodGet, "/users/123", nil))
		assert.Equal(t, "GET", span.Tag(ext.ResourceName))
		assert.Nil(t, span.Tag(ext.HTTPRoute))
		assert.Equal(t, "/users/123", span.Tag(ext.URLPath))
	})

	t.Run("serve mux uses matched route", func(t *testing.T) {
		mux := http.NewServeMux()
		mux.HandleFunc("/users/{id}", func(http.ResponseWriter, *http.Request) {})
		span := orchestrionHandlerSpan(t, WrapHandler(mux), httptest.NewRequest(http.MethodGet, "/users/123", nil))
		assert.Equal(t, "GET /users/{id}", span.Tag(ext.ResourceName))
		assert.Equal(t, "/users/{id}", span.Tag(ext.HTTPRoute))
	})
}

func TestWrapHandlerDatadogResourceDefaults(t *testing.T) {
	setOTelSemantics(t, false)

	t.Run("path", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/users/123", nil)
		span := orchestrionHandlerSpan(t, WrapHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})), r)
		assert.Equal(t, resourceNamer(r), span.Tag(ext.ResourceName))
		assert.Equal(t, "GET", span.Tag(ext.HTTPMethod))
	})

	t.Run("quantized path", func(t *testing.T) {
		t.Setenv("DD_TRACE_HTTP_HANDLER_RESOURCE_NAME_QUANTIZE", "true")
		r := httptest.NewRequest(http.MethodGet, "/users/123", nil)
		span := orchestrionHandlerSpan(t, WrapHandler(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})), r)
		assert.Equal(t, quantizeResourceNamer(r), span.Tag(ext.ResourceName))
		assert.Equal(t, "GET", span.Tag(ext.HTTPMethod))
	})
}

func orchestrionHandlerSpan(t *testing.T, handler http.Handler, r *http.Request) *mocktracer.Span {
	t.Helper()
	mt := mocktracer.Start()
	t.Cleanup(mt.Stop)
	handler.ServeHTTP(httptest.NewRecorder(), r)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	return spans[0]
}
