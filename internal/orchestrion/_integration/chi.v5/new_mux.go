// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package chiv5

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseNewMux checks that a router created with chi.NewMux is traced.
type TestCaseNewMux struct {
	handler http.Handler
}

func (tc *TestCaseNewMux) Setup(context.Context, *testing.T) {
	mux := chi.NewMux()
	mux.Get("/mux", ok)
	tc.handler = mux
}

func (tc *TestCaseNewMux) Run(ctx context.Context, t *testing.T) {
	serve(ctx, t, tc.handler, "/mux")
}

func (*TestCaseNewMux) ExpectedTraces() trace.Traces {
	return expectChiSpan("GET /mux")
}

func ok(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
}

func serve(ctx context.Context, t *testing.T, h http.Handler, path string) {
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func expectChiSpan(resource string) trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": resource,
			},
			Meta: map[string]string{
				"component": "go-chi/chi.v5",
			},
		},
	}
}
