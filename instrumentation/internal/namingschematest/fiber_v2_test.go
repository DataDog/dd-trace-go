// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2024 Datadog, Inc.

package namingschematest

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	fibertrace "github.com/DataDog/dd-trace-go/contrib/gofiber/fiber.v2/v2"
	"github.com/DataDog/dd-trace-go/instrumentation/internal/namingschematest/v2/harness"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation"
)

var fiberV2Test = harness.TestCase{
	Name: instrumentation.PackageGoFiberV2,
	GenSpans: func(t *testing.T, serviceOverride string) []*mocktracer.Span {
		var opts []fibertrace.Option
		if serviceOverride != "" {
			opts = append(opts, fibertrace.WithService(serviceOverride))
		}
		mt := mocktracer.Start()
		defer mt.Stop()

		mux := fiber.New()
		mux.Use(fibertrace.Middleware(opts...))
		mux.Get("/200", func(c *fiber.Ctx) error {
			return c.SendString("ok")
		})
		req := httptest.NewRequest("GET", "/200", nil)
		resp, err := mux.Test(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		return mt.FinishedSpans()
	},
	WantServiceNameV0: harness.ServiceNameAssertions{
		Defaults:        []string{"fiber"},
		DDService:       []string{harness.TestDDService},
		ServiceOverride: []string{harness.TestServiceOverride},
	},
	AssertOpV0: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "http.request", spans[0].OperationName())
	},
	AssertOpV1: func(t *testing.T, spans []*mocktracer.Span) {
		require.Len(t, spans, 1)
		assert.Equal(t, "http.server.request", spans[0].OperationName())
	},
}
