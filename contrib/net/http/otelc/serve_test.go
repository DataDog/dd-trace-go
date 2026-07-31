// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package otelc

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/otelc/pkg/hook/hooktest"

	"github.com/DataDog/dd-trace-go/contrib/net/http/v2/internal/wrap"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/ext"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
)

// serveHTTP runs BeforeServe the way otelc's generated trampoline would at
// the top of (*http.Server).Serve, then dispatches r through the resulting
// srv.Handler the way (serverHandler).ServeHTTP would for a real connection.
func serveHTTP(t *testing.T, srv *http.Server, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	ictx := hooktest.NewMockHookContext(srv)
	BeforeServe(ictx, srv)
	w := httptest.NewRecorder()
	srv.Handler.ServeHTTP(w, r)
	return w
}

func TestBeforeServeWrapsPlainServeMuxWithPatternResource(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	mux := http.NewServeMux()
	mux.HandleFunc("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Handler: mux}

	r, err := http.NewRequest(http.MethodGet, "http://example.com/users/123", nil)
	require.NoError(t, err)

	serveHTTP(t, srv, r)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/{id}", spans[0].Tag(ext.ResourceName))
	assert.Equal(t, "/users/{id}", spans[0].Tag(ext.HTTPRoute))
	assert.NotSame(t, mux, srv.Handler, "BeforeServe must replace srv.Handler with a traced wrapper")
}

func TestBeforeServeWrapsPlainHandlerWithPathResource(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Handler: handler}

	r, err := http.NewRequest(http.MethodGet, "http://example.com/users/123", nil)
	require.NoError(t, err)

	serveHTTP(t, srv, r)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET /users/123", spans[0].Tag(ext.ResourceName))
}

func TestBeforeServeDefaultsNilHandlerToDefaultServeMux(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	pattern := "/nil-handler-" + t.Name()
	http.DefaultServeMux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{}

	r, err := http.NewRequest(http.MethodGet, "http://example.com"+pattern, nil)
	require.NoError(t, err)

	serveHTTP(t, srv, r)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	assert.Equal(t, "GET "+pattern, spans[0].Tag(ext.ResourceName))
}

func TestBeforeServeLeavesAlreadyWrappedHandlerFnUnchanged(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := wrap.Handler(inner, "svc", "custom-resource")
	srv := &http.Server{Handler: wrapped}

	r, err := http.NewRequest(http.MethodGet, "http://example.com/users/123", nil)
	require.NoError(t, err)

	serveHTTP(t, srv, r)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1, "must not wrap an already-wrapped handler again, or requests would produce nested duplicate spans")
	assert.Equal(t, "custom-resource", spans[0].Tag(ext.ResourceName))
}

func TestBeforeServeLeavesAlreadyWrappedServeMuxUnchanged(t *testing.T) {
	mt := mocktracer.Start()
	defer mt.Stop()

	mux := wrap.NewServeMux()
	mux.HandleFunc("/users/{id}", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	srv := &http.Server{Handler: mux}

	r, err := http.NewRequest(http.MethodGet, "http://example.com/users/123", nil)
	require.NoError(t, err)

	serveHTTP(t, srv, r)

	spans := mt.FinishedSpans()
	require.Len(t, spans, 1, "must not wrap an already-wrapped ServeMux again, or requests would produce nested duplicate spans")
	assert.Equal(t, "GET /users/{id}", spans[0].Tag(ext.ResourceName))
	assert.Same(t, mux, srv.Handler, "BeforeServe must leave an already-wrapped ServeMux untouched")
}
