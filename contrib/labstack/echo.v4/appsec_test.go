// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2023 Datadog, Inc.

package echo

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	pappsec "gopkg.in/DataDog/dd-trace-go.v1/appsec"
	"gopkg.in/DataDog/dd-trace-go.v1/ddtrace/mocktracer"
	"gopkg.in/DataDog/dd-trace-go.v1/internal/appsec"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/require"
)

func TestAppSec(t *testing.T) {
	appsec.Start()
	defer appsec.Stop()

	if !appsec.Enabled() {
		t.Skip("appsec disabled")
	}

	// Start and trace an HTTP server
	e := echo.New()
	e.Use(Middleware())

	// Add some testing routes
	e.POST("/path0.0/:myPathParam0/path0.1/:myPathParam1/path0.2/:myPathParam2/path0.3/*myPathParam3", func(c echo.Context) error {
		return c.String(200, "Hello World!\n")
	})
	e.POST("/", func(c echo.Context) error {
		return c.String(200, "Hello World!\n")
	})
	e.POST("/body", func(c echo.Context) error {
		pappsec.MonitorParsedHTTPBody(c.Request().Context(), "$globals")
		return c.String(200, "Hello Body!\n")
	})
	srv := httptest.NewServer(e)
	defer srv.Close()

	// Test an LFI attack via path parameters
	t.Run("request-uri", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()
		// Send an LFI attack (according to appsec rule id crs-930-110)
		req, err := http.NewRequest("POST", srv.URL+"/../../../secret.txt", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		// Check that the server behaved as intended (no 301 but 404 directly)
		require.Equal(t, http.StatusNotFound, res.StatusCode)
		// The span should contain the security event
		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		event := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "crs-930-110"))
		require.True(t, strings.Contains(event, "server.request.uri.raw"))
	})

	// Test a security scanner attack via path parameters
	t.Run("path-params", func(t *testing.T) {
		t.Run("regular", func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			// Send a security scanner attack (according to appsec rule id crs-913-120)
			req, err := http.NewRequest("POST", srv.URL+"/path0.0/param0/path0.1/param1/path0.2/appscan_fingerprint/path0.3/param3", nil)
			if err != nil {
				panic(err)
			}
			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			// Check that the handler was properly called
			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Equal(t, "Hello World!\n", string(b))
			require.Equal(t, http.StatusOK, res.StatusCode)
			// The span should contain the security event
			finished := mt.FinishedSpans()
			require.Len(t, finished, 1)
			event := finished[0].Tag("_dd.appsec.json").(string)
			require.NotNil(t, event)
			require.True(t, strings.Contains(event, "crs-913-120"))
			require.True(t, strings.Contains(event, "myPathParam2"))
			require.True(t, strings.Contains(event, "server.request.path_params"))
		})

		t.Run("wildcard", func(t *testing.T) {
			mt := mocktracer.Start()
			defer mt.Stop()
			// Send a security scanner attack (according to appsec rule id crs-913-120)
			req, err := http.NewRequest("POST", srv.URL+"/path0.0/param0/path0.1/param1/path0.2/param2/path0.3/appscan_fingerprint", nil)
			if err != nil {
				panic(err)
			}
			res, err := srv.Client().Do(req)
			require.NoError(t, err)
			// Check that the handler was properly called
			b, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			require.Equal(t, "Hello World!\n", string(b))
			require.Equal(t, http.StatusOK, res.StatusCode)
			// The span should contain the security event
			finished := mt.FinishedSpans()
			require.Len(t, finished, 1)
			event := finished[0].Tag("_dd.appsec.json").(string)
			require.NotNil(t, event)
			require.True(t, strings.Contains(event, "crs-913-120"))
			// Wildcards are not named in echo
			require.False(t, strings.Contains(event, "myPathParam3"))
			require.True(t, strings.Contains(event, "server.request.path_params"))
		})
	})

	t.Run("response-status", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srv.URL+"/etc/", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		require.Equal(t, 404, res.StatusCode)

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		event := finished[0].Tag("_dd.appsec.json").(string)
		require.NotNil(t, event)
		require.True(t, strings.Contains(event, "server.response.status"))
		require.True(t, strings.Contains(event, "nfd-000-001"))
	})

	// Test a PHP injection attack via request parsed body
	t.Run("SDK-body", func(t *testing.T) {
		mt := mocktracer.Start()
		defer mt.Stop()

		req, err := http.NewRequest("POST", srv.URL+"/body", nil)
		if err != nil {
			panic(err)
		}
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		// Check that the handler was properly called
		b, err := io.ReadAll(res.Body)
		require.NoError(t, err)
		require.Equal(t, "Hello Body!\n", string(b))

		finished := mt.FinishedSpans()
		require.Len(t, finished, 1)
		event := finished[0].Tag("_dd.appsec.json")
		require.NotNil(t, event)
		require.True(t, strings.Contains(event.(string), "crs-933-130"))
	})
}

