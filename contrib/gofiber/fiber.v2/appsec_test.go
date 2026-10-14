// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016 Datadog, Inc.

package fiber

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/require"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func newAppSecRouter() *fiber.App {
	router := fiber.New()
	Wrap(router)
	router.All("/", func(c *fiber.Ctx) error {
		return c.SendString("Hello World!\n")
	})
	router.All("/params/:myPathParam", func(c *fiber.Ctx) error {
		return c.SendString("Hello World!\n")
	})
	router.All("/body", func(c *fiber.Ctx) error {
		var body struct {
			Name string `json:"name"`
		}
		if err := c.BodyParser(&body); err != nil {
			return err
		}
		// The error is deliberately ignored so that the test also covers a
		// blocking response replacing one the handler already produced.
		appsec.MonitorParsedHTTPBody(c.UserContext(), body)
		return c.SendString("Hello Body!\n")
	})
	return router
}

func TestAppSec(t *testing.T) {
	t.Setenv("DD_APPSEC_ENABLED", "true")
	testutils.StartAppSec(t)

	router := newAppSecRouter()

	// An LFI attack in the request URI (appsec rule crs-930-110).
	t.Run("request-uri", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req := httptest.NewRequest("POST", "/../../../secret.txt", nil)
		res, err := router.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		event, ok := spans[0].Tag("_dd.appsec.json").(string)
		require.True(t, ok, "expected an appsec event on the request span")
		require.Contains(t, event, "server.request.uri.raw")
		require.Contains(t, event, "crs-930-110")
	})

	// A security scanner attack in a route parameter (appsec rule crs-913-120).
	// Wrap installs a guard that reports parameters after route matching,
	// before the user handler runs.
	t.Run("path-params", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req := httptest.NewRequest("POST", "/params/appscan_fingerprint", nil)
		res, err := router.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		event, ok := spans[0].Tag("_dd.appsec.json").(string)
		require.True(t, ok, "expected an appsec event on the request span")
		require.Contains(t, event, "crs-913-120")
		require.Contains(t, event, "server.request.path_params")
		require.Contains(t, event, "myPathParam")
	})

	// A PHP injection attack in the parsed body, reported through the SDK,
	// which is only reachable if the operation made it into the user context.
	t.Run("SDK-body", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req := httptest.NewRequest("POST", "/body", strings.NewReader(`{"name":"$globals"}`))
		req.Header.Set("Content-Type", "application/json")
		res, err := router.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello Body!\n", string(body))

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		event, ok := spans[0].Tag("_dd.appsec.json").(string)
		require.True(t, ok, "expected an appsec event on the request span")
		require.Contains(t, event, "crs-933-130")
	})

	t.Run("no-event", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := router.Test(httptest.NewRequest("GET", "/", nil))
		require.NoError(t, err)
		defer res.Body.Close()
		require.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(body))

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		require.Nil(t, spans[0].Tag("_dd.appsec.json"))
	})
}

func TestAppSecBlocking(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	testutils.StartAppSec(t)

	router := newAppSecRouter()

	// The blocked client IP is known before the handler runs, so the handler
	// must never be reached.
	t.Run("block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req := httptest.NewRequest("POST", "/", nil)
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		res, err := router.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, "application/json", res.Header.Get("Content-Type"))
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NotContains(t, string(body), "Hello World!")

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		require.Equal(t, "403", spans[0].Tag("http.status_code"))
		require.NotNil(t, spans[0].Tag("_dd.appsec.json"))
	})

	t.Run("block-html", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req := httptest.NewRequest("POST", "/", nil)
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		req.Header.Set("Accept", "text/html")
		res, err := router.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusForbidden, res.StatusCode)
		require.Equal(t, "text/html", res.Header.Get("Content-Type"))
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), "<!DOCTYPE html>")
		require.NotContains(t, string(body), "Hello World!")
		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		require.Equal(t, "403", spans[0].Tag("http.status_code"))
		require.NotNil(t, spans[0].Tag("_dd.appsec.json"))
	})

	// The body is only seen once the handler has run and written its own
	// response, so the blocking response has to replace it.
	t.Run("body-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req := httptest.NewRequest("POST", "/body", strings.NewReader(`{"name":"$globals"}`))
		req.Header.Set("Content-Type", "application/json")
		res, err := router.Test(req)
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusForbidden, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.NotContains(t, string(body), "Hello Body!")

		spans := mt.FinishedSpans()
		require.Len(t, spans, 1)
		require.Equal(t, "403", spans[0].Tag("http.status_code"))
	})

	t.Run("no-block", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		res, err := router.Test(httptest.NewRequest("POST", "/", nil))
		require.NoError(t, err)
		defer res.Body.Close()

		require.Equal(t, http.StatusOK, res.StatusCode)
		body, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello World!\n", string(body))
	})
}
