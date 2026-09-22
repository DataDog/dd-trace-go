// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2026 Datadog, Inc.

package fiber

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/stretchr/testify/require"
	"github.com/valyala/fasthttp"

	"github.com/DataDog/dd-trace-go/v2/appsec"
	"github.com/DataDog/dd-trace-go/v2/ddtrace/mocktracer"
	"github.com/DataDog/dd-trace-go/v2/instrumentation/testutils"
)

func TestConvertRequest(t *testing.T) {
	var fctx fasthttp.RequestCtx
	const target = "/a%zz?name=$globals;x=1&name=two&raw=%zz&plus=a+b&escaped=a%2Bb"
	fctx.Request.SetRequestURI(target)
	fctx.Request.Header.SetMethod("POST")
	fctx.Request.Header.SetHost("example.com")
	fctx.Request.Header.Add("X-Test", "one")
	fctx.Request.Header.Add("X-Test", "two")
	fctx.Request.Header.SetCookie("session", "benign")
	type contextKey struct{}
	ctx := context.WithValue(context.Background(), contextKey{}, "parent")
	req := convertRequest(ctx, &fctx)
	require.Equal(t, target, req.RequestURI)
	require.Equal(t, "POST", req.Method)
	require.Equal(t, "example.com", req.Host)
	require.Equal(t, "parent", req.Context().Value(contextKey{}))
	require.Nil(t, req.Body)
	require.Equal(t, []string{"$globals;x=1", "two"}, req.URL.Query()["name"])
	require.Equal(t, "%zz", req.URL.Query().Get("raw"))
	require.Equal(t, "a b", req.URL.Query().Get("plus"))
	require.Equal(t, "a+b", req.URL.Query().Get("escaped"))
	cookie, err := req.Cookie("session")
	require.NoError(t, err)
	require.Equal(t, "benign", cookie.Value)

	fctx.Request.Reset()
	fctx.Request.SetRequestURI("/replacement?name=changed")
	fctx.Request.Header.SetMethod("DELETE")
	fctx.Request.Header.SetHost("replacement.com")
	fctx.Request.Header.Set("X-Test", "replacement")
	require.Equal(t, target, req.RequestURI)
	require.Equal(t, "POST", req.Method)
	require.Equal(t, "example.com", req.Host)
	require.Equal(t, []string{"one", "two"}, req.Header.Values("X-Test"))
	require.Equal(t, []string{"$globals;x=1", "two"}, req.URL.Query()["name"])
}

func TestAppSecResponseHeaders(t *testing.T) {
	var fctx fasthttp.RequestCtx
	fctx.Response.Header.Set("Content-Type", "text/plain")
	fctx.Response.Header.Add("X-Test", "one")
	fctx.Response.Header.Add("X-Test", "two")
	fctx.Response.Header.Add("Set-Cookie", "first=one")
	fctx.Response.Header.Add("Set-Cookie", "second=two")
	headers := responseHeaders(&fctx)
	fctx.Response.Header.Reset()
	fctx.Response.Header.Set("X-Test", "replacement")
	require.Equal(t, "text/plain", headers.Get("Content-Type"))
	require.Equal(t, []string{"one", "two"}, headers.Values("X-Test"))
	require.Equal(t, []string{"first=one", "second=two"}, headers.Values("Set-Cookie"))
}

func TestAppSecRequestParsing(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	testutils.StartAppSec(t)

	for _, tc := range []struct {
		name, target, header, address, rule string
	}{
		{"IP-control", "/ok", "X-Forwarded-For: 1.2.3.4\r\n", "http.client_ip", "blk-001-001"},
		{"invalid-path-escape", "/%zz", "X-Forwarded-For: 1.2.3.4\r\n", "http.client_ip", "blk-001-001"},
		{"short-path-escape", "/a%2", "X-Forwarded-For: 1.2.3.4\r\n", "http.client_ip", "blk-001-001"},
		{"query-control", "/?name=$globals", "", "server.request.query", "crs-933-130-block"},
		{"query-semicolon", "/?name=$globals;x=1", "", "server.request.query", "crs-933-130-block"},
		{"query-invalid-escape", "/?name=$globals%zz", "", "server.request.query", "crs-933-130-block"},
		{"query-repeated-key", "/?name=benign&name=$globals", "", "server.request.query", "crs-933-130-block"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var calls atomic.Int32
			router := fiber.New()
			router.Use(Middleware())
			router.Use(func(c *fiber.Ctx) error {
				calls.Add(1)
				return c.SendString("handler response")
			})

			// A raw request keeps invalid escapes intact; net/http would escape them.
			status, headers, body := appSecRawRequest(t, serveOnPipe(t, router), tc.target, tc.header)
			require.Equal(t, http.StatusForbidden, status)
			require.Zero(t, calls.Load(), "an early block must prevent handler execution")
			require.Equal(t, "application/json", headers.Get("Content-Type"))
			require.True(t, json.Valid([]byte(body)), "the block response must be valid JSON")
			require.Contains(t, body, "You've been blocked")
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			require.Equal(t, "403", spans[0].Tag("http.status_code"))
			event, ok := spans[0].Tag("_dd.appsec.json").(string)
			require.True(t, ok)
			require.Contains(t, event, tc.address)
			require.Contains(t, event, tc.rule)
		})
	}
}

