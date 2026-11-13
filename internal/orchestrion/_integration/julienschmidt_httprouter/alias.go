// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package julienschmidt_httprouter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	hr "github.com/julienschmidt/httprouter"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that a router is traced exactly once when
// httprouter is imported under another name.
type TestCaseAliasedImport struct {
	handler http.Handler
}

func (tc *TestCaseAliasedImport) Setup(context.Context, *testing.T) {
	r := hr.New()
	r.GET("/alias", func(w http.ResponseWriter, _ *http.Request, _ hr.Params) {
		w.WriteHeader(http.StatusOK)
	})
	tc.handler = r
}

func (tc *TestCaseAliasedImport) Run(ctx context.Context, t *testing.T) {
	rec := httptest.NewRecorder()
	tc.handler.ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/alias", nil))
	require.Equal(t, http.StatusOK, rec.Code)
}

func (*TestCaseAliasedImport) ExpectedSpanCount() int {
	return 1
}

func (*TestCaseAliasedImport) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /alias",
				"type":     "web",
				"service":  "http.router",
			},
			Meta: map[string]string{
				"component":  "julienschmidt/httprouter",
				"span.kind":  "server",
				"http.route": "/alias",
			},
		},
	}
}
