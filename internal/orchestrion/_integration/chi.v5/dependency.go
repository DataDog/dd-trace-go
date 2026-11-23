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

	"example.com/chidep/v5router"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that a router created in a dependency module, not
// in the module being built, is traced.
type TestCaseDependency struct {
	handler http.Handler
}

func (tc *TestCaseDependency) Setup(context.Context, *testing.T) {
	tc.handler = v5router.New()
}

func (tc *TestCaseDependency) Run(ctx context.Context, t *testing.T) {
	rec := httptest.NewRecorder()
	tc.handler.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/dep", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /dep",
			},
			Meta: map[string]string{
				"component": "go-chi/chi.v5",
			},
		},
	}
}