func TestAppSecErrorResponses(t *testing.T) {
	testutils.StartAppSec(t)
	for _, tc := range []struct {
		name         string
		handler      fiber.Handler
		errorHandler fiber.ErrorHandler
		status       int
		calls        int32
	}{
		{"unmatched-route", nil, fiber.DefaultErrorHandler, 404, 1},
		{"returned-error", func(*fiber.Ctx) error { return fiber.ErrNotFound }, fiber.DefaultErrorHandler, 404, 1},
		{"explicit-status", func(c *fiber.Ctx) error { return c.SendStatus(404) }, fiber.DefaultErrorHandler, 404, 0},
		{"custom-error-handler", func(*fiber.Ctx) error { return fiber.ErrBadRequest }, func(c *fiber.Ctx, _ error) error {
			c.Set("X-Error-Handler", "custom")
			return c.Status(404).SendString("custom error")
		}, 404, 1},
		{"error-handler-failure", func(*fiber.Ctx) error { return fiber.ErrBadRequest }, func(*fiber.Ctx, error) error {
			return fiber.ErrInternalServerError
		}, 500, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var calls atomic.Int32
			router := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
				calls.Add(1)
				return tc.errorHandler(c, err)
			}})
			router.Use(Middleware())
			if tc.handler != nil {
				router.Post("/etc/passwd", tc.handler)
			}
			res, err := router.Test(httptest.NewRequest("POST", "/etc/passwd", nil))
			require.NoError(t, err)
			defer res.Body.Close()
			require.Equal(t, tc.status, res.StatusCode)
			require.Equal(t, tc.calls, calls.Load(), "unexpected error handler call count")
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			require.Equal(t, strconv.Itoa(tc.status), spans[0].Tag("http.status_code"))
			if tc.calls != 0 {
				require.NotNil(t, spans[0].Tag("error.message"))
			}
			if tc.status == 404 {
				event, ok := spans[0].Tag("_dd.appsec.json").(string)
				require.True(t, ok)
				require.Contains(t, event, "nfd-000-001")
				require.Contains(t, event, "server.response.status")
			}
			if tc.name == "custom-error-handler" {
				require.Equal(t, "custom", res.Header.Get("X-Error-Handler"))
				body, err := io.ReadAll(res.Body)
				require.NoError(t, err)
				require.Equal(t, "custom error", string(body))
			}
		})
	}
}

func TestAppSecOuterMiddlewareErrors(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			if enabled {
				testutils.StartAppSec(t)
			}
			mt := mocktracer.Start()
			defer mt.Stop()
			var sawError atomic.Bool
			var errorCalls atomic.Int32
			router := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
				errorCalls.Add(1)
				return fiber.DefaultErrorHandler(c, err)
			}})
			router.Use(func(c *fiber.Ctx) error {
				err := c.Next()
				sawError.Store(err != nil)
				return err
			})
			router.Use(Middleware())
			router.Get("/error", func(*fiber.Ctx) error { return fiber.ErrNotFound })
			res, err := router.Test(httptest.NewRequest("GET", "/error", nil))
			require.NoError(t, err)
			defer res.Body.Close()
			require.Equal(t, http.StatusNotFound, res.StatusCode)
			require.Equal(t, int32(1), errorCalls.Load())
			// AppSec consumes rendered errors so the WAF can inspect the final
			// response without Fiber or outer middleware rendering it again.
			require.Equal(t, !enabled, sawError.Load())
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			require.Equal(t, fiber.ErrNotFound.Error(), spans[0].Tag("error.message"))
		})
	}
}