func TestControlFlow(t *testing.T) {

	middlewareResponseBody := "Hello Middleware"
	middlewareResponseStatus := 433
	handlerResponseBody := "Hello Handler"
	handlerResponseStatus := 533

	for _, tc := range []struct {
		name        string
		middlewares []echo.MiddlewareFunc
		handler     func(echo.Context) error
		test        func(t *testing.T, rec *httptest.ResponseRecorder, err error)
	}{
		{
			name: "middleware-first/middleware-aborts-before-handler",
			middlewares: []echo.MiddlewareFunc{
				Middleware(),
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						c.String(middlewareResponseStatus, middlewareResponseBody)
						return errors.New("middleware abort")
					}
				},
			},
			handler: func(echo.Context) error {
				panic("unexpected control flow")
				return nil
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, err error) {
				require.Error(t, err)
				require.Equal(t, "middleware abort", err.Error())
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody, rec.Body.String())
			},
		},
		{
			name: "middleware-first/handler-aborts",
			middlewares: []echo.MiddlewareFunc{
				Middleware(),
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := next(c)
						require.Error(t, err)
						return err
					}
				},
			},
			handler: func(c echo.Context) error {
				c.String(handlerResponseStatus, handlerResponseBody)
				return errors.New("handler abort")
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, err error) {
				require.Error(t, err)
				require.Equal(t, "handler abort", err.Error())
				require.Equal(t, handlerResponseStatus, rec.Code)
				require.Equal(t, handlerResponseBody, rec.Body.String())
			},
		},
		{
			name: "middleware-first/no-aborts",
			middlewares: []echo.MiddlewareFunc{
				Middleware(),
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						err = next(c)
						require.NoError(t, err)
						err = c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						return err
					}
				},
			},
			handler: func(c echo.Context) error {
				return c.String(handlerResponseStatus, handlerResponseBody)
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, err error) {
				require.NoError(t, err)
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody+handlerResponseBody+middlewareResponseBody, rec.Body.String())
			},
		},
		{
			name: "middleware-first/middleware-aborts-after-handler",
			middlewares: []echo.MiddlewareFunc{
				Middleware(),
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := next(c)
						require.NoError(t, err)
						err = c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						return errors.New("middleware abort")
					}
				},
			},
			handler: func(c echo.Context) error {
				return nil
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, err error) {
				require.Error(t, err)
				require.Equal(t, "middleware abort", err.Error())
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody, rec.Body.String())
			},
		},
		{
			name: "middleware-after/middleware-aborts-after-next-handler",
			middlewares: []echo.MiddlewareFunc{
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := next(c)
						require.NoError(t, err)
						err = c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						return errors.New("middleware abort")
					}
				},
				Middleware(),
			},
			handler: func(c echo.Context) error {
				// Do nothing so that the calling middleware can handle the response.
				return nil
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, err error) {
				require.Error(t, err)
				require.Equal(t, "middleware abort", err.Error())
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody, rec.Body.String())
			},
		},
		{
			name: "middleware-after/middleware-aborts-before-next-handler",
			middlewares: []echo.MiddlewareFunc{
				func(echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						return errors.New("middleware abort")
					}
				},
				func(echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						// Make sure echo doesn't call the next middleware when the
						// previous one returns an error.
						panic("unexpected control flow")
					}
				},
				Middleware(),
			},
			handler: func(echo.Context) error {
				// Make sure echo doesn't call the handler when one of the
				// previous middlewares return an error.
				panic("unexpected control flow")
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, err error) {
				require.Error(t, err)
				require.Equal(t, "middleware abort", err.Error())
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody, rec.Body.String())
			},
		},
		{
			name: "middleware-after/handler-aborts",
			middlewares: []echo.MiddlewareFunc{
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := next(c)
						require.Error(t, err)
						return err
					}
				},
				Middleware(),
			},
			handler: func(c echo.Context) error {
				err := c.String(handlerResponseStatus, handlerResponseBody)
				require.NoError(t, err)
				return errors.New("handler abort")
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, err error) {
				require.Error(t, err)
				require.Equal(t, "handler abort", err.Error())
				require.Equal(t, handlerResponseStatus, rec.Code)
				require.Equal(t, handlerResponseBody, rec.Body.String())
			},
		},
		{
			name: "middleware-after/no-aborts",
			middlewares: []echo.MiddlewareFunc{
				func(next echo.HandlerFunc) echo.HandlerFunc {
					return func(c echo.Context) error {
						err := c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						err = next(c)
						require.NoError(t, err)
						err = c.String(middlewareResponseStatus, middlewareResponseBody)
						require.NoError(t, err)
						return nil
					}
				},
				Middleware(),
			},
			handler: func(c echo.Context) error {
				err := c.String(handlerResponseStatus, handlerResponseBody)
				require.NoError(t, err)
				return nil
			},
			test: func(t *testing.T, rec *httptest.ResponseRecorder, err error) {
				require.NoError(t, err)
				require.Equal(t, middlewareResponseStatus, rec.Code)
				require.Equal(t, middlewareResponseBody+handlerResponseBody+middlewareResponseBody, rec.Body.String())
			},
		},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// Create a echo router
			router := echo.New()
			// Setup the middleware
			router.Use(tc.middlewares...)
			// Add the endpoint
			router.GET("/", tc.handler)

			// Perform the request and record the output
			rec := httptest.NewRecorder()
			req, _ := http.NewRequest("GET", "/", nil)
			var err error
			router.HTTPErrorHandler = func(e error, _ echo.Context) {
				err = e
			}
			router.ServeHTTP(rec, req)

			// Check the request was performed as expected
			tc.test(t, rec, err)
		})
	}
}
