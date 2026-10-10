// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package gorilla_mux

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

type TestCaseParentRoute struct {
	TestCaseOTelSemantics
	parent *http.ServeMux
}

func (tc *TestCaseParentRoute) Setup(ctx context.Context, t *testing.T) {
	tc.TestCaseOTelSemantics.Setup(ctx, t)
	unmatchedHandler := func(status int) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			span, ok := tracer.SpanFromContext(r.Context())
			require.True(t, ok)
			require.NotContains(t, span.AsMap(), "http.route")
			w.WriteHeader(status)
		})
	}
	tc.router.NotFoundHandler = unmatchedHandler(http.StatusNotFound)
	tc.router.MethodNotAllowedHandler = unmatchedHandler(http.StatusMethodNotAllowed)
	tc.router.HandleFunc("/api/users/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}).Methods(http.MethodGet)
	tc.parent = http.NewServeMux()
	tc.parent.Handle("/api/", tc.router)
}

func (tc *TestCaseParentRoute) Run(_ context.Context, t *testing.T) {
	for _, tt := range []struct {
		method string
		path   string
		status int
	}{
		{method: http.MethodGet, path: "/api/missing", status: http.StatusNotFound},
		{method: http.MethodPost, path: "/api/users/123", status: http.StatusMethodNotAllowed},
		{method: http.MethodGet, path: "/api/users/123", status: http.StatusOK},
	} {
		recorder := httptest.NewRecorder()
		tc.parent.ServeHTTP(recorder, httptest.NewRequest(tt.method, tt.path, nil))
		require.Equal(t, tt.status, recorder.Code)
	}
}

func (*TestCaseParentRoute) ExpectedTraces() trace.Traces {
	traces := make(trace.Traces, 0, 3)
	for _, tt := range []struct {
		method   string
		resource string
		status   string
		route    string
	}{
		{method: http.MethodGet, resource: "GET", status: "404"},
		{method: http.MethodPost, resource: "POST", status: "405"},
		{method: http.MethodGet, resource: "GET /api/users/{id}", status: "200", route: "/api/users/{id}"},
	} {
		span := &trace.Trace{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": tt.resource,
				"type":     "web",
				"service":  "mux.router",
			},
			Meta: map[string]string{
				"component":                 "gorilla/mux",
				"span.kind":                 "server",
				"http.request.method":       tt.method,
				"http.response.status_code": tt.status,
			},
		}
		if tt.route != "" {
			span.Meta["http.route"] = tt.route
		}
		traces = append(traces, span)
	}
	return traces
}
