// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026-present Datadog, Inc.

package fiber

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"example.com/fiberdep"
	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// TestCaseDependency checks that an app created in a dependency module, not in
// the module being built, is traced.
type TestCaseDependency struct {
	app *fiber.App
}

func (tc *TestCaseDependency) Setup(context.Context, *testing.T) {
	tc.app = fiberdep.New()
}

func (tc *TestCaseDependency) Run(_ context.Context, t *testing.T) {
	res, err := tc.app.Test(httptest.NewRequest(http.MethodGet, "/dep", nil))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func (*TestCaseDependency) ExpectedSpanCount() int {
	return 1
}

func (*TestCaseDependency) ExpectedTraces() trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": "GET /dep",
				"type":     "web",
			},
			Meta: map[string]string{
				"component": "gofiber/fiber.v2",
				"span.kind": "server",
			},
		},
	}
}