func TestAppSecEarlyBlockReplacesExistingResponse(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	testutils.StartAppSec(t)
	for _, contentType := range []string{"application/json", "text/html"} {
		t.Run(contentType, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var calls atomic.Int32
			var headerSettingsPreserved atomic.Bool
			router := fiber.New(fiber.Config{
				DisableDefaultDate:        true,
				DisableDefaultContentType: true,
				DisableHeaderNormalizing:  true,
			})
			router.Use(func(c *fiber.Ctx) error {
				c.Context().SetConnectionClose()
				c.Status(http.StatusAccepted)
				c.Context().Response.Header.SetStatusMessage([]byte("must-not-escape"))
				c.Set("Content-Type", "text/plain")
				c.Set("X-Before-Tracing", "must not escape")
				c.Cookie(&fiber.Cookie{Name: "session", Value: "must-not-escape"})
				c.Cookie(&fiber.Cookie{Name: "second-session", Value: "must-not-escape"})
				if _, err := c.WriteString("existing response prefix:"); err != nil {
					return err
				}
				err := c.Next()
				var headers fasthttp.ResponseHeader
				c.Context().Response.Header.CopyTo(&headers)
				headers.Del("Content-Type")
				headerSettingsPreserved.Store(headers.DisableNormalizing() && len(headers.ContentType()) == 0)
				return err
			})
			router.Use(Middleware())
			router.Get("/", func(c *fiber.Ctx) error {
				calls.Add(1)
				_, err := c.WriteString("allowed")
				return err
			})
			req := httptest.NewRequest("GET", "/", nil)
			req.Header.Set("X-Forwarded-For", "1.2.3.4")
			req.Header.Set("Accept", contentType)
			res, err := router.Test(req)
			require.NoError(t, err)
			body, err := io.ReadAll(res.Body)
			require.NoError(t, res.Body.Close())
			require.NoError(t, err)
			require.Equal(t, http.StatusForbidden, res.StatusCode)
			require.Zero(t, calls.Load())
			require.Equal(t, contentType, res.Header.Get("Content-Type"))
			require.Equal(t, "403 Forbidden", res.Status)
			require.Empty(t, res.Header.Get("Date"))
			require.True(t, res.Close)
			require.True(t, headerSettingsPreserved.Load())
			require.Empty(t, res.Header.Get("X-Before-Tracing"))
			require.Empty(t, res.Header.Values("Set-Cookie"))
			require.NotContains(t, string(body), "existing response prefix:")
			require.Contains(t, string(body), "You've been blocked")
			if contentType == "application/json" {
				require.True(t, json.Valid(body))
			} else {
				require.Contains(t, string(body), "<!DOCTYPE html>")
			}
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			require.Equal(t, "403", spans[0].Tag("http.status_code"))

			req.Header.Del("X-Forwarded-For")
			headerSettingsPreserved.Store(false)
			res, err = router.Test(req)
			require.NoError(t, err)
			body, err = io.ReadAll(res.Body)
			require.NoError(t, res.Body.Close())
			require.NoError(t, err)
			require.Equal(t, http.StatusAccepted, res.StatusCode)
			require.Equal(t, int32(1), calls.Load())
			require.Equal(t, "existing response prefix:allowed", string(body))
			require.Equal(t, "202 must-not-escape", res.Status)
			require.True(t, headerSettingsPreserved.Load())
			require.Equal(t, "must not escape", res.Header.Get("X-Before-Tracing"))
			require.Len(t, res.Header.Values("Set-Cookie"), 2)
		})
	}
}

func TestAppSecBlocksRenderedError(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "testdata/response-blocking.json")
	testutils.StartAppSec(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var errorCalls atomic.Int32
	router := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, _ error) error {
		errorCalls.Add(1)
		c.Set("X-Error-Handler", "must not escape")
		c.Cookie(&fiber.Cookie{Name: "session", Value: "must-not-escape"})
		return c.Status(http.StatusNotFound).SendString("error response must not escape")
	}})
	router.Use(Middleware())
	router.Get("/error", func(*fiber.Ctx) error { return fiber.ErrNotFound })
	res, err := router.Test(httptest.NewRequest("GET", "/error", nil))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, int32(1), errorCalls.Load())
	require.Equal(t, http.StatusForbidden, res.StatusCode)
	require.Equal(t, "application/json", res.Header.Get("Content-Type"))
	require.Empty(t, res.Header.Get("X-Error-Handler"))
	require.Empty(t, res.Header.Values("Set-Cookie"))
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	require.NotContains(t, string(body), "error response must not escape")
	require.Contains(t, string(body), "You've been blocked")
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	require.Equal(t, "403", spans[0].Tag("http.status_code"))
	require.Nil(t, spans[0].Tag("error.message"))
	event, ok := spans[0].Tag("_dd.appsec.json").(string)
	require.True(t, ok)
	require.Contains(t, event, "fiber-block-error-response")
	require.Contains(t, event, "server.response.status")
}

