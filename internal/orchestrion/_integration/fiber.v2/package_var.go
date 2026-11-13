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

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/internal/orchestrion/_integration/internal/trace"
)

// pkgApp is created during package initialization, before the tracer starts.
var pkgApp = fiber.New(fiber.Config{DisableStartupMessage: true})

// TestCasePackageVar checks that an app created in a package-level variable is
// traced exactly once.
type TestCasePackageVar struct{}

func (*TestCasePackageVar) Setup(context.Context, *testing.T) {
	pkgApp.Get("/var", func(c *fiber.Ctx) error { return c.SendString("ok") })
}

func (*TestCasePackageVar) Run(_ context.Context, t *testing.T) {
	get(t, pkgApp, "/var")
}

func (*TestCasePackageVar) ExpectedSpanCount() int {
	return 1
}

func (*TestCasePackageVar) ExpectedTraces() trace.Traces {
	return expectFiberSpan("GET /var")
}

func get(t *testing.T, app *fiber.App, path string) {
	res, err := app.Test(httptest.NewRequest(http.MethodGet, path, nil))
	require.NoError(t, err)
	require.NoError(t, res.Body.Close())
	require.Equal(t, http.StatusOK, res.StatusCode)
}

func expectFiberSpan(resource string) trace.Traces {
	return trace.Traces{
		{
			Tags: map[string]any{
				"name":     "http.request",
				"resource": resource,
				"type":     "web",
			},
			Meta: map[string]string{
				"component": "gofiber/fiber.v2",
				"span.kind": "server",
			},
		},
	}
}
