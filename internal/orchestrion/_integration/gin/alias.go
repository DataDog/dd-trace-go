// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package gin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	web "github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseAliasedImport checks that an engine is traced exactly once when gin
// is imported under another name.
type TestCaseAliasedImport struct {
	handler http.Handler
}

func (tc *TestCaseAliasedImport) Setup(context.Context, *testing.T) {
	web.SetMode(web.ReleaseMode) // Silence start-up logging
	e := web.New()
	e.GET("/alias", func(c *web.Context) { c.Status(http.StatusOK) })
	tc.handler = e
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
				"service":  "gin.router",
			},
			Meta: map[string]string{
				"component": "gin-gonic/gin",
			},
		},
	}
}