func TestAppSecLateBlocking(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	testutils.StartAppSec(t)
	for _, mode := range []string{"path-params", "body", "body-security-error", "body-handler-error", "path-handler-error"} {
		t.Run(mode, func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			var calls, errorCalls atomic.Int32
			router := fiber.New(fiber.Config{ErrorHandler: func(c *fiber.Ctx, err error) error {
				errorCalls.Add(1)
				return fiber.DefaultErrorHandler(c, err)
			}})
			router.Use(Middleware())
			router.Post("/params/:value", func(c *fiber.Ctx) error {
				calls.Add(1)
				c.Set("Content-Type", "text/plain")
				c.Set("X-Handler", "must not escape")
				c.Cookie(&fiber.Cookie{Name: "session", Value: "must-not-escape"})
				if err := c.SendString("handler response"); err != nil {
					return err
				}
				if mode == "path-handler-error" {
					return fiber.ErrBadRequest
				}
				if mode == "path-params" {
					return nil
				}
				err := appsec.MonitorParsedHTTPBody(c.UserContext(), map[string]string{"name": "$globals"})
				if mode == "body-security-error" {
					return err
				}
				if mode == "body-handler-error" {
					return fiber.ErrBadRequest
				}
				return nil
			})
			target := "/params/benign"
			if mode == "path-params" || mode == "path-handler-error" {
				target = "/params/$globals"
			}
			res, err := router.Test(httptest.NewRequest("POST", target, nil))
			require.NoError(t, err)
			defer res.Body.Close()
			require.Equal(t, int32(1), calls.Load())
			require.Zero(t, errorCalls.Load(), "the error handler must not replace a block")
			require.Equal(t, http.StatusForbidden, res.StatusCode)
			require.Equal(t, "application/json", res.Header.Get("Content-Type"))
			require.Empty(t, res.Header.Values("Set-Cookie"))
			require.Empty(t, res.Header.Get("X-Handler"))
			body, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.NotContains(t, string(body), "handler response")
			require.Contains(t, string(body), "You've been blocked")
			spans := mt.FinishedSpans()
			require.Len(t, spans, 1)
			require.Equal(t, "403", spans[0].Tag("http.status_code"))
			require.Nil(t, spans[0].Tag("error.message"))
			event, ok := spans[0].Tag("_dd.appsec.json").(string)
			require.True(t, ok)
			require.Contains(t, event, "crs-933-130-block")
			if mode == "path-params" || mode == "path-handler-error" {
				require.Contains(t, event, "server.request.path_params")
			} else {
				require.Contains(t, event, "server.request.body")
			}
		})
	}
}

func TestAppSecPanic(t *testing.T) {
	testutils.StartAppSec(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	router := fiber.New()
	router.Use(recover.New())
	router.Use(Middleware())
	router.Use(func(*fiber.Ctx) error { panic("handler panic") })
	res, err := router.Test(httptest.NewRequest("GET", "/../../../secret.txt", nil))
	require.NoError(t, err)
	defer res.Body.Close()
	require.Equal(t, http.StatusInternalServerError, res.StatusCode)
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	event, ok := spans[0].Tag("_dd.appsec.json").(string)
	require.True(t, ok, "the WAF operation must finish even when the handler panics")
	require.Contains(t, event, "crs-930-110")
}

func TestAppSecKeepAliveAfterBlock(t *testing.T) {
	t.Setenv("DD_APPSEC_RULES", "../../../internal/appsec/testdata/blocking.json")
	testutils.StartAppSec(t)
	mt := mocktracer.Start()
	defer mt.Stop()
	var calls atomic.Int32
	router := fiber.New()
	router.Use(Middleware())
	router.Use(func(c *fiber.Ctx) error {
		calls.Add(1)
		return c.SendString("benign response")
	})
	conn := serveOnPipe(t, router)
	status, _, _ := appSecRawRequest(t, conn, "/blocked", "X-Forwarded-For: 1.2.3.4\r\n")
	require.Equal(t, http.StatusForbidden, status)
	require.Zero(t, calls.Load())
	spans := mt.FinishedSpans()
	require.Len(t, spans, 1)
	event := spans[0].Tag("_dd.appsec.json")
	require.NotNil(t, event)
	status, _, body := appSecRawRequest(t, conn, "/a-longer-benign-request", "")
	require.Equal(t, http.StatusOK, status)
	require.Equal(t, "benign response", body)
	require.Equal(t, int32(1), calls.Load())
	spans = mt.FinishedSpans()
	require.Len(t, spans, 2)
	require.Equal(t, event, spans[0].Tag("_dd.appsec.json"))
	require.Nil(t, spans[1].Tag("_dd.appsec.json"))
}

func appSecRawRequest(t *testing.T, conn *pipedConn, target, headers string) (int, http.Header, string) {
	t.Helper()
	require.NoError(t, conn.SetDeadline(time.Now().Add(10*time.Second)))
	_, err := fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: example.com\r\n%s\r\n", target, headers)
	require.NoError(t, err)
	res, err := http.ReadResponse(conn.r, nil)
	require.NoError(t, err)
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	return res.StatusCode, res.Header, string(body)
}
